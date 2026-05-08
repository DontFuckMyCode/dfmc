package context

// trajectory_helpers.go — small, mostly-stateless utilities used by
// the trajectory rule engine: round-summary formatting, confidence
// calculation, path abbreviation, the shell-command → dedicated-tool
// mapping (preferDedicatedTool), and the string parsers (firstToken,
// firstLine, argAsString) every rule reaches for.

import (
	"fmt"
	"strings"
)

// buildRoundSummary produces a one-line recap of the round's activity.
func buildRoundSummary(fresh []TraceEntry) string {
	if len(fresh) == 0 {
		return ""
	}
	var actions []string
	searched := false
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file":
			actions = append(actions, "edited "+abbrevPath(argAsString(t.Args, "path")))
		case "write_file":
			actions = append(actions, "wrote "+abbrevPath(argAsString(t.Args, "path")))
		case "apply_patch":
			actions = append(actions, "applied patch")
		case "grep_codebase":
			searched = true
		case "read_file":
			actions = append(actions, "read "+abbrevPath(argAsString(t.Args, "path")))
		case "codemap":
			actions = append(actions, "explored codemap")
		case "run_command":
			actions = append(actions, "ran command")
		}
	}
	if len(actions) == 0 {
		if searched {
			return "searched codebase"
		}
		return "no significant file activity"
	}
	if len(actions) > 3 {
		actions = actions[:3]
		actions = append(actions, "...")
	}
	return strings.Join(actions, ", ")
}

// computeConfidence returns 0-1 based on how many tools succeeded and whether
// there are unresolved errors or large search results.
func computeConfidence(fresh []TraceEntry) float64 {
	if len(fresh) == 0 {
		return 0.5
	}
	ok := 0
	for _, t := range fresh {
		if t.Ok {
			ok++
		}
	}
	rate := float64(ok) / float64(len(fresh))
	// Deduct for failures.
	if rate < 1.0 {
		rate -= 0.1
	}
	if rate < 0 {
		rate = 0
	}
	return rate
}

// abbrevPath returns the last path component, trimmed.
func abbrevPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// preferDedicatedTool maps shell commands onto better-suited DFMC tools.
// Returns "" when the command is a legitimate shell-only task.
func preferDedicatedTool(cmd string) string {
	first := strings.ToLower(firstToken(cmd))
	switch first {
	case "cat", "head", "tail", "less", "more":
		return "read_file"
	case "grep", "rg", "ack", "ag":
		return "grep_codebase"
	case "find":
		return "glob"
	case "ls", "dir":
		return "list_dir"
	case "sed", "awk":
		return "edit_file or apply_patch"
	case "echo":
		// Only flag echo when it's redirecting to a file.
		if strings.Contains(cmd, ">") {
			return "write_file"
		}
	case "curl", "wget":
		return "web_fetch"
	}
	return ""
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func argAsString(args map[string]any, key string) string {
	if len(args) == 0 || key == "" {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}
