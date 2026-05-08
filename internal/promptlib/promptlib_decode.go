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
	doc := templateDoc{}
	if err := yaml.Unmarshal(data, &doc); err == nil && len(doc.Templates) > 0 {
		return doc.Templates
	}
	single := Template{}
	if err := yaml.Unmarshal(data, &single); err != nil {
		return nil
	}
	if strings.TrimSpace(single.ID) == "" {
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
