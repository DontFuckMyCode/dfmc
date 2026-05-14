package promptlib

// promptlib_decode.go — On-disk and embedded prompt-file loaders. YAML, JSON,
// and Markdown (with optional YAML frontmatter) all funnel through
// decodePromptFile so the directory walker only needs the bytes-and-path
// view. Core lifecycle (Library, New, LoadOverrides) lives in promptlib.go.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func loadPromptDir(root string) ([]Template, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", root)
	}

	out := make([]Template, 0, 16)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !isPromptFile(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		out = append(out, decodePromptFile(path, data)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func isPromptFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json", ".md":
		return true
	default:
		return false
	}
}

type templateDoc struct {
	Templates []Template `json:"templates" yaml:"templates"`
}

func decodePromptFile(path string, data []byte) []Template {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		return decodeYAMLTemplates(path, data)
	case ".json":
		return decodeJSONTemplates(path, data)
	case ".md":
		if t, ok := decodeMarkdownTemplate(path, data); ok {
			return []Template{t}
		}
	}
	return nil
}

func decodeYAMLTemplates(path string, data []byte) []Template {
	if len(data) == 0 {
		return nil
	}

	// Strategy 1: document-level templates array (the correct shipped format).
	// yaml.Unmarshal fails if the top-level is a list but the struct field is []Template
	// AND the YAML has extra map fields below — so we try both the array and map shapes.

	// Shape A: `templates: [...]`
	doc := templateDoc{}
	if err := yaml.Unmarshal(data, &doc); err == nil && len(doc.Templates) > 0 {
		return doc.Templates
	}

	// Shape B: top-level map with per-entry structure.
	// The YAML looks like `templates:` followed by flat map entries (type, task, body...).
	// We scan for blocks starting with `type:` and collect them sequentially.
	if entries := collectYamlEntries(data); len(entries) > 0 {
		result := make([]Template, 0, len(entries))
		for _, entry := range entries {
			if t, ok := decodeYamlEntry(entry); ok {
				result = append(result, t)
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// Shape C: bare single template (no wrapping, or only one entry).
	single := Template{}
	if err := yaml.Unmarshal(data, &single); err == nil && strings.TrimSpace(single.Body) != "" {
		single.ID = fallbackTemplateID(path)
	}
	return []Template{single}
}

func decodeJSONTemplates(path string, data []byte) []Template {
	if len(data) == 0 {
		return nil
	}
	doc := templateDoc{}
	if err := json.Unmarshal(data, &doc); err == nil && len(doc.Templates) > 0 {
		return doc.Templates
	}
	single := Template{}
	if err := json.Unmarshal(data, &single); err != nil {
		return nil
	}
	if strings.TrimSpace(single.ID) == "" {
		single.ID = fallbackTemplateID(path)
	}
	return []Template{single}
}

func decodeMarkdownTemplate(path string, data []byte) (Template, bool) {
	// Normalize CRLF -> LF up front so the frontmatter detection below
	// works regardless of how the file was saved. A user editing a prompt
	// in Notepad / VSCode on Windows gets CRLF by default; without this
	// the strict `"---\n"` prefix check missed the marker and the file
	// loaded with no metadata (ID/Type/Task all fell back to defaults).
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	raw := strings.TrimSpace(text)
	if raw == "" {
		return Template{}, false
	}

	t := Template{
		ID:   fallbackTemplateID(path),
		Type: "system",
		Task: "general",
		Body: raw,
	}

	if strings.HasPrefix(raw, "---\n") {
		rest := strings.TrimPrefix(raw, "---\n")
		idx := strings.Index(rest, "\n---\n")
		if idx > 0 {
			header := rest[:idx]
			body := strings.TrimSpace(rest[idx+len("\n---\n"):])
			meta := Template{}
			if err := yaml.Unmarshal([]byte(header), &meta); err == nil {
				if strings.TrimSpace(meta.ID) != "" {
					t.ID = meta.ID
				}
				if strings.TrimSpace(meta.Type) != "" {
					t.Type = meta.Type
				}
				if strings.TrimSpace(meta.Task) != "" {
					t.Task = meta.Task
				}
				if strings.TrimSpace(meta.Language) != "" {
					t.Language = meta.Language
				}
				if strings.TrimSpace(meta.Profile) != "" {
					t.Profile = meta.Profile
				}
				if strings.TrimSpace(meta.Role) != "" {
					t.Role = meta.Role
				}
				if strings.TrimSpace(meta.Description) != "" {
					t.Description = meta.Description
				}
				t.Priority = meta.Priority
			}
			t.Body = body
			return t, true
		}
	}

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parts := strings.Split(name, ".")
	if len(parts) >= 1 && strings.TrimSpace(parts[0]) != "" {
		t.Type = parts[0]
	}
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		t.Task = parts[1]
	}
	if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
		t.Language = parts[2]
	}
	return t, true
}

func fallbackTemplateID(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = strings.ToLower(strings.TrimSpace(base))
	base = strings.ReplaceAll(base, " ", "_")
	if base == "" {
		return "template"
	}
	return base
}

// collectYamlEntries scans YAML data for blocks starting with "    type:".
func collectYamlEntries(data []byte) [][]byte {
	var result [][]byte
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "    type:") || strings.HasPrefix(line, "    - type:") {
			bs := i
			i++
			for i < len(lines) {
				n := lines[i]
				if strings.HasPrefix(n, "    ") && strings.TrimSpace(n) != "" {
					t := strings.TrimLeft(n, " ")
					if strings.HasPrefix(t, "type:") {
						break
					}
				}
				i++
			}
			result = append(result, []byte(strings.Join(lines[bs:i], "\n")))
		} else {
			i++
		}
	}
	return result
}

// decodeYamlEntry parses a block beginning with "    type:" into a Template.
func decodeYamlEntry(block []byte) (Template, bool) {
	var t Template
	lines := strings.Split(strings.ReplaceAll(string(block), "\r\n", "\n"), "\n")
	inBody := false
	bodyMarkerIndent := 0
	bodyLines := []string{}
	for _, line := range lines {
		if inBody {
			if strings.TrimSpace(line) == "" {
				bodyLines = append(bodyLines, line)
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent < bodyMarkerIndent {
				inBody = false
			} else {
				bodyLines = append(bodyLines, line)
				continue
			}
		}
		if strings.HasPrefix(line, "    type:") {
			t.Type = strings.TrimSpace(strings.TrimPrefix(line, "    type:"))
		} else if strings.HasPrefix(line, "    id:") {
			t.ID = strings.TrimSpace(strings.TrimPrefix(line, "    id:"))
		} else if strings.HasPrefix(line, "    task:") {
			t.Task = strings.TrimSpace(strings.TrimPrefix(line, "    task:"))
		} else if strings.HasPrefix(line, "    language:") {
			t.Language = strings.TrimSpace(strings.TrimPrefix(line, "    language:"))
		} else if strings.HasPrefix(line, "    profile:") {
			t.Profile = strings.TrimSpace(strings.TrimPrefix(line, "    profile:"))
		} else if strings.HasPrefix(line, "    role:") {
			t.Role = strings.TrimSpace(strings.TrimPrefix(line, "    role:"))
		} else if strings.HasPrefix(line, "    compose:") {
			t.Compose = strings.TrimSpace(strings.TrimPrefix(line, "    compose:"))
		} else if strings.HasPrefix(line, "    priority:") {
			if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "    priority:"))); err == nil {
				t.Priority = v
			}
		} else if strings.TrimSpace(line) == "body: |" || strings.TrimSpace(line) == "body:|" {
			inBody = true
			bodyMarkerIndent = 6
			bodyLines = bodyLines[:0]
		}
	}
	if len(bodyLines) > 0 {
		t.Body = strings.TrimRight(strings.Join(bodyLines, "\n"), "\n")
	}
	return t, t.Type != "" || t.Body != ""
}
