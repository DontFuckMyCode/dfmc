// Human-readable tool spec rendering for the TUI. Mirrors the
// `dfmc tool show` CLI output so operators see the same shape
// regardless of surface. Pure formatting — no side effects on Model.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// describeToolsList renders the registered tool surface with a one-line
// summary per tool so users can triage `/tools` output without having
// to run `/tool show NAME` for every entry. Tools with no Specer fall
// back to just the name; the Specer synthesizer in internal/tools
// already populates Summary from Description() so most tools render
// with a useful line.
func (m Model) describeToolsList(names []string) string {
	if len(names) == 0 {
		return "No tools registered."
	}
	var b strings.Builder
	b.WriteString("Tools (")
	fmt.Fprintf(&b, "%d)\n", len(names))
	var specs map[string]string
	if m.eng != nil && m.eng.Tools != nil {
		specs = map[string]string{}
		for _, spec := range m.eng.Tools.Specs() {
			specs[spec.Name] = strings.TrimSpace(spec.Summary)
		}
	}
	for _, name := range names {
		summary := ""
		if specs != nil {
			summary = specs[name]
		}
		if summary != "" {
			fmt.Fprintf(&b, "  %-18s %s\n", name, summary)
		} else {
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}
	b.WriteString("\n/tool show NAME for parameter shape · F6 for the Tools panel · /tool NAME for one-shot execution.")
	return b.String()
}

// describeToolSpec returns a multi-line string describing the named
// tool. Returns an error-ish message (not an actual error) when the
// tool is unknown or the engine has no tool registry — the caller
// injects the result verbatim into the transcript as a system message.
func (m Model) describeToolSpec(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Usage: /tool show NAME"
	}
	if m.eng == nil || m.eng.Tools == nil {
		return "Tool registry not initialized."
	}
	spec, ok := m.eng.Tools.Spec(name)
	if !ok {
		return fmt.Sprintf("Unknown tool: %s", name)
	}
	return formatToolSpec(spec)
}

// highlightToolSpecLines wraps formatToolSpec output for the Tools panel:
// it splits on newlines, applies subtle styling to label prefixes, accent
// to risk/cost/tags lines, and width-truncates each row so the right
// column of the panel doesn't blow up. Returns a slice ready to splice
// into the panel's detailLines.
func highlightToolSpecLines(text string, width int) []string {
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		styled := line
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "summary:"),
			strings.HasPrefix(trimmed, "purpose:"),
			strings.HasPrefix(trimmed, "returns:"):
			styled = subtleStyle.Render(line)
		case strings.HasPrefix(trimmed, "risk:"):
			styled = warnStyle.Render(line)
		case strings.HasPrefix(trimmed, "tags:"),
			strings.HasPrefix(trimmed, "deprecated:"):
			styled = accentStyle.Render(line)
		case strings.HasPrefix(trimmed, "args:"),
			strings.HasPrefix(trimmed, "examples:"):
			styled = sectionTitleStyle.Render(line)
		case strings.HasPrefix(trimmed, "- "):
			styled = boldStyle.Render(line)
		}
		// Truncate after styling so we keep ANSI codes intact for the
		// short rows; only over-long rows lose their colour, which is
		// acceptable since they were going to wrap anyway.
		if width > 0 && len([]rune(line)) > width {
			runes := []rune(line)
			cut := max(width-12, 0)
			styled = string(runes[:cut]) + " ... [trimmed]"
		}
		out = append(out, styled)
	}
	return out
}

func formatToolSpec(spec tools.ToolSpec) string {
	var b strings.Builder

	label := strings.TrimSpace(spec.Title)
	if label == "" {
		label = spec.Name
	}
	fmt.Fprintf(&b, "%s (%s)\n", spec.Name, label)

	if s := strings.TrimSpace(spec.Summary); s != "" {
		fmt.Fprintf(&b, "  summary: %s\n", s)
	}
	if p := strings.TrimSpace(spec.Purpose); p != "" {
		fmt.Fprintf(&b, "  purpose: %s\n", p)
	}

	riskLine := fmt.Sprintf("  risk: %s", string(spec.Risk))
	if spec.Idempotent {
		riskLine += " (idempotent)"
	}
	if c := strings.TrimSpace(spec.CostHint); c != "" {
		riskLine += " · cost=" + c
	}
	b.WriteString(riskLine + "\n")

	if d := strings.TrimSpace(spec.Deprecated); d != "" {
		fmt.Fprintf(&b, "  deprecated: %s\n", d)
	}
	if len(spec.Tags) > 0 {
		fmt.Fprintf(&b, "  tags: %s\n", strings.Join(spec.Tags, ", "))
	}

	if len(spec.Args) > 0 {
		b.WriteString("  args:\n")
		for _, a := range spec.Args {
			tag := string(a.Type)
			if a.Required {
				tag += ", required"
			}
			fmt.Fprintf(&b, "    - %s (%s)", a.Name, tag)
			if a.Default != nil {
				fmt.Fprintf(&b, " default=%v", a.Default)
			}
			b.WriteString("\n")
			if desc := strings.TrimSpace(a.Description); desc != "" {
				fmt.Fprintf(&b, "        %s\n", desc)
			}
			if len(a.Enum) > 0 {
				parts := make([]string, 0, len(a.Enum))
				for _, v := range a.Enum {
					parts = append(parts, fmt.Sprintf("%v", v))
				}
				fmt.Fprintf(&b, "        enum: %s\n", strings.Join(parts, ", "))
			}
		}
	}

	if r := strings.TrimSpace(spec.Returns); r != "" {
		fmt.Fprintf(&b, "  returns: %s\n", r)
	}
	if len(spec.Examples) > 0 {
		b.WriteString("  examples:\n")
		for _, ex := range spec.Examples {
			fmt.Fprintf(&b, "    %s\n", ex)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
