// slash_skills.go — the /skill slash-command family. Uses the shared
// skills catalog (internal/skills) so builtins, YAML skills, and Agent
// Skills directory bundles are all surfaced consistently.
//
//   - skillSlash: the /skill dispatcher (list | show | run).
//   - collectSkills: wraps skills.Discover into skillEntry rows.
//   - formatSkillsList / formatSkillsShow: the two renderers.

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/skills"
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

// skillEntry captures one skill row — from the shared skills catalog.
type skillEntry struct {
	Name      string
	Source    string // "builtin" / "project" / "global"
	Path      string // "" for builtin
	Summary   string
	Preferred []string
	Allowed   []string
	Body      string // system prompt / markdown body
}

func collectSkills(projectRoot string) []skillEntry {
	raw := skills.Discover(projectRoot)
	out := make([]skillEntry, 0, len(raw))
	for _, s := range raw {
		entry := skillEntry{
			Name:      s.Name,
			Source:    s.Source,
			Path:      s.Path,
			Summary:   s.Description,
			Preferred: s.Preferred,
			Allowed:   s.Allowed,
			Body:      s.SystemInstruction(),
		}
		if entry.Body == "" {
			entry.Body = s.System
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Source == "builtin") != (out[j].Source == "builtin") {
			return out[i].Source == "builtin"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func formatSkillsList(skills []skillEntry) string {
	if len(skills) == 0 {
		return "No skills found. Run /skill list to refresh."
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
	b.WriteString("Show: /skill show <name>  ·  Run builtin: /review /explain /refactor /test /doc /audit /onboard")
	return strings.TrimRight(b.String(), "\n")
}

func formatSkillsShow(s skillEntry) string {
	if s.Source == "builtin" {
		extra := ""
		if len(s.Preferred) > 0 || len(s.Allowed) > 0 {
			var b strings.Builder
			if len(s.Preferred) > 0 {
				fmt.Fprintf(&b, "  prefer: %s\n", strings.Join(s.Preferred, ", "))
			}
			if len(s.Allowed) > 0 {
				fmt.Fprintf(&b, "  allow:  %s\n", strings.Join(s.Allowed, ", "))
			}
			extra = strings.TrimRight(b.String(), "\n")
		}
		header := fmt.Sprintf("▸ %s (builtin)\n  %s\n  Run it with: /%s <target>", s.Name, s.Summary, s.Name)
		if extra != "" {
			return header + "\n" + extra
		}
		return header
	}
	if s.Path == "" {
		return fmt.Sprintf("▸ %s [%s] — no path on disk", s.Name, s.Source)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return fmt.Sprintf("▸ %s [%s]\n  path: %s\n  read error: %v", s.Name, s.Source, s.Path, err)
	}
	extra := ""
	if len(s.Preferred) > 0 || len(s.Allowed) > 0 {
		var b strings.Builder
		if len(s.Preferred) > 0 {
			fmt.Fprintf(&b, "  prefer: %s\n", strings.Join(s.Preferred, ", "))
		}
		if len(s.Allowed) > 0 {
			fmt.Fprintf(&b, "  allow:  %s\n", strings.Join(s.Allowed, ", "))
		}
		extra = strings.TrimRight(b.String(), "\n")
	}
	body := truncateCommandBlock(string(data), 4000)
	if extra != "" {
		return fmt.Sprintf("▸ %s [%s]\n  path: %s\n%s\n  body:\n%s",
			s.Name, s.Source, filepath.ToSlash(s.Path), extra, body)
	}
	return fmt.Sprintf("▸ %s [%s]\n  path: %s\n  body:\n%s",
		s.Name, s.Source, filepath.ToSlash(s.Path), body)
}
