// Human-readable rendering of a ToolSpec, used by `dfmc tool show`.
// Kept out of cli.go to reduce churn on that sprawling file.

package cli

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func printToolSpec(spec tools.ToolSpec) {
	label := strings.TrimSpace(spec.Title)
	if label == "" {
		label = spec.Name
	}
	fmt.Printf("%s (%s)\n", spec.Name, label)
	if s := strings.TrimSpace(spec.Summary); s != "" {
		fmt.Printf("  summary: %s\n", s)
	}
	if p := strings.TrimSpace(spec.Purpose); p != "" {
		fmt.Printf("  purpose: %s\n", p)
	}
	fmt.Printf("  risk: %s", string(spec.Risk))
	if spec.Idempotent {
		fmt.Printf(" (idempotent)")
	}
	if c := strings.TrimSpace(spec.CostHint); c != "" {
		fmt.Printf(" · cost=%s", c)
	}
	fmt.Println()
	if d := strings.TrimSpace(spec.Deprecated); d != "" {
		fmt.Printf("  deprecated: %s\n", d)
	}
	if len(spec.Tags) > 0 {
		fmt.Printf("  tags: %s\n", strings.Join(spec.Tags, ", "))
	}

	if len(spec.Args) > 0 {
		fmt.Println("  args:")
		for _, a := range spec.Args {
			tag := string(a.Type)
			if a.Required {
				tag += ", required"
			}
			fmt.Printf("    - %s (%s)", a.Name, tag)
			if a.Default != nil {
				fmt.Printf(" default=%v", a.Default)
			}
			fmt.Println()
			if desc := strings.TrimSpace(a.Description); desc != "" {
				fmt.Printf("        %s\n", desc)
			}
			if len(a.Enum) > 0 {
				parts := make([]string, 0, len(a.Enum))
				for _, v := range a.Enum {
					parts = append(parts, fmt.Sprintf("%v", v))
				}
				fmt.Printf("        enum: %s\n", strings.Join(parts, ", "))
			}
		}
	}

	if r := strings.TrimSpace(spec.Returns); r != "" {
		fmt.Printf("  returns: %s\n", r)
	}
	if len(spec.Examples) > 0 {
		fmt.Println("  examples:")
		for _, ex := range spec.Examples {
			fmt.Printf("    %s\n", ex)
		}
	}
	if pr := strings.TrimSpace(spec.Prompt); pr != "" {
		fmt.Println("  prompt:")
		for _, line := range strings.Split(pr, "\n") {
			fmt.Printf("    %s\n", line)
		}
	}
}
