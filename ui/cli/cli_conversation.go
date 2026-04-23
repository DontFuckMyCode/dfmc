// Conversation CLI: `dfmc conversation [list|active|new|save|undo|load|
// search|branch]` manages persisted conversation state. Extracted from
// cli_analysis.go — no overlap with analyze/map/tool beyond the CLI
// surface.

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runConversation(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items, err := eng.ConversationList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation list error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(items)
			return 0
		}
		for _, item := range items {
			fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
		}
		return 0

	case "active":
		active := eng.ConversationActive()
		if active == nil {
			if jsonMode {
				mustPrintJSON(map[string]any{"active": nil})
				return 0
			}
			fmt.Println("No active conversation.")
			return 0
		}
		payload := map[string]any{
			"id":         active.ID,
			"provider":   active.Provider,
			"model":      active.Model,
			"started_at": active.StartedAt,
			"branch":     active.Branch,
			"branches":   eng.ConversationBranchList(),
			"messages":   len(active.Messages()),
		}
		if jsonMode {
			mustPrintJSON(payload)
			return 0
		}
		fmt.Printf("ID:       %s\n", active.ID)
		fmt.Printf("Provider: %s\n", active.Provider)
		fmt.Printf("Model:    %s\n", active.Model)
		fmt.Printf("Branch:   %s\n", active.Branch)
		fmt.Printf("Messages: %d\n", len(active.Messages()))
		return 0

	case "new", "clear":
		c := eng.ConversationStart()
		if c == nil {
			fmt.Fprintln(os.Stderr, "failed to start a new conversation")
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status": "ok",
				"id":     c.ID,
				"branch": c.Branch,
			})
		} else {
			fmt.Printf("Started new conversation: %s\n", c.ID)
		}
		return 0

	case "save":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "conversation save error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"status": "ok"})
		} else {
			fmt.Println("conversation saved")
		}
		return 0

	case "undo":
		n, err := eng.ConversationUndoLast()
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation undo error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"status": "ok", "removed": n})
		} else {
			fmt.Printf("undone messages: %d\n", n)
		}
		return 0

	case "load":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation load <id>")
			return 2
		}
		c, err := eng.ConversationLoad(strings.TrimSpace(args[1]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation load error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"id":       c.ID,
				"branch":   c.Branch,
				"messages": len(c.Messages()),
			})
		} else {
			fmt.Printf("Loaded %s (%d messages)\n", c.ID, len(c.Messages()))
		}
		return 0

	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation search <query>")
			return 2
		}
		query := strings.Join(args[1:], " ")
		items, err := eng.ConversationSearch(query, 20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation search error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(items)
			return 0
		}
		for _, item := range items {
			fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
		}
		return 0

	case "branch":
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

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc conversation [list|search <query>|active|new|clear|save|undo|load <id>|branch ...]")
		return 2
	}
}
