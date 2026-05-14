package promptlib

// promptlib.go — core types and Library lifecycle. The render path lives in
// promptlib_render.go (replace/append axes, scoring, cache-break splice).
// File loaders live in promptlib_decode.go (YAML, JSON, Markdown frontmatter).
// Task / language inference lives in promptlib_detect.go.
//
// The split keeps construction, embedded-default loading, override loading,
// and template upsert in one short file so the wiring is easy to audit; the
// scoring and decoding logic — both substantial and self-contained — sit in
// their own siblings.

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

//go:embed defaults/*.yaml
var defaultTemplatesFS embed.FS

type Template struct {
	ID          string `json:"id" yaml:"id"`
	Type        string `json:"type" yaml:"type"`
	Task        string `json:"task" yaml:"task"`
	Language    string `json:"language" yaml:"language"`
	Profile     string `json:"profile" yaml:"profile"`
	Role        string `json:"role" yaml:"role"`
	Compose     string `json:"compose,omitempty" yaml:"compose"`
	Priority    int    `json:"priority" yaml:"priority"`
	Description string `json:"description" yaml:"description"`
	Body        string `json:"body" yaml:"body"`
}

type RenderRequest struct {
	Type     string
	Task     string
	Language string
	Profile  string
	Role     string
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
	var loadErrs []error
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
		} else if !errors.Is(err, os.ErrNotExist) {
			loadErrs = append(loadErrs, fmt.Errorf("%s: %w", abs, err))
		}

		l.mu.Lock()
		l.loadedRoots[abs] = struct{}{}
		l.mu.Unlock()
	}
	return errors.Join(loadErrs...)
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

func (l *Library) Get(id string) (Template, bool) {
	id = strings.TrimSpace(id)
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, t := range l.templates {
		if strings.EqualFold(strings.TrimSpace(t.ID), id) {
			return t, true
		}
	}
	return Template{}, false
}

// RawEmbedFS exposes the embedded defaults FS for diff comparison.
func (l *Library) RawEmbedFS() embed.FS { return defaultTemplatesFS }

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
