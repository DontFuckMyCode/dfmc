// slash_skills.go — the /skill slash-command family. Lists builtin
// template verbs (/review /explain /refactor /test /doc) alongside
// any YAML files discovered under .dfmc/skills or ~/.dfmc/skills so
// users can see every way the TUI can be driven without leaving the
// chat. Read-only; execution for YAML skills still goes through the
// CLI (dfmc skill run <name>).
//
//   - skillSlash: the /skill dispatcher (list | show | run).
//   - skillEntry + collectSkills: builtin + filesystem discovery with
//     de-dup and builtins-first sort.
//   - formatSkillsList / formatSkillsShow: the two renderers.

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// skillSlash lists and describes skills (built-in template-family verbs
// plus YAML files in .dfmc/skills and ~/.dfmc/skills). Mirrors the
// /api/v1/skills surface so the TUI carries its own view. Supports:
//
//	/skill                → list (alias: /skill list)
//	/skill show <name>    → inline body for YAML skills
//	/skill run <name>     → pointer to the template-slash that runs it
func (m Model) skillSlash(args []string) string {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}
	skills := collectSkills(m.projectRoot())
	switch sub {
	case "", "list", "ls":
		return formatSkillsList(skills)
	case "show", "cat":
		if len(rest) == 0 {
			return "Usage: /skill show <name>"
		}
		name := strings.TrimSpace(rest[0])
		for _, s := range skills {
			if strings.EqualFold(s.Name, name) {
				return formatSkillsShow(s)
			}
		}
		return fmt.Sprintf("No skill with name %q. Run /skill list.", name)
	case "run", "call":
		if len(rest) == 0 {
			return "Usage: /skill run <name> [args...]. For builtin skills prefer the dedicated slash (/review, /explain, /refactor, /test, /doc)."
		}
		name := strings.TrimSpace(rest[0])
		lower := strings.ToLower(name)
		switch lower {
		case "review", "explain", "refactor", "test", "doc":
			return fmt.Sprintf("Run the builtin skill directly: /%s %s", lower, strings.Join(rest[1:], " "))
		}
		return "YAML skill execution is CLI-only for now: dfmc skill run " + name + " " + strings.Join(rest[1:], " ")
	default:
		return "skill: unknown subcommand. Try: list | show <name> | run <name>"
	}
}

// skillEntry captures one skill row — either a builtin template-family
// verb or a YAML file discovered under .dfmc/skills / ~/.dfmc/skills.
type skillEntry struct {
	Name    string
	Source  string // "builtin" / "project" / "global"
	Path    string // "" for builtin
	Summary string
}

func collectSkills(projectRoot string) []skillEntry {
	builtins := []skillEntry{
		{Name: "review", Source: "builtin", Summary: "Review a target for bugs, smells, and hazards."},
		{Name: "explain", Source: "builtin", Summary: "Explain what a target does and why."},
		{Name: "refactor", Source: "builtin", Summary: "Propose a scoped, reversible refactor."},
		{Name: "test", Source: "builtin", Summary: "Draft tests for a target."},
		{Name: "doc", Source: "builtin", Summary: "Draft or update documentation."},
	}
	out := make([]skillEntry, 0, len(builtins)+8)
	out = append(out, builtins...)
	seen := map[string]struct{}{}
	for _, b := range builtins {
		seen[strings.ToLower(b.Name)] = struct{}{}
	}
	roots := []struct {
		path   string
		source string
	}{
		{path: filepath.Join(projectRoot, ".dfmc", "skills"), source: "project"},
		{path: filepath.Join(config.UserConfigDir(), "skills"), source: "global"},
	}
	for _, root := range roots {
		if strings.TrimSpace(root.path) == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root.path, "*.y*ml"))
		for _, p := range matches {
			name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, skillEntry{Name: name, Source: root.source, Path: p})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Builtins first, then by name.
		if (out[i].Source == "builtin") != (out[j].Source == "builtin") {
			return out[i].Source == "builtin"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func formatSkillsList(skills []skillEntry) string {
	if len(skills) == 0 {
		return "No skills found. Place YAML files in .dfmc/skills/ or ~/.dfmc/skills/."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (%d):\n", len(skills))
	for _, s := range skills {
		if s.Summary != "" {
			fmt.Fprintf(&b, "  %-12s [%s]  %s\n", s.Name, s.Source, s.Summary)
		} else {
			fmt.Fprintf(&b, "  %-12s [%s]\n", s.Name, s.Source)
		}
	}
	b.WriteString("Show body: /skill show <name>  ·  Run builtin: /review /explain /refactor /test /doc")
	return strings.TrimRight(b.String(), "\n")
}

func formatSkillsShow(s skillEntry) string {
	if s.Source == "builtin" {
		return fmt.Sprintf("▸ %s (builtin)\n  %s\n  Run it with: /%s <target>", s.Name, s.Summary, s.Name)
	}
	if s.Path == "" {
		return fmt.Sprintf("▸ %s [%s] — no path on disk", s.Name, s.Source)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return fmt.Sprintf("▸ %s [%s]\n  path: %s\n  read error: %v", s.Name, s.Source, s.Path, err)
	}
	return fmt.Sprintf("▸ %s [%s]\n  path: %s\n  body:\n%s",
		s.Name, s.Source, filepath.ToSlash(s.Path), truncateCommandBlock(string(data), 4000))
}
