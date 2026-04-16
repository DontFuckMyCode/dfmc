package commands

// help.go — plain-text help rendering used by CLI `dfmc help`, TUI `/help`,
// and the `--help` short path. The output is deterministic so golden-file
// tests can pin it, and tight enough to fit a standard 80-col terminal
// without wrapping mid-word.

import (
	"fmt"
	"sort"
	"strings"
)

const helpIndent = "    " // 4 spaces — reads cleaner than tabs across terminals.

// RenderHelp produces the catalog view: one section per category, with a
// two-column "name — summary" listing inside each. Subcommands are NOT
// expanded here; use RenderCommandHelp for a single-command deep dive.
//
// The `header` argument lets the caller inject an introductory paragraph
// (e.g. the CLI prepends "DFMC — code intelligence assistant\n"). Pass ""
// to skip.
func (r *Registry) RenderHelp(surface Surface, header string) string {
	var b strings.Builder
	if strings.TrimSpace(header) != "" {
		b.WriteString(strings.TrimRight(header, "\n"))
		b.WriteString("\n\n")
	}
	groups := r.ListByCategory(surface)
	width := r.nameColumnWidth(surface)
	for _, g := range groups {
		fmt.Fprintf(&b, "%s:\n", g.Label)
		for _, cmd := range g.Commands {
			fmt.Fprintf(&b, "%s%-*s  %s\n", helpIndent, width, cmd.Name, cmd.Summary)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderCommandHelp produces the single-command view: usage, description,
// subcommands, aliases, examples. Returns the empty string when the command
// isn't registered so callers can fall back to a suggestion message.
func (r *Registry) RenderCommandHelp(name string) string {
	cmd, ok := r.Lookup(name)
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", cmd.Name, cmd.Summary)
	b.WriteString("\n")
	if usage := strings.TrimSpace(cmd.Usage); usage != "" {
		fmt.Fprintf(&b, "Usage:\n%s%s\n\n", helpIndent, usage)
	}
	if desc := strings.TrimSpace(cmd.Description); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(&b, "Aliases: %s\n\n", strings.Join(cmd.Aliases, ", "))
	}
	if len(cmd.Subcommands) > 0 {
		b.WriteString("Subcommands:\n")
		w := subcommandColumnWidth(cmd.Subcommands)
		for _, s := range cmd.Subcommands {
			fmt.Fprintf(&b, "%s%-*s  %s\n", helpIndent, w, s.Name, s.Summary)
		}
		b.WriteString("\n")
	}
	if len(cmd.Examples) > 0 {
		b.WriteString("Examples:\n")
		for _, ex := range cmd.Examples {
			fmt.Fprintf(&b, "%s%s\n", helpIndent, ex)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Available on: %s\n", cmd.Surfaces)
	return strings.TrimRight(b.String(), "\n")
}

// nameColumnWidth picks a column width that accommodates the longest command
// name for the given surface, clamped to [6, 14] so the summary column still
// has room on an 80-col terminal.
func (r *Registry) nameColumnWidth(surface Surface) int {
	width := 6
	for _, cmd := range r.ForSurface(surface) {
		if n := len(cmd.Name); n > width {
			width = n
		}
	}
	if width > 14 {
		width = 14
	}
	return width
}

func subcommandColumnWidth(subs []Subcommand) int {
	width := 6
	for _, s := range subs {
		if n := len(s.Name); n > width {
			width = n
		}
	}
	if width > 14 {
		width = 14
	}
	return width
}

// Names returns the sorted list of canonical command names registered for
// surface — useful for completion scripts and the web discovery endpoint.
func (r *Registry) Names(surface Surface) []string {
	out := make([]string, 0)
	for _, cmd := range r.ForSurface(surface) {
		out = append(out, cmd.Name)
	}
	sort.Strings(out)
	return out
}
