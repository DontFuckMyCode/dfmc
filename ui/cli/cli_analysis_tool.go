package cli

// cli_analysis_tool.go — `dfmc tool` subcommand: list registered tools,
// show a single tool's spec, or run a tool with positional --key value
// flags. Lives next to the analysis dispatcher because it uses the
// same engine surface (eng.Tools / eng.CallTool) and shares the JSON-
// vs-text output convention.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func runTool(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 || args[0] == "list" {
		if eng == nil || eng.Tools == nil {
			fmt.Fprintln(os.Stderr, "tool engine not initialized")
			return 1
		}
		allTools := eng.Tools.ListAll()
		disabled := eng.ListDisabledTools()
		disabledSet := map[string]bool{}
		for _, d := range disabled {
			disabledSet[d] = true
		}
		if jsonMode {
			type toolEntry struct {
				Name      string `json:"name"`
				Disabled  bool   `json:"disabled"`
				Protected bool   `json:"protected"`
			}
			entries := make([]toolEntry, 0, len(allTools))
			for _, t := range allTools {
				entries = append(entries, toolEntry{
					Name:      t,
					Disabled:  disabledSet[t],
					Protected: eng.ToolIsProtected(t),
				})
			}
			mustPrintJSON(map[string]any{"tools": entries})
			return 0
		}
		// Show one line per tool with a short summary pulled from its
		// ToolSpec. Keeps text mode readable without requiring a follow-
		// up `dfmc tool show NAME` just to learn what each verb does.
		var specs map[string]string
		if eng.Tools != nil {
			specs = map[string]string{}
			// Use Specs() which already filters disabled — but we also want
			// specs for disabled tools so we can show their summaries.
			// Access the full registry via Spec(name) for each tool.
			for _, t := range allTools {
				if spec, ok := eng.Tools.Spec(t); ok && spec.Summary != "" {
					specs[t] = spec.Summary
				}
			}
		}
		if specs == nil {
			specs = map[string]string{}
		}
		for _, t := range allTools {
			summary := specs[t]
			suffix := ""
			if disabledSet[t] {
				suffix = " [DISABLED]"
			} else if eng.ToolIsProtected(t) {
				suffix = " [protected]"
			}
			if summary != "" {
				fmt.Printf("%-24s %s%s\n", t, summary, suffix)
			} else {
				fmt.Printf("%s%s\n", t, suffix)
			}
		}
		return 0
	}

	// `dfmc tool show NAME` — print the ToolSpec so operators can see
	// the parameter shape before invoking `dfmc tool run` blind. This
	// fills the gap where previously you had to grep the source to
	// discover what args a tool accepts.
	if args[0] == "show" || args[0] == "describe" || args[0] == "inspect" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc tool show <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		if eng.Tools == nil {
			fmt.Fprintln(os.Stderr, "tools engine not initialized")
			return 1
		}
		spec, ok := eng.Tools.Spec(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown tool: %s\n", name)
			return 1
		}
		if jsonMode {
			mustPrintJSON(spec)
			return 0
		}
		printToolSpec(spec)
		return 0
	}

	// `dfmc tool enable/disable NAME` — toggle tool availability.
	if args[0] == "enable" || args[0] == "disable" {
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: dfmc tool %s <name>\n", args[0])
			return 2
		}
		name := strings.TrimSpace(args[1])
		enabled := args[0] == "enable"
		if err := eng.SetToolEnabled(name, enabled); err != nil {
			if errors.Is(err, tools.ErrToolProtected) {
				fmt.Fprintf(os.Stderr, "error: %q is a protected tool and cannot be disabled\n", name)
				return 1
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"name": name, "enabled": enabled})
			return 0
		}
		if enabled {
			fmt.Printf("tool %q enabled\n", name)
		} else {
			fmt.Printf("tool %q disabled\n", name)
		}
		return 0
	}

	if args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool [list|show <name>|enable <name>|disable <name>|run <name> [--key value ...]]")
		return 2
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool run <name> [--key value ...]")
		return 2
	}
	name := args[1]
	params := map[string]any{}
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		part := rest[i]
		if !strings.HasPrefix(part, "--") {
			continue
		}
		key := strings.TrimPrefix(part, "--")
		val := "true"
		if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
			val = rest[i+1]
			i++
		}
		params[key] = val
	}

	res, err := eng.CallTool(ctx, name, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tool error: %v\n", err)
		return 1
	}
	if jsonMode {
		mustPrintJSON(res)
		return 0
	}
	if strings.TrimSpace(res.Output) != "" {
		fmt.Println(res.Output)
	}
	return 0
}
