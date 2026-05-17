package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/taskview"
)

func handleSlashTUIParity(eng *engine.Engine, cmd string, args []string) (exit bool, handled bool) {
	switch cmd {
	case "/providers":
		st := eng.Status()
		fmt.Printf("provider: %s (model=%s)\n", st.Provider, st.Model)
		fmt.Println("(run 'dfmc setup' or edit ~/.config/dfmc/config.toml to add more)")
	case "/models":
		st := eng.Status()
		fmt.Printf("current provider: %s\n", st.Provider)
		fmt.Printf("current model:    %s\n", st.Model)
	case "/redo":
		fmt.Println("redo is not available for conversation history")
	case "/quit", "/exit":
		fmt.Println("goodbye!")
		return true, true
	case "/hooks":
		fmt.Println("lifecycle hooks:")
		fmt.Println("  pre_tool    - before each tool call")
		fmt.Println("  post_tool   - after each tool call")
		fmt.Println("  user_prompt_submit - before sending to model")
		fmt.Println("(configure in ~/.config/dfmc/config.toml -> hooks block)")
	case "/approve":
		fmt.Println("(run 'dfmc setup' for approval gate configuration)")
		fmt.Println("tools that prompt for approval: see config.toml -> approval_rules")
	case "/reload":
		fmt.Println("(config reload requires a restart; run 'dfmc' again to pick up changes)")
		st := eng.Status()
		fmt.Printf("current config: provider=%s model=%s project=%s\n", st.Provider, st.Model, st.ProjectRoot)
	case "/log":
		handleSlashLog(eng)
	case "/file":
		fmt.Println("(file picker is TUI-only - use /ls and /read to browse in CLI)")
		fmt.Println("run 'dfmc tui' for the visual file picker")
	case "/coach":
		fmt.Println("(coach notes are TUI-only - run 'dfmc tui' for background coaching)")
	case "/hints":
		fmt.Println("(trajectory hints are TUI-only - run 'dfmc tui' for between-round hints)")
	case "/workflow":
		handleSlashWorkflow(eng)
	case "/todos":
		fmt.Println("(todo list is a TUI feature - tracked in-memory during the session)")
		fmt.Println("run 'dfmc tui' for the full todo tracking panel")
	case "/tasks":
		handleSlashTasks(eng, args)
	case "/subagents":
		fmt.Println("(subagent fan-out is TUI-only - run 'dfmc tui' for delegation view)")
	case "/toolstatus":
		fmt.Println("(tool call history is TUI-only - run 'dfmc tui' for the toolstatus panel)")
		fmt.Printf("available tools: %d\n", len(eng.ListTools()))
	case "/shortcuts", "/keys":
		printCLIShellShortcuts()
	case "/queue":
		fmt.Println("(prompt queue is TUI-only - /btw <note> queues a single note for the next step)")
		fmt.Println("run 'dfmc tui' for the full queue inspector")
	case "/export":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "export error: %v\n", err)
		} else {
			fmt.Println("conversation saved to disk (see ~/.dfmc/conversations/)")
		}
	case "/pin", "/unpin":
		fmt.Println("(pinning is TUI-only - run 'dfmc tui' to pin assistant turns)")
	case "/fork":
		fmt.Println("(branching is TUI-only - use /branch in CLI for a simpler branch flow)")
		fmt.Println("run 'dfmc tui' for the visual branch picker")
	case "/copy":
		fmt.Println("(clipboard copy is TUI-only - pipe output to clipboard manually)")
	case "/intent":
		sub := strings.TrimSpace(strings.Join(args, " "))
		if sub == "show" {
			fmt.Println("intent rewriting is TUI-only - run 'dfmc tui' for the intent inspector")
		} else {
			fmt.Println("(intent toggle is TUI-only - run 'dfmc tui' for between-round intent hints)")
		}
	case "/mouse":
		fmt.Println("(mouse capture toggle is TUI-only - run 'dfmc tui')")
	case "/select":
		fmt.Println("(selection mode is TUI-only - run 'dfmc tui')")
	case "/code":
		fmt.Println("(plan mode toggle is TUI-only - run 'dfmc tui' for plan mode)")
	default:
		return false, false
	}
	return false, true
}

func handleSlashTasks(eng *engine.Engine, args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	if sub == "" || sub == "open" || sub == "panel" || sub == "close" || sub == "hide" {
		fmt.Println("(task store panel is TUI-only - run 'dfmc tui' for the task panel)")
		fmt.Println("CLI supports: /tasks list | tree | roots | show <id> | clear")
		return
	}
	if eng == nil || eng.Tools == nil {
		fmt.Println("Engine unavailable.")
		return
	}
	store := eng.Tools.TaskStore()
	if store == nil {
		fmt.Println("Task store not initialized.")
		return
	}
	switch sub {
	case "list":
		fmt.Println(taskview.List(store))
	case "tree":
		fmt.Println(taskview.Tree(store, ""))
	case "roots":
		fmt.Println(taskview.Roots(store))
	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: /tasks show <id>")
			return
		}
		fmt.Println(taskview.Detail(store, strings.TrimSpace(args[1])))
	case "clear", "reset":
		fmt.Println(taskview.ClearNonDrive(store))
	default:
		fmt.Println(taskview.UnknownSubcommandHelp)
	}
}

func handleSlashLog(eng *engine.Engine) {
	active := eng.ConversationActive()
	if active == nil {
		fmt.Println("no active conversation")
		return
	}
	msgs := active.Messages()
	recent := msgs
	if len(msgs) > 10 {
		recent = msgs[len(msgs)-10:]
	}
	fmt.Printf("recent messages (%d of %d):\n", len(recent), len(msgs))
	for _, m := range recent {
		preview := m.Content
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		fmt.Printf("  [%s]: %s\n", m.Role, preview)
	}
}

func handleSlashWorkflow(eng *engine.Engine) {
	fmt.Println("workflow status:")
	active := eng.ConversationActive()
	msgN := 0
	if active != nil {
		msgN = len(active.Messages())
	}
	fmt.Printf("  messages in conversation: %d\n", msgN)
	if eng.HasParkedAgent() {
		fmt.Println("  agent loop: parked (use /continue to resume)")
	}
	st := eng.Status()
	fmt.Printf("  provider=%s model=%s\n", st.Provider, st.Model)
}

func printCLIShellShortcuts() {
	fmt.Println("DFMC CLI shortcuts (shell-level):")
	fmt.Println("  Ctrl+D          end input / exit")
	fmt.Println("  Ctrl+C          cancel current turn")
	fmt.Println("  Ctrl+L          clear screen (shell)")
	fmt.Println("  Tab             completions (if enabled)")
	fmt.Println("TUI shortcuts (run 'dfmc tui', press Alt+h):")
	fmt.Println("  Alt+h           open shortcuts cheat sheet")
	fmt.Println("  Ctrl+shift+t   toggle tool status panel")
	fmt.Println("  Alt+d           toggle drive panel")
	fmt.Println("  Ctrl+C          cancel agent loop")
}
