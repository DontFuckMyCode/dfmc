// Chat slash-command dispatcher. Extracted from cli_ask_chat.go so
// the interactive loop entry points stay terse. Owns the slash command
// switch table; the heavyweight per-command handlers (/branch, /context,
// /apply) plus the /cost helpers live in cli_chat_slash_handlers.go.

package cli

import (
	"context"
	"fmt"
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
	if exit, handled := handleSlashTUIParity(eng, cmd, args); handled {
		return exit, true
	}

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

	default:
		return false, false
	}
}
