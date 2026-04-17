// Human-readable tool spec rendering for the TUI. Mirrors the
// `dfmc tool show` CLI output so operators see the same shape
// regardless of surface. Pure formatting — no side effects on Model.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

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
