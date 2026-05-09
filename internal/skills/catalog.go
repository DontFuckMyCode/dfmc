package skills

// catalog.go — public Skill type, discovery + selection surface, and
// the regex state shared with the rendering helpers in
// catalog_render.go (DecorateQuery / StripMarkers / RenderSystemText
// / SystemInstruction / explicitNames / skillForTask). File-format
// decoders for on-disk skills live in catalog_loader.go; the binary's
// ten built-in skills live in catalog_builtin.go.

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"os"
)

type Skill struct {
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description"`
	Path          string        `json:"path,omitempty" yaml:"-"`
	Source        string        `json:"source" yaml:"-"`
	Builtin       bool          `json:"builtin" yaml:"-"`
	Prompt        string        `json:"-" yaml:"prompt"`
	System        string        `json:"-" yaml:"system_prompt"`
	Task          string        `json:"task,omitempty" yaml:"task"`
	Role          string        `json:"role,omitempty" yaml:"role"`
	Profile       string        `json:"profile,omitempty" yaml:"profile"`
	Preferred     []string      `json:"preferred_tools,omitempty" yaml:"preferred_tools"`
	Allowed       []string      `json:"allowed_tools,omitempty" yaml:"allowed_tools"`
	Version       string        `json:"version,omitempty" yaml:"-"`
	Author        string        `json:"author,omitempty" yaml:"-"`
	Tags          []string      `json:"tags,omitempty" yaml:"-"`
	Compatibility string        `json:"compatibility,omitempty" yaml:"-"`
	Triggers      []Trigger     `json:"triggers,omitempty" yaml:"-"`
	Requires      []Requirement `json:"requires,omitempty" yaml:"-"`
}

// Selection is the result of ResolveForQuery. Origin records WHY
// each skill activated ("explicit" / "trigger" / "task" / "required")
// so UI surfaces can render badges without re-running resolution.
type Selection struct {
	Query     string
	Skills    []Skill
	Explicit  bool
	Triggered bool
	Origin    map[string]string
}

var (
	skillMarkerRE     = regexp.MustCompile(`\[\[skill:([A-Za-z0-9._/-]+)\]\]`)
	trailingRequestRE = regexp.MustCompile(`(?is)\n*(user request|request)\s*:\s*$`)
)

// Discover returns every skill visible to this project, in this precedence:
//  1. Builtins (review/explain/refactor/...) — defined in builtinCatalog().
//  2. Project skills under <projectRoot>/.dfmc/skills/.
//  3. Global skills under <userConfigDir>/skills/.
//
// Names collide case-insensitively; the FIRST registration wins. That means
// a project or user skill named "review" / "debug" / "audit" / etc. is
// silently shadowed by the builtin of the same name — pick a different
// name for any custom skill that overlaps. This precedence is intentional:
// the builtin contracts ship with the binary and the agent loop's prompts
// reference them, so letting a user-supplied YAML override the contract
// would silently break the system prompts that depend on the builtin's
// shape. Tests pin this in catalog_test.go.
func Discover(projectRoot string) []Skill {
	out := make([]Skill, 0, 16)
	seen := map[string]struct{}{}

	for _, item := range builtinCatalog() {
		key := strings.ToLower(strings.TrimSpace(item.Name))
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}

	roots := []struct {
		path   string
		source string
	}{}
	if strings.TrimSpace(projectRoot) != "" {
		roots = append(roots, struct {
			path   string
			source string
		}{
			path:   filepath.Join(projectRoot, ".dfmc", "skills"),
			source: "project",
		})
	}
	roots = append(roots, struct {
		path   string
		source string
	}{
		path:   filepath.Join(config.UserConfigDir(), "skills"),
		source: "global",
	})

	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root.path, "*.y*ml"))
		for _, path := range files {
			item := readSkillFile(path, root.source)
			key := strings.ToLower(strings.TrimSpace(item.Name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
		// Also scan for Agent Skills directory bundles: <name>/SKILL.md
		entries, _ := os.ReadDir(root.path)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(root.path, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillPath); err != nil {
				continue
			}
			item := readSkillFile(skillPath, root.source)
			key := strings.ToLower(strings.TrimSpace(item.Name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func Lookup(projectRoot, name string) (Skill, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Skill{}, false
	}
	for _, item := range Discover(projectRoot) {
		if strings.EqualFold(item.Name, name) {
			return item, true
		}
	}
	return Skill{}, false
}

// SkillForName is a thin wrapper around Lookup that returns the Skill
// struct directly. Used by callers that need the full Skill value
// (Preferred/Allowed lists) rather than just existence.
func SkillForName(projectRoot, name string) (Skill, bool) {
	return Lookup(projectRoot, name)
}

// ResolveForQuery picks skills to activate for `query`. Priority:
//
//  1. Explicit [[skill:name]] markers (user wins).
//  2. Trigger match — highest-weighted regex hit ≥ MinTriggerScore.
//  3. Task-hint fallback (skillForTask map).
//
// After the primary set is picked, `requires:` deps are expanded
// depth-first and prepended (transitive prerequisites land before
// their dependants). Selection.Origin maps lower-cased skill name
// to "explicit" / "trigger" / "task" / "required".
func ResolveForQuery(projectRoot, query, detectedTask string) Selection {
	all := Discover(projectRoot)
	byName := make(map[string]Skill, len(all))
	for _, item := range all {
		byName[strings.ToLower(strings.TrimSpace(item.Name))] = item
	}

	cleanQuery := StripMarkers(query)
	origin := map[string]string{}
	selected := make([]Skill, 0, 2)
	selectedSeen := map[string]struct{}{}

	add := func(name, why string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if _, ok := selectedSeen[key]; ok {
			return
		}
		item, ok := byName[key]
		if !ok {
			return
		}
		selectedSeen[key] = struct{}{}
		selected = append(selected, item)
		origin[key] = why
	}

	explicit := explicitNames(query)
	for _, name := range explicit {
		add(name, "explicit")
	}

	triggered := false
	if len(selected) == 0 {
		if name := matchTriggers(all, cleanQuery); name != "" {
			before := len(selected)
			add(name, "trigger")
			if len(selected) > before {
				triggered = true
			}
		}
	}

	if len(selected) == 0 {
		if mapped := skillForTask(detectedTask); mapped != "" {
			add(mapped, "task")
		}
	}

	if len(selected) > 0 {
		deps := expandRequires(selected, byName)
		if len(deps) > 0 {
			merged := make([]Skill, 0, len(deps)+len(selected))
			for _, dep := range deps {
				key := strings.ToLower(strings.TrimSpace(dep.Name))
				if _, ok := origin[key]; !ok {
					origin[key] = "required"
				}
				merged = append(merged, dep)
			}
			merged = append(merged, selected...)
			selected = merged
		}
	}

	return Selection{
		Query:     cleanQuery,
		Triggered: triggered,
		Origin:    origin,
		Skills:    selected,
		Explicit:  len(explicit) > 0,
	}
}

func (s Selection) Primary() (Skill, bool) {
	if len(s.Skills) == 0 {
		return Skill{}, false
	}
	return s.Skills[0], true
}

// DecorateQuery + StripMarkers + RenderSystemText +
// Skill.SystemInstruction + explicitNames + skillForTask live in
// catalog_render.go.
