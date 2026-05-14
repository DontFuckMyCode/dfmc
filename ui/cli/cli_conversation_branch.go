package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runConversationBranch(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch [list|create|switch|compare]")
		return 2
	}
	action := strings.ToLower(strings.TrimSpace(args[1]))
	if eng.ConversationActive() == nil {
		_ = eng.ConversationStart()
	}
	switch action {
	case "list":
		items := eng.ConversationBranchList()
		if jsonMode {
			mustPrintJSON(map[string]any{"branches": items})
		} else {
			for _, name := range items {
				fmt.Printf("- %s\n", name)
			}
		}
		return 0
	case "create":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch create <name>")
			return 2
		}
		name := strings.TrimSpace(args[2])
		if err := eng.ConversationBranchCreate(name); err != nil {
			fmt.Fprintf(os.Stderr, "branch create error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"status": "ok", "branch": name})
		} else {
			fmt.Printf("branch created: %s\n", name)
		}
		return 0
	case "switch":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch switch <name>")
			return 2
		}
		name := strings.TrimSpace(args[2])
		if err := eng.ConversationBranchSwitch(name); err != nil {
			fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"status": "ok", "branch": name})
		} else {
			fmt.Printf("branch switched: %s\n", name)
		}
		return 0
	case "compare":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch compare <branch-a> <branch-b>")
			return 2
		}
		comp, err := eng.ConversationBranchCompare(strings.TrimSpace(args[2]), strings.TrimSpace(args[3]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "branch compare error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(comp)
		} else {
			fmt.Printf("%s vs %s: shared=%d only_%s=%d only_%s=%d\n",
				comp.BranchA, comp.BranchB, comp.SharedPrefixN, comp.BranchA, comp.OnlyA, comp.BranchB, comp.OnlyB)
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch [list|create|switch|compare]")
		return 2
	}
}
