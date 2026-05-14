// Chat slash-command dispatcher. Extracted from cli_ask_chat.go so
// the interactive loop entry points stay terse. Owns the slash command
// switch table; the heavyweight per-command handlers (/branch, /context,
// /apply) plus the /cost helpers live in cli_chat_slash_handlers.go.

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runChatSlash(ctx context.Context, eng *engine.Engine, line string) (exit bool, handled bool) {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) == 0 {
		return false, false
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		printChatHelp()
		return false, true

	case "/clear":
		handleSlashClear(eng)
		return false, true

	case "/save":
		handleSlashSave(eng)
		return false, true

	case "/load":
		handleSlashLoad(eng, args)
		return false, true

	case "/history":
		handleSlashHistory(eng, args)
		return false, true

	case "/provider":
		handleSlashProvider(eng, args)
		return false, true

	case "/model":
		handleSlashModel(eng, args)
		return false, true

	case "/branch":
		handleSlashBranch(eng, args)
		return false, true

	case "/tools":
		handleSlashTools(eng)
		return false, true

	case "/skills":
		handleSlashSkills(eng)
		return false, true

	case "/context":
		handleSlashContext(eng, args)
		return false, true

	case "/cost":
		handleSlashCost(eng)
		return false, true

	case "/diff":
		handleSlashDiff(eng)
		return false, true

	case "/undo":
		handleSlashUndo(eng)
		return false, true

	case "/apply":
		handleSlashApply(eng, args)
		return false, true

	case "/cancel", "/stop":
		handleSlashCancel(eng)
		return false, true

	case "/retry":
		handleSlashRetry(ctx, eng)
		return false, true

	case "/continue":
		handleSlashContinue(ctx, eng, args)
		return false, true

	case "/agents":
		handleSlashAgents(eng)
		return false, true

	case "/stats":
		handleSlashStats(eng)
		return false, true

	case "/version":
		fmt.Printf("dfmc version=%s\n", eng.Version)
		return false, true

	case "/drive":
		handleSlashDrive(eng)
		return false, true

	case "/plan":
		fmt.Println("plan mode is not available in CLI chat; use the TUI (/tui) for plan mode")
		return false, true

	case "/compact":
		fmt.Println("(CLI does not compact the transcript; use /clear instead)")
		return false, true

	case "/btw":
		handleSlashBtw(eng, args)
		return false, true

	case "/split":
		handleSlashSplit(args)
		return false, true

	case "/doctor":
		handleSlashDoctor(eng)
		return false, true

	// ── Agent/template commands (ask, review, explain, …) ─────────

	case "/ask":
		handleSlashAsk(ctx, eng, args)
		return false, true

	case "/review":
		return runAgentTemplate(ctx, eng, args, "review")
	case "/explain":
		return runAgentTemplate(ctx, eng, args, "explain")
	case "/refactor":
		return runAgentTemplate(ctx, eng, args, "refactor")
	case "/test":
		return runAgentTemplate(ctx, eng, args, "test")
	case "/doc":
		return runAgentTemplate(ctx, eng, args, "doc")
	case "/analyze":
		return runAgentTemplate(ctx, eng, args, "analyze")
	case "/scan":
		return runAgentTemplate(ctx, eng, args, "scan")

	case "/map":
		handleSlashMap(eng)
		return false, true

	case "/setup":
		handleSlashSetup(eng)
		return false, true

	case "/magicdoc":
		handleSlashMagicdoc(ctx, eng, args)
		return false, true

	case "/conversation", "/conv":
		handleSlashConversation(eng, args)
		return false, true

	case "/memory":
		handleSlashMemory(eng, args)
		return false, true

	case "/prompt":
		handleSlashPrompt(eng, args)
		return false, true

	case "/skill":
		handleSlashSkill(eng, args)
		return false, true

	// ── TUI parity: missing dispatcher cases ───────────────────────

	case "/providers":
		// Proxy through engine if it exposes it, otherwise show what's available.
		st := eng.Status()
		fmt.Printf("provider: %s (model=%s)\n", st.Provider, st.Model)
		fmt.Println("(run 'dfmc setup' or edit ~/.config/dfmc/config.toml to add more)")
		return false, true

	case "/models":
		st := eng.Status()
		fmt.Printf("current provider: %s\n", st.Provider)
		fmt.Printf("current model:    %s\n", st.Model)
		return false, true

	case "/redo":
		fmt.Println("redo is not available for conversation history")
		return false, true

	case "/quit", "/exit":
		fmt.Println("goodbye!")
		return true, true

	case "/hooks":
		// Lifecycle hooks are a TUI/approval concept; CLI shows what it knows.
		fmt.Println("lifecycle hooks:")
		fmt.Println("  pre_tool    — before each tool call")
		fmt.Println("  post_tool   — after each tool call")
		fmt.Println("  user_prompt_submit — before sending to model")
		fmt.Println("(configure in ~/.config/dfmc/config.toml → hooks block)")
		return false, true

	case "/approve":
		fmt.Println("(run 'dfmc setup' for approval gate configuration)")
		fmt.Println("tools that prompt for approval: see config.toml → approval_rules")
		return false, true

	case "/reload":
		// Reload config + env — engine reinit.
		fmt.Println("(config reload requires a restart; run 'dfmc' again to pick up changes)")
		st := eng.Status()
		fmt.Printf("current config: provider=%s model=%s project=%s\n",
			st.Provider, st.Model, st.ProjectRoot)
		return false, true

	case "/log":
		// Lightweight log: show recent conversation events.
		active := eng.ConversationActive()
		if active == nil {
			fmt.Println("no active conversation")
			return false, true
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
				preview = preview[:80] + "…"
			}
			fmt.Printf("  [%s]: %s\n", m.Role, preview)
		}
		return false, true

	case "/ls":
		handleSlashList(eng, args)
		return false, true

	case "/read":
		handleSlashRead(eng, args)
		return false, true

	case "/grep":
		handleSlashGrep(eng, args)
		return false, true

	case "/run":
		handleSlashRun(eng, args)
		return false, true

	case "/file":
		fmt.Println("(file picker is TUI-only — use /ls and /read to browse in CLI)")
		fmt.Println("run 'dfmc tui' for the visual file picker")
		return false, true

	case "/coach":
		fmt.Println("(coach notes are TUI-only — run 'dfmc tui' for background coaching)")
		return false, true

	case "/hints":
		fmt.Println("(trajectory hints are TUI-only — run 'dfmc tui' for between-round hints)")
		return false, true

	case "/workflow":
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
		return false, true

	case "/todos":
		fmt.Println("(todo list is a TUI feature — tracked in-memory during the session)")
		fmt.Println("run 'dfmc tui' for the full todo tracking panel")
		return false, true

	case "/tasks":
		fmt.Println("(task store panel is TUI-only — run 'dfmc tui' for the task panel)")
		return false, true

	case "/subagents":
		fmt.Println("(subagent fan-out is TUI-only — run 'dfmc tui' for delegation view)")
		return false, true

	case "/toolstatus":
		fmt.Println("(tool call history is TUI-only — run 'dfmc tui' for the toolstatus panel)")
		// Show a basic count as a fallback.
		tools := eng.ListTools()
		fmt.Printf("available tools: %d\n", len(tools))
		return false, true

	case "/shortcuts", "/keys":
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
		return false, true

	case "/queue":
		fmt.Println("(prompt queue is TUI-only — /btw <note> queues a single note for the next step)")
		fmt.Println("run 'dfmc tui' for the full queue inspector")
		return false, true

	case "/export":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "export error: %v\n", err)
		} else {
			fmt.Println("conversation saved to disk (see ~/.dfmc/conversations/)")
		}
		return false, true

	case "/pin", "/unpin":
		fmt.Println("(pinning is TUI-only — run 'dfmc tui' to pin assistant turns)")
		return false, true

	case "/fork":
		fmt.Println("(branching is TUI-only — use /branch in CLI for a simpler branch flow)")
		fmt.Println("run 'dfmc tui' for the visual branch picker")
		return false, true

	case "/copy":
		fmt.Println("(clipboard copy is TUI-only — pipe output to clipboard manually)")
		return false, true

	case "/intent":
		sub := strings.TrimSpace(strings.Join(args, " "))
		if sub == "show" {
			fmt.Println("intent rewriting is TUI-only — run 'dfmc tui' for the intent inspector")
		} else {
			fmt.Println("(intent toggle is TUI-only — run 'dfmc tui' for between-round intent hints)")
		}
		return false, true

	case "/mouse":
		fmt.Println("(mouse capture toggle is TUI-only — run 'dfmc tui')")
		return false, true

	case "/select":
		fmt.Println("(selection mode is TUI-only — run 'dfmc tui')")
		return false, true

	case "/code":
		fmt.Println("(plan mode toggle is TUI-only — run 'dfmc tui' for plan mode)")
		return false, true

	default:
		return false, false
	}
}
