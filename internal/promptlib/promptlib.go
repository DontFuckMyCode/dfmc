package promptlib

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
	"gopkg.in/yaml.v3"
)

//go:embed defaults/*.yaml
var defaultTemplatesFS embed.FS

type Template struct {
	ID          string `json:"id" yaml:"id"`
	Type        string `json:"type" yaml:"type"`
	Task        string `json:"task" yaml:"task"`
	Language    string `json:"language" yaml:"language"`
	Profile     string `json:"profile" yaml:"profile"`
	Priority    int    `json:"priority" yaml:"priority"`
	Description string `json:"description" yaml:"description"`
	Body        string `json:"body" yaml:"body"`
}

type RenderRequest struct {
	Type     string
	Task     string
	Language string
	Profile  string
	Vars     map[string]string
}

type Library struct {
	mu          sync.RWMutex
	templates   []Template
	loadedRoots map[string]struct{}
}

func New() *Library {
	l := &Library{
		templates:   make([]Template, 0, 16),
		loadedRoots: map[string]struct{}{},
	}
	l.loadEmbeddedDefaults()
	return l
}

func (l *Library) loadEmbeddedDefaults() {
	_ = fs.WalkDir(defaultTemplatesFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !isPromptFile(path) {
			return nil
		}
		data, err := defaultTemplatesFS.ReadFile(path)
		if err != nil {
			return nil
		}
		templates := decodePromptFile(path, data)
		for _, t := range templates {
			l.upsert(t)
		}
		return nil
	})
}

func (l *Library) LoadOverrides(projectRoot string) error {
	roots := []string{
		filepath.Join(config.UserConfigDir(), "prompts"),
	}
	if strings.TrimSpace(projectRoot) != "" {
		roots = append(roots, filepath.Join(projectRoot, ".dfmc", "prompts"))
	}

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}

		l.mu.RLock()
		_, done := l.loadedRoots[abs]
		l.mu.RUnlock()
		if done {
			continue
		}

		entries, err := loadPromptDir(abs)
		if err == nil {
			for _, t := range entries {
				l.upsert(t)
			}
		}

		l.mu.Lock()
		l.loadedRoots[abs] = struct{}{}
		l.mu.Unlock()
	}
	return nil
}

func (l *Library) List() []Template {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Template, len(l.templates))
	copy(out, l.templates)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type == out[j].Type {
			if out[i].Task == out[j].Task {
				return out[i].ID < out[j].ID
			}
			return out[i].Task < out[j].Task
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func (l *Library) Render(req RenderRequest) string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	best := Template{}
	bestScore := -1_000_000
	for _, t := range l.templates {
		score, ok := templateScore(t, req)
		if !ok {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = t
		}
	}

	if bestScore < 0 {
		return defaultFallbackPrompt(req)
	}
	return renderBody(best.Body, req.Vars)
}

func (l *Library) upsert(t Template) {
	t = normalizeTemplate(t)
	if strings.TrimSpace(t.Body) == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if t.ID != "" {
		for i := range l.templates {
			if strings.EqualFold(strings.TrimSpace(l.templates[i].ID), t.ID) {
				l.templates[i] = t
				return
			}
		}
	}
	l.templates = append(l.templates, t)
}

func normalizeTemplate(t Template) Template {
	t.ID = strings.TrimSpace(t.ID)
	t.Type = normalizeKey(t.Type)
	if t.Type == "" {
		t.Type = "system"
	}
	t.Task = normalizeKey(t.Task)
	if t.Task == "" {
		t.Task = "general"
	}
	t.Language = normalizeKey(t.Language)
	t.Profile = normalizeKey(t.Profile)
	t.Description = strings.TrimSpace(t.Description)
	t.Body = strings.TrimSpace(t.Body)
	return t
}

func normalizeKey(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func templateScore(t Template, req RenderRequest) (int, bool) {
	typ := normalizeKey(req.Type)
	task := normalizeKey(req.Task)
	lang := normalizeKey(req.Language)
	profile := normalizeKey(req.Profile)

	if typ != "" && t.Type != typ {
		return 0, false
	}

	score := t.Priority

	if task != "" {
		switch {
		case t.Task == task:
			score += 100
		case t.Task == "general":
			score += 25
		default:
			return 0, false
		}
	}

	if lang != "" {
		switch {
		case t.Language == "":
			score += 5
		case t.Language == lang:
			score += 30
		default:
			return 0, false
		}
	}

	if profile != "" {
		switch {
		case t.Profile == "":
			score += 2
		case t.Profile == profile:
			score += 20
		default:
			return 0, false
		}
	}

	return score, true
}

func defaultFallbackPrompt(req RenderRequest) string {
	projectRoot := strings.TrimSpace(req.Vars["project_root"])
	task := strings.TrimSpace(req.Task)
	if task == "" {
		task = "general"
	}
	lang := strings.TrimSpace(req.Language)
	if lang == "" {
		lang = "generic"
	}
	if projectRoot == "" {
		return fmt.Sprintf("You are DFMC. Task=%s Language=%s. Be correct, concise, and safe.", task, lang)
	}
	return fmt.Sprintf("You are DFMC for project %s. Task=%s Language=%s. Be correct, concise, and safe.", projectRoot, task, lang)
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func renderBody(body string, vars map[string]string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	if vars == nil {
		return body
	}
	return placeholderRe.ReplaceAllStringFunc(body, func(match string) string {
		parts := placeholderRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return ""
		}
		return strings.TrimSpace(vars[parts[1]])
	})
}

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
	text := string(data)
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

func DetectTask(query string) string {
	q := strings.ToLower(" " + strings.TrimSpace(query) + " ")
	qFolded := " " + foldSearchText(strings.TrimSpace(query)) + " "
	has := func(words ...string) bool {
		for _, w := range words {
			key := strings.ToLower(strings.TrimSpace(w))
			if key == "" {
				continue
			}
			if strings.Contains(q, " "+key+" ") || strings.Contains(qFolded, " "+foldSearchText(key)+" ") {
				return true
			}
		}
		return false
	}
	switch {
	case has("security", "audit", "vuln", "vulnerability", "xss", "sqli", "guvenlik", "zaafiyet"):
		return "security"
	case has("review", "code review", "incele", "inceleme"):
		return "review"
	case has("refactor", "cleanup", "restructure"):
		return "refactor"
	case has("test", "tests", "unit test", "integration test"):
		return "test"
	case has("doc", "docs", "documentation", "belgele"):
		return "doc"
	case has("plan", "planning", "roadmap", "phase", "sprint", "adim"):
		return "planning"
	case has("bug", "fix", "error", "exception", "panic", "hata", "debug"):
		return "debug"
	default:
		return "general"
	}
}

func foldSearchText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 0x131, 0x130:
			r = 'i'
		case 0x11f, 0x11e:
			r = 'g'
		case 0x15f, 0x15e:
			r = 's'
		case 0xfc, 0xdc:
			r = 'u'
		case 0xf6, 0xd6:
			r = 'o'
		case 0xe7, 0xc7:
			r = 'c'
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func InferLanguage(query string, chunks []types.ContextChunk) string {
	q := strings.ToLower(query)
	explicit := map[string]string{
		"golang":     "go",
		"go ":        "go",
		"typescript": "typescript",
		"javascript": "javascript",
		" python":    "python",
		" rust":      "rust",
		" java":      "java",
		" c#":        "csharp",
		" csharp":    "csharp",
		" php":       "php",
		" kotlin":    "kotlin",
		" swift":     "swift",
	}
	for needle, lang := range explicit {
		if strings.Contains(" "+q+" ", needle) {
			return lang
		}
	}

	counts := map[string]int{}
	for _, ch := range chunks {
		lang := normalizeKey(ch.Language)
		if lang == "" {
			lang = languageFromPath(ch.Path)
		}
		if lang == "" {
			continue
		}
		counts[lang]++
	}
	bestLang := ""
	bestCount := 0
	for lang, n := range counts {
		if n > bestCount {
			bestLang = lang
			bestCount = n
		}
	}
	if bestLang != "" {
		return bestLang
	}
	return "generic"
}

func languageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	default:
		return ""
	}
}
