package cli

// cli_chat_slash_handlers.go — heavyweight per-command handlers
// extracted from the runChatSlash dispatcher. Sibling to
// cli_chat_slash.go which keeps the dispatcher + simple one-liners +
// printChatHelp.
//
//   - handleSlashBranch    /branch [list|create|switch|compare|<name>]
//   - handleSlashContext   /context [show|add|rm]
//   - handleSlashApply     /apply [--check] [patch.diff]
//   - summarizeMessageUsage / estimateConversationCostUSD back /cost.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func handleSlashBranch(eng *engine.Engine, args []string) {
	if eng.ConversationActive() == nil {
		_ = eng.ConversationStart()
	}
	if len(args) == 0 {
		active := eng.ConversationActive()
		fmt.Printf("current branch: %s\n", active.Branch)
		for _, name := range eng.ConversationBranchList() {
			fmt.Printf("- %s\n", name)
		}
		return
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	switch action {
	case "list":
		for _, name := range eng.ConversationBranchList() {
			fmt.Printf("- %s\n", name)
		}
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /branch create <name>")
			return
		}
		name := strings.TrimSpace(args[1])
		if err := eng.ConversationBranchCreate(name); err != nil {
			fmt.Fprintf(os.Stderr, "branch create error: %v\n", err)
			return
		}
		fmt.Printf("branch created: %s\n", name)
	case "switch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /branch switch <name>")
			return
		}
		name := strings.TrimSpace(args[1])
		if err := eng.ConversationBranchSwitch(name); err != nil {
			fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
			return
		}
		fmt.Printf("switched branch: %s\n", name)
	case "compare":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: /branch compare <branch-a> <branch-b>")
			return
		}
		comp, err := eng.ConversationBranchCompare(args[1], args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "branch compare error: %v\n", err)
			return
		}
		fmt.Printf("%s vs %s: shared=%d only_%s=%d only_%s=%d\n",
			comp.BranchA, comp.BranchB, comp.SharedPrefixN, comp.BranchA, comp.OnlyA, comp.BranchB, comp.OnlyB)
	default:
		// /branch <name> => switch if exists, otherwise create+switch
		name := strings.TrimSpace(args[0])
		if err := eng.ConversationBranchSwitch(name); err == nil {
			fmt.Printf("switched branch: %s\n", name)
			return
		}
		if err := eng.ConversationBranchCreate(name); err != nil {
			fmt.Fprintf(os.Stderr, "branch error: %v\n", err)
			return
		}
		if err := eng.ConversationBranchSwitch(name); err != nil {
			fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
			return
		}
		fmt.Printf("created and switched branch: %s\n", name)
	}
}

func handleSlashContext(eng *engine.Engine, args []string) {
	action := "show"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch action {
	case "show":
		preview := eng.ContextBudgetPreview("")
		workspaceFiles := "explicit/tool"
		if preview.AutoIncludeFiles {
			workspaceFiles = "auto"
		}
		fmt.Printf("context budget: provider=%s model=%s task=%s mentions=%d workspace_files=%s scale[t=%.2f f=%.2f pf=%.2f] provider_max=%d available=%d reserve_total=%d reserve[prompt=%d history=%d response=%d tools=%d] total=%d per_file=%d history=%d files=%d compression=%s tests=%t docs=%t\n",
			preview.Provider,
			preview.Model,
			preview.Task,
			preview.ExplicitFileMentions,
			workspaceFiles,
			preview.TaskTotalScale,
			preview.TaskFileScale,
			preview.TaskPerFileScale,
			preview.ProviderMaxContext,
			preview.ContextAvailableTokens,
			preview.ReserveTotalTokens,
			preview.ReservePromptTokens,
			preview.ReserveHistoryTokens,
			preview.ReserveResponseTokens,
			preview.ReserveToolTokens,
			preview.MaxTokensTotal,
			preview.MaxTokensPerFile,
			preview.MaxHistoryTokens,
			preview.MaxFiles,
			preview.Compression,
			preview.IncludeTests,
			preview.IncludeDocs,
		)
		w := eng.MemoryWorking()
		if len(w.RecentFiles) == 0 {
			fmt.Println("context: no recent files yet")
			return
		}
		fmt.Println("recent context files:")
		for _, f := range w.RecentFiles {
			fmt.Printf("- %s\n", f)
		}
	case "messages":
		active := eng.ConversationActive()
		if active == nil {
			fmt.Println("no active conversation")
			return
		}
		msgs := active.Messages()
		for i, m := range msgs {
			preview := m.Content
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			tokens := "?"
			if m.TokenCnt > 0 {
				tokens = fmt.Sprintf("~%d", m.TokenCnt)
			}
			fmt.Printf("  [%2d] %s %s: %s\n", i+1, m.Role, tokens, preview)
		}
	case "add", "rm", "remove":
		fmt.Println("context add/remove is not available in this REPL yet")
	default:
		fmt.Fprintln(os.Stderr, "usage: /context [show|messages|add <file>|rm <file>]")
	}
}

func handleSlashApply(eng *engine.Engine, args []string) {
	checkOnly := false
	diffPath := ""
	for _, a := range args {
		v := strings.TrimSpace(a)
		if v == "" {
			continue
		}
		if v == "--check" {
			checkOnly = true
			continue
		}
		if diffPath == "" {
			diffPath = v
		}
	}
	patchText := ""
	if diffPath != "" {
		data, err := os.ReadFile(diffPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "apply error: cannot read diff file: %v\n", err)
			return
		}
		patchText = extractUnifiedDiff(string(data))
		if strings.TrimSpace(patchText) == "" {
			fmt.Fprintln(os.Stderr, "apply error: no unified diff found in file")
			return
		}
	} else {
		patchText = latestAssistantUnifiedDiff(eng.ConversationActive())
		if strings.TrimSpace(patchText) == "" {
			fmt.Fprintln(os.Stderr, "apply: no assistant diff found. Provide a diff file path or ask for a unified diff.")
			return
		}
	}

	root := strings.TrimSpace(eng.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	if err := applyUnifiedDiff(root, patchText, checkOnly); err != nil {
		fmt.Fprintf(os.Stderr, "apply error: %v\n", err)
		return
	}
	if checkOnly {
		fmt.Println("apply check: patch is valid")
		return
	}
	changed, err := gitChangedFiles(root, 12)
	if err != nil || len(changed) == 0 {
		fmt.Println("patch applied")
		return
	}
	fmt.Printf("patch applied (%d file(s)):\n", len(changed))
	for _, file := range changed {
		fmt.Printf("- %s\n", file)
	}
}

func summarizeMessageUsage(msgs []types.Message) (messages int, users int, assistants int, tokens int) {
	for _, msg := range msgs {
		messages++
		switch msg.Role {
		case types.RoleUser:
			users++
		case types.RoleAssistant:
			assistants++
		}
		if msg.TokenCnt > 0 {
			tokens += msg.TokenCnt
			continue
		}
		// Fallback estimate for historic messages without explicit token count.
		tokens += len(strings.Fields(msg.Content))
	}
	return messages, users, assistants, tokens
}

func estimateConversationCostUSD(provider string, totalTokens int) float64 {
	if totalTokens <= 0 {
		return 0
	}
	blendedPerMillion := map[string]float64{
		"anthropic": 9.0,
		"openai":    6.25,
		"google":    7.0,
		"deepseek":  0.35,
		"kimi":      1.8,
		"minimax":   0.75,
		"zai":       2.1,
		"alibaba":   2.0,
	}
	rate, ok := blendedPerMillion[provider]
	if !ok {
		return -1
	}
	return (float64(totalTokens) / 1_000_000.0) * rate
}

func printChatHelp() {
	fmt.Println("DFMC slash commands")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println("  /help          show this list")
	fmt.Println("  /clear         start a fresh conversation")
	fmt.Println("  /save          save conversation to disk")
	fmt.Println("  /load <id>     load a saved conversation")
	fmt.Println("  /branch [list|create|switch|<name>]")
	fmt.Println("  /context [show|add|rm]")
	fmt.Println("  /provider [name]      show / switch provider")
	fmt.Println("  /model [name]         show / switch model")
	fmt.Println("  /providers            list configured providers")
	fmt.Println("  /models               list models for current provider")
	fmt.Println("  /undo                 undo the last assistant reply")
	fmt.Println("  /redo                 redo the undone reply")
	fmt.Println("  /apply [--check] [diff file]")
	fmt.Println("  /retry                resend the last user message")
	fmt.Println("  /cancel, /stop        cancel active agent loop")
	fmt.Println("  /continue             resume parked agent loop")
	fmt.Println("  /agents               list roles and profiles")
	fmt.Println("  /stats                provider · model · project")
	fmt.Println("  /version              dfmc version")
	fmt.Println("  /drive                start autonomous run")
	fmt.Println("  /drive active         show current run")
	fmt.Println("  /drive list           list recent runs")
	fmt.Println("  /drive stop           stop active run")
	fmt.Println("  /drive resume <id>    resume a stopped run")
	fmt.Println("  /plan                 plan mode (read-only; use TUI for full experience)")
	fmt.Println("  /compact              transcript compaction (use TUI for full experience)")
	fmt.Println("  /btw <note>           queue a note for the next step")
	fmt.Println("  /split <task>         decompose a task into subtasks")
	fmt.Println("  /doctor               health snapshot")
	fmt.Println("  /ask <question>       ask a question")
	fmt.Println("  /review <file>        review code")
	fmt.Println("  /explain <file>       explain code")
	fmt.Println("  /refactor <file>      refactor code")
	fmt.Println("  /test <file>          generate tests")
	fmt.Println("  /doc <file>           write documentation")
	fmt.Println("  /map                  render codemap")
	fmt.Println("  /scan                 scan for smells")
	fmt.Println("  /file              file picker (TUI: @ or /file; use /ls in CLI)")
	fmt.Println("  /coach             toggle background coach notes (TUI only)")
	fmt.Println("  /hints             toggle trajectory hints (TUI only)")
	fmt.Println("  /workflow          show todos, agent progress, drive status")
	fmt.Println("  /todos             shared todo list (TUI panel; /todos clear to reset)")
	fmt.Println("  /tasks             task store panel (TUI only; /tasks list in CLI)")
	fmt.Println("  /subagents         subagent delegation view (TUI only)")
	fmt.Println("  /toolstatus        tool call history (TUI only)")
	fmt.Println("  /shortcuts, /keys  cheat sheet for TUI keybindings")
	fmt.Println("  /queue             prompt queue inspector (TUI only; /btw for single note)")
	fmt.Println("  /export            save transcript to .dfmc/exports/ (same as /save)")
	fmt.Println("  /pin /unpin        pin assistant turns (TUI only)")
	fmt.Println("  /fork              visual branch picker (TUI only; /branch for CLI)")
	fmt.Println("  /copy              copy last reply to clipboard (TUI only)")
	fmt.Println("  /intent            toggle intent rewriting (TUI only)")
	fmt.Println("  /mouse             toggle mouse capture (TUI only)")
	fmt.Println("  /select            selection mode (TUI only)")
	fmt.Println("  /code              exit plan mode (TUI only)")
	fmt.Println()
	fmt.Println("  /magicdoc <target> generate documentation via AI")
	fmt.Println("  /conversation [list|save|clear]")
	fmt.Println("  /prompt [show|context]")
	fmt.Println("  /skill [list|<name>]")
	fmt.Println()
	fmt.Println("TUI-only commands (/plan, /compact, /fork, …) — run `dfmc tui` for full experience.")
	fmt.Println(strings.Repeat("─", 50))
}

// runAgentTemplate dispatches a named template command (review, explain,
// refactor, test, doc, analyze, scan) by building a prompt that targets
// the user's args and streaming the result to stdout — mirroring the
// same pattern as /ask but with a skill-injected template context.
func runAgentTemplate(ctx context.Context, eng *engine.Engine, args []string, skill string) (exit bool, handled bool) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		fmt.Fprintf(os.Stderr, "usage: /%s <target or question>\n", skill)
		return false, true
	}

	// Build a skill-prefixed prompt so the engine routes through the
	// matching skill handler (review/explain/refactor/test/doc/etc.).
	prompt := fmt.Sprintf("Skill: %s\n%s", skill, query)

	stream, err := eng.StreamAsk(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "/%s error: %v\n", skill, err)
		return false, true
	}
	printProviderStream(stream)
	return false, true
}
