package skills

// catalog_loader.go — file-format decoders for skills loaded from disk.
// Three formats are accepted, in priority order:
//
//   1. Native DFMC YAML (canonical: name + description + prompt/system_prompt
//      + task/role/profile + preferred_tools/allowed_tools).
//   2. Agent Skills SKILL.md (YAML frontmatter + markdown body, with
//      `allowed-tools` as a space-separated string).
//   3. Generic best-effort YAML map fallback.
//
// readSkillFile is the entry point; it tries each format in order and
// returns the first parse that yields a non-empty Name. The two
// string-list helpers (parseStringList, cleanStringList) are shared with
// the catalog core. Live separately from catalog.go so adding a new
// frontmatter dialect doesn't push the discovery / selection logic
// around.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// extractMarkdownBody splits a SKILL.md body (YAML frontmatter + markdown)
// and returns the markdown portion. Returns empty string if the file
// doesn't look like a SKILL.md.
func extractMarkdownBody(data []byte) string {
	body := string(data)
	if !strings.HasPrefix(body, "---") {
		return ""
	}
	sep := "\n---"
	idx := strings.Index(body[3:], sep)
	if idx < 0 {
		return ""
	}
	frontEnd := 3 + idx + len(sep)
	return strings.TrimSpace(body[frontEnd:])
}

// tryAgentSkillFormat parses a SKILL.md file (YAML frontmatter + markdown body)
// and returns a Skill struct. Returns zero value if the file does not look like
// an Agent Skills SKILL.md.
func tryAgentSkillFormat(data []byte, path, source string) Skill {
	// Split YAML frontmatter from markdown body.
	body := string(data)
	if !strings.HasPrefix(body, "---") {
		return Skill{}
	}
	sep := "\n---"
	idx := strings.Index(body[3:], sep)
	if idx < 0 {
		return Skill{}
	}
	frontEnd := 3 + idx + len(sep)
	frontMatter := strings.TrimSpace(body[3 : 3+idx])
	markdownBody := strings.TrimSpace(body[frontEnd:])
	if markdownBody == "" {
		return Skill{}
	}

	raw := map[string]any{}
	if err := yaml.Unmarshal([]byte(frontMatter), &raw); err != nil {
		return Skill{}
	}
	nameRaw, ok := raw["name"]
	if !ok {
		return Skill{}
	}
	name := strings.TrimSpace(fmt.Sprint(nameRaw))
	if name == "" {
		return Skill{}
	}
	// Validate filename matches skill name (Agent Skills spec requirement).
	// For <name>.SKILL.md: strip ".md" then ".SKILL" to get <name>.
	// For <name>.md: strip ".md" to get <name>.
	filename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.HasSuffix(strings.ToLower(filename), ".skill") {
		filename = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	if !strings.EqualFold(name, filename) {
		// Soft error per the Agent Skills spec — surface it so operators
		// can spot a SKILL.md whose `name:` drifted from its on-disk
		// filename instead of the mismatch silently passing through. We
		// still proceed; the in-frontmatter name wins.
		log.Printf("skills: SKILL.md %q: frontmatter name %q does not match filename %q (using frontmatter)", path, name, filename)
	}
	description := ""
	if v, ok := raw["description"]; ok {
		description = strings.TrimSpace(fmt.Sprint(v))
	}
	// Build system_prompt from markdown body.
	system := markdownBody

	// allowed-tools is space-separated; convert to our []string.
	var allowed []string
	if v, ok := raw["allowed-tools"].(string); ok && strings.TrimSpace(v) != "" {
		for tok := range strings.FieldsSeq(v) {
			if t := strings.TrimSpace(tok); t != "" {
				allowed = append(allowed, t)
			}
		}
	}

	// compatibility and metadata are informational only — stash in description.
	var extra []string
	if v, ok := raw["compatibility"].(string); ok && strings.TrimSpace(v) != "" {
		extra = append(extra, "compatibility: "+strings.TrimSpace(v))
	}
	if meta, ok := raw["metadata"].(map[string]any); ok {
		for k, rv := range meta {
			extra = append(extra, k+"="+fmt.Sprint(rv))
		}
	}
	desc := description
	if len(extra) > 0 {
		desc = desc + " (" + strings.Join(extra, ", ") + ")"
	}

	return Skill{
		Name:        name,
		Description: desc,
		Path:        path,
		Source:      source,
		Builtin:     false,
		System:      system,
		Allowed:     allowed,
	}
}

func readSkillFile(path, source string) Skill {
	item := Skill{
		Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Path:    path,
		Source:  source,
		Builtin: false,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return item
	}
	// If yaml.Unmarshal succeeds with a name, it's a native DFMC skill.
	if err := yaml.Unmarshal(data, &item); err == nil && strings.TrimSpace(item.Name) != "" {
		item.Source = source
		item.Path = path
		// SKILL.md files: yaml tag mapping misses `allowed-tools` (hyphen vs underscore).
		// If Allowed is still empty, re-parse as raw map and pull allowed-tools.
		if len(item.Allowed) == 0 && strings.HasPrefix(string(data), "---") {
			raw := map[string]any{}
			if yaml.Unmarshal(data, &raw) == nil {
				if v, ok := raw["allowed-tools"].(string); ok && strings.TrimSpace(v) != "" {
					for tok := range strings.FieldsSeq(v) {
						if t := strings.TrimSpace(tok); t != "" {
							item.Allowed = append(item.Allowed, t)
						}
					}
				}
			}
		}
		// SKILL.md: System field maps from system_prompt but the markdown body
		// is the actual skill content. Extract it when System is empty.
		if item.System == "" && strings.HasPrefix(string(data), "---") {
			if md := extractMarkdownBody(data); md != "" {
				item.System = md
			}
		}
		return item
	}

	// Not a native DFMC skill — try Agent Skills SKILL.md format.
	item = tryAgentSkillFormat(data, path, source)
	if strings.TrimSpace(item.Name) != "" {
		return item
	}

	// Last resort: generic map[string]any parse.
	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return item
	}
	if v, ok := raw["name"]; ok {
		if name := strings.TrimSpace(fmt.Sprint(v)); name != "" {
			item.Name = name
		}
	}
	if v, ok := raw["description"]; ok {
		item.Description = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["prompt"]; ok {
		item.Prompt = strings.TrimSpace(fmt.Sprint(v))
	}
	if item.Prompt == "" {
		if v, ok := raw["template"]; ok {
			item.Prompt = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	if v, ok := raw["system_prompt"]; ok {
		item.System = strings.TrimSpace(fmt.Sprint(v))
	}
	if item.System == "" {
		if v, ok := raw["system"]; ok {
			item.System = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	if v, ok := raw["task"]; ok {
		item.Task = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["role"]; ok {
		item.Role = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["profile"]; ok {
		item.Profile = strings.TrimSpace(fmt.Sprint(v))
	}
	item.Preferred = parseStringList(raw["preferred_tools"])
	item.Allowed = parseStringList(raw["allowed_tools"])
	return item
}

func parseStringList(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return nil
	case []string:
		return cleanStringList(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return cleanStringList(out)
	case string:
		return cleanStringList(strings.Split(v, ","))
	default:
		return cleanStringList([]string{fmt.Sprint(v)})
	}
}

func cleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
