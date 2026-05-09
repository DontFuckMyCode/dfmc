package skills

// validate.go — author-side lint for SKILL.md and native YAML
// skill files. Surfaces problems that the loader would otherwise
// silently mask: a malformed YAML, a missing `name:`, a broken
// regex pattern in `triggers:`, a `requires:` entry pointing at a
// skill the catalog doesn't ship.
//
// The loader is deliberately fail-soft (drops a bad trigger, logs
// once, keeps the rest of the skill alive) so that one fat-fingered
// pattern doesn't take down the whole catalog at runtime. That same
// softness hides authoring bugs at install time, which is when the
// author can actually fix them. ValidateSkillFile re-parses the file
// strictly — any of the same issues become a Diagnostic with a clear
// message and (when possible) a line number.
//
// `dfmc skill validate <path>` calls this before install/discover so
// authors get immediate, structured feedback instead of "the trigger
// silently never fires and you have no idea why".

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Severity grades a Diagnostic. Errors block install; warnings are
// surfaced but don't fail the operation.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is a single lint finding. Field is the YAML field that
// triggered the problem (empty for file-wide issues). Line is 1-based
// and 0 when unknown.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Field    string   `json:"field,omitempty"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
}

// recognised YAML field names — anything else triggers an "unknown
// field" warning. Keep in sync with the Skill struct + extended
// fields handled in catalog_loader.go.
var recognisedSkillFields = map[string]struct{}{
	"name":            {},
	"description":     {},
	"system_prompt":   {},
	"prompt":          {},
	"task":            {},
	"role":            {},
	"profile":         {},
	"preferred_tools": {},
	"allowed_tools":   {},
	"allowed-tools":   {},
	"version":         {},
	"compatibility":   {},
	"metadata":        {},
	"triggers":        {},
	"requires":        {},
}

// ValidateSkillFile reads `path` and returns a list of diagnostics.
// An empty list means the file is well-formed. Errors include
// missing-name, unparseable YAML, and invalid regex; warnings
// include missing-description (per agentskills.io spec, description
// is required but a missing one is recoverable) and unknown fields.
func ValidateSkillFile(path string) ([]Diagnostic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ValidateSkillBytes(data, filepath.Base(path)), nil
}

// ValidateSkillBytes is the in-memory variant. `displayPath` is used
// only in messages so callers can pass any name (e.g. "stdin").
func ValidateSkillBytes(data []byte, displayPath string) []Diagnostic {
	var diags []Diagnostic

	// Detect SKILL.md vs native YAML. Both formats route through the
	// same field validator below — the only difference is that SKILL.md
	// lives behind a `---` frontmatter block that must be split out
	// before YAML parsing.
	frontmatter := data
	isAgentSkill := strings.HasPrefix(string(data), "---")
	hasMarkdownBody := false
	if isAgentSkill {
		body := string(data)
		sep := "\n---"
		idx := strings.Index(body[3:], sep)
		if idx < 0 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  "SKILL.md frontmatter is missing a closing '---' separator",
			})
			return diags
		}
		frontEnd := 3 + idx
		frontmatter = []byte(strings.TrimSpace(body[3:frontEnd]))
		mdBody := strings.TrimSpace(body[frontEnd+len(sep):])
		hasMarkdownBody = mdBody != ""
		if !hasMarkdownBody {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Field:    "system_prompt",
				Message:  "SKILL.md has no markdown body — the body IS the skill's system prompt",
			})
		}
	}

	raw := map[string]any{}
	if err := yaml.Unmarshal(frontmatter, &raw); err != nil {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("YAML parse error: %v", err),
		})
		return diags
	}

	// name is mandatory.
	name := ""
	if v, ok := raw["name"]; ok {
		name = strings.TrimSpace(fmt.Sprint(v))
	}
	if name == "" {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Field:    "name",
			Message:  "missing required field 'name'",
		})
	} else if !skillNameRE.MatchString(name) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Field:    "name",
			Message:  fmt.Sprintf("name %q has invalid characters; allowed: letters, digits, dot, underscore, dash, slash", name),
		})
	}

	// description is required by agentskills.io. We only warn on
	// missing — the loader still accepts the file so it's recoverable.
	if v, ok := raw["description"]; !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Field:    "description",
			Message:  "missing 'description' (required by agentskills.io spec)",
		})
	}

	// Skill body must be present in some form. Native YAML can use
	// `system_prompt` or `prompt`; SKILL.md uses the markdown body.
	if !isAgentSkill {
		hasBody := false
		for _, key := range []string{"system_prompt", "prompt", "system"} {
			if v, ok := raw[key]; ok && strings.TrimSpace(fmt.Sprint(v)) != "" {
				hasBody = true
				break
			}
		}
		if !hasBody {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "system_prompt",
				Message:  "skill has no system_prompt / prompt body — runtime activation will be a no-op",
			})
		}
	}

	// Validate trigger regex syntax. parseTriggers also drops bad
	// patterns at runtime but does so silently — here we surface them.
	if rawTriggers, ok := raw["triggers"]; ok {
		diags = append(diags, validateTriggers(rawTriggers)...)
	}

	// Validate requires shape. We can't check the target skill
	// exists without a catalog reference, but we can at least flag
	// entries with no skill name.
	if rawRequires, ok := raw["requires"]; ok {
		diags = append(diags, validateRequires(rawRequires)...)
	}

	// Unknown fields (typos / spec drift). Warnings only — agentskills.io
	// may add fields we don't yet model.
	for key := range raw {
		if _, ok := recognisedSkillFields[key]; !ok {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    key,
				Message:  fmt.Sprintf("unknown field %q (typo? unsupported extension?)", key),
			})
		}
	}

	return diags
}

var skillNameRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func validateTriggers(raw any) []Diagnostic {
	var diags []Diagnostic
	list, ok := raw.([]any)
	if !ok {
		// parseTriggers also accepts a single string — same here.
		if _, ok := raw.(string); ok {
			return nil
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Field:    "triggers",
			Message:  "must be a list of strings or {pattern, weight} objects",
		})
		return diags
	}
	for i, item := range list {
		switch v := item.(type) {
		case string:
			pattern, _ := splitInlineWeight(v, defaultTriggerWeight)
			if pattern == "" {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Field:    fmt.Sprintf("triggers[%d]", i),
					Message:  "empty trigger pattern",
				})
				continue
			}
			if _, err := regexp.Compile("(?i)" + pattern); err != nil {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Field:    fmt.Sprintf("triggers[%d]", i),
					Message:  fmt.Sprintf("invalid regex %q: %v", pattern, err),
				})
			}
		case map[string]any:
			pattern := ""
			if rawPat, ok := v["pattern"]; ok {
				pattern = strings.TrimSpace(fmt.Sprint(rawPat))
			}
			if pattern == "" {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Field:    fmt.Sprintf("triggers[%d].pattern", i),
					Message:  "missing or empty 'pattern' field",
				})
				continue
			}
			if _, err := regexp.Compile("(?i)" + pattern); err != nil {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Field:    fmt.Sprintf("triggers[%d].pattern", i),
					Message:  fmt.Sprintf("invalid regex %q: %v", pattern, err),
				})
			}
		default:
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Field:    fmt.Sprintf("triggers[%d]", i),
				Message:  "trigger must be a string or {pattern, weight} object",
			})
		}
	}
	return diags
}

func validateRequires(raw any) []Diagnostic {
	var diags []Diagnostic
	list, ok := raw.([]any)
	if !ok {
		if _, ok := raw.(string); ok {
			return nil
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Field:    "requires",
			Message:  "must be a list of skill names or {skill, reason} objects",
		})
		return diags
	}
	for i, item := range list {
		switch v := item.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Field:    fmt.Sprintf("requires[%d]", i),
					Message:  "empty skill name",
				})
			}
		case map[string]any:
			skill := ""
			if rawSkill, ok := v["skill"]; ok {
				skill = strings.TrimSpace(fmt.Sprint(rawSkill))
			}
			if skill == "" {
				if rawSkill, ok := v["name"]; ok {
					skill = strings.TrimSpace(fmt.Sprint(rawSkill))
				}
			}
			if skill == "" {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Field:    fmt.Sprintf("requires[%d].skill", i),
					Message:  "missing 'skill' field",
				})
			}
		default:
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Field:    fmt.Sprintf("requires[%d]", i),
				Message:  "requires entry must be a string or {skill, reason} object",
			})
		}
	}
	return diags
}
