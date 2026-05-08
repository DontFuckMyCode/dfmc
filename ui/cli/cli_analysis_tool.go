package cli

// cli_analysis_tool.go — `dfmc tool` subcommand: list registered tools,
// show a single tool's spec, or run a tool with positional --key value
// flags. Lives next to the analysis dispatcher because it uses the
// same engine surface (eng.Tools / eng.CallTool) and shares the JSON-
// vs-text output convention.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runTool(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 || args[0] == "list" {
		tools := eng.ListTools()
		if jsonMode {
			mustPrintJSON(map[string]any{"tools": tools})
			return 0
		}
		// Show one line per tool with a short summary pulled from its
		// ToolSpec. Keeps text mode readable without requiring a follow-
		// up `dfmc tool show NAME` just to learn what each verb does.
		var specs map[string]string
		if eng.Tools != nil {
			specs = map[string]string{}
			for _, s := range eng.Tools.Specs() {
				specs[s.Name] = strings.TrimSpace(s.Summary)
			}
		}
		for _, t := range tools {
			summary := ""
			if specs != nil {
				summary = specs[t]
			}
			if summary != "" {
				fmt.Printf("%-18s %s\n", t, summary)
			} else {
				fmt.Println(t)
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

	if args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool [list|show <name>|run <name> [--key value ...]]")
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
