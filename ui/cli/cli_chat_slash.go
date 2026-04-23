// Chat slash-command dispatcher. Extracted from cli_ask_chat.go so
// the interactive loop entry points stay terse. Owns the /help,
// /clear, /save, /load, /history, /provider, /model, /branch,
// /context, /memory, /tools, /skills, /diff, /undo, /apply, /run,
// /cost command table plus the two small post-handlers
// (summarizeMessageUsage, estimateConversationCostUSD) that back
// the /cost surface.

package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
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

	case "/exit":
		return true, true

	case "/clear":
		conv := eng.ConversationStart()
		if conv == nil {
			fmt.Fprintln(os.Stderr, "unable to create conversation")
			return false, true
		}
		fmt.Printf("Started new conversation: %s\n", conv.ID)
		return false, true

	case "/save":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "save error: %v\n", err)
		} else {
			fmt.Println("conversation saved")
		}
		return false, true

	case "/load":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /load <conversation-id>")
			return false, true
		}
		conv, err := eng.ConversationLoad(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "load error: %v\n", err)
			return false, true
		}
		fmt.Printf("Loaded conversation %s (%d messages)\n", conv.ID, len(conv.Messages()))
		return false, true

	case "/history":
		limit := 10
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := eng.ConversationList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "history error: %v\n", err)
			return false, true
		}
		for i, item := range items {
			if i >= limit {
				break
			}
			fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
		}
		return false, true

	case "/memory":
		w := eng.MemoryWorking()
		fmt.Printf("last question: %s\n", w.LastQuestion)
		fmt.Printf("last answer: %s\n", truncateLine(w.LastAnswer, 120))
		fmt.Printf("recent files: %d\n", len(w.RecentFiles))
		return false, true

	case "/provider":
		if len(args) == 0 {
			st := eng.Status()
			fmt.Printf("provider=%s model=%s\n", st.Provider, st.Model)
			return false, true
		}
		eng.SetProviderModel(strings.TrimSpace(args[0]), "")
		st := eng.Status()
		fmt.Printf("provider set to %s (model=%s)\n", st.Provider, st.Model)
		return false, true

	case "/model":
		if len(args) == 0 {
			st := eng.Status()
			fmt.Printf("provider=%s model=%s\n", st.Provider, st.Model)
			return false, true
		}
		st := eng.Status()
		eng.SetProviderModel(st.Provider, strings.TrimSpace(args[0]))
		st = eng.Status()
		fmt.Printf("model set to %s (provider=%s)\n", st.Model, st.Provider)
		return false, true

	case "/branch":
		if eng.ConversationActive() == nil {
			_ = eng.ConversationStart()
		}
		if len(args) == 0 {
			active := eng.ConversationActive()
			fmt.Printf("current branch: %s\n", active.Branch)
			for _, name := range eng.ConversationBranchList() {
				fmt.Printf("- %s\n", name)
			}
			return false, true
		}
		action := strings.ToLower(strings.TrimSpace(args[0]))
		switch action {
		case "list":
			for _, name := range eng.ConversationBranchList() {
				fmt.Printf("- %s\n", name)
			}
			return false, true
		case "create":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: /branch create <name>")
				return false, true
			}
			name := strings.TrimSpace(args[1])
			if err := eng.ConversationBranchCreate(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch create error: %v\n", err)
				return false, true
			}
			fmt.Printf("branch created: %s\n", name)
			return false, true
		case "switch":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: /branch switch <name>")
				return false, true
			}
			name := strings.TrimSpace(args[1])
			if err := eng.ConversationBranchSwitch(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
				return false, true
			}
			fmt.Printf("switched branch: %s\n", name)
			return false, true
		case "compare":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: /branch compare <branch-a> <branch-b>")
				return false, true
			}
			comp, err := eng.ConversationBranchCompare(args[1], args[2])
			if err != nil {
				fmt.Fprintf(os.Stderr, "branch compare error: %v\n", err)
				return false, true
			}
			fmt.Printf("%s vs %s: shared=%d only_%s=%d only_%s=%d\n",
				comp.BranchA, comp.BranchB, comp.SharedPrefixN, comp.BranchA, comp.OnlyA, comp.BranchB, comp.OnlyB)
			return false, true
		default:
			// /branch <name> => switch if exists, otherwise create+switch
			name := strings.TrimSpace(args[0])
			if err := eng.ConversationBranchSwitch(name); err == nil {
				fmt.Printf("switched branch: %s\n", name)
				return false, true
			}
			if err := eng.ConversationBranchCreate(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch error: %v\n", err)
				return false, true
			}
			if err := eng.ConversationBranchSwitch(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
				return false, true
			}
			fmt.Printf("created and switched branch: %s\n", name)
			return false, true
		}

	case "/tools":
		for _, t := range eng.ListTools() {
			fmt.Printf("- %s\n", t)
		}
		return false, true

	case "/skills":
		for _, s := range discoverSkills(eng.Status().ProjectRoot) {
			source := s.Source
			if s.Builtin {
				source = "builtin"
			}
			fmt.Printf("- %s [%s]\n", s.Name, source)
		}
		return false, true

	case "/context":
		action := "show"
		if len(args) > 0 {
			action = strings.ToLower(strings.TrimSpace(args[0]))
		}
		switch action {
		case "show":
			preview := eng.ContextBudgetPreview("")
			fmt.Printf("context budget: provider=%s model=%s task=%s mentions=%d scale[t=%.2f f=%.2f pf=%.2f] provider_max=%d available=%d reserve_total=%d reserve[prompt=%d history=%d response=%d tools=%d] total=%d per_file=%d history=%d files=%d compression=%s tests=%t docs=%t\n",
				preview.Provider,
				preview.Model,
				preview.Task,
				preview.ExplicitFileMentions,
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
				return false, true
			}
			fmt.Println("recent context files:")
			for _, f := range w.RecentFiles {
				fmt.Printf("- %s\n", f)
			}
		case "add", "rm", "remove":
			fmt.Println("context add/remove is not available in this REPL yet")
		default:
			fmt.Fprintln(os.Stderr, "usage: /context [show|add <file>|rm <file>]")
		}
		return false, true

	case "/cost":
		active := eng.ConversationActive()
		if active == nil {
			fmt.Println("no active conversation")
			return false, true
		}
		msgN, userN, assistantN, tokenN := summarizeMessageUsage(active.Messages())
		usd := estimateConversationCostUSD(strings.ToLower(strings.TrimSpace(active.Provider)), tokenN)
		fmt.Printf("messages=%d user=%d assistant=%d tokens=%d", msgN, userN, assistantN, tokenN)
		if usd >= 0 {
			fmt.Printf(" approx_cost=$%.6f", usd)
		}
		fmt.Println()
		return false, true

	case "/diff":
		diff, err := gitWorkingDiff(eng.Status().ProjectRoot, 200_000)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff error: %v\n", err)
			return false, true
		}
		if strings.TrimSpace(diff) == "" {
			fmt.Println("working tree is clean")
			return false, true
		}
		fmt.Print(diff)
		if !strings.HasSuffix(diff, "\n") {
			fmt.Println()
		}
		return false, true

	case "/undo":
		removed, err := eng.ConversationUndoLast()
		if err != nil {
			fmt.Fprintf(os.Stderr, "undo error: %v\n", err)
			return false, true
		}
		fmt.Printf("undone messages: %d\n", removed)
		return false, true

	case "/apply":
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
				return false, true
			}
			patchText = extractUnifiedDiff(string(data))
			if strings.TrimSpace(patchText) == "" {
				fmt.Fprintln(os.Stderr, "apply error: no unified diff found in file")
				return false, true
			}
		} else {
			patchText = latestAssistantUnifiedDiff(eng.ConversationActive())
			if strings.TrimSpace(patchText) == "" {
				fmt.Fprintln(os.Stderr, "apply: no assistant diff found. Provide a diff file path or ask for a unified diff.")
				return false, true
			}
		}

		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		if err := applyUnifiedDiff(root, patchText, checkOnly); err != nil {
			fmt.Fprintf(os.Stderr, "apply error: %v\n", err)
			return false, true
		}
		if checkOnly {
			fmt.Println("apply check: patch is valid")
			return false, true
		}
		changed, err := gitChangedFiles(root, 12)
		if err != nil || len(changed) == 0 {
			fmt.Println("patch applied")
			return false, true
		}
		fmt.Printf("patch applied (%d file(s)):\n", len(changed))
		for _, file := range changed {
			fmt.Printf("- %s\n", file)
		}
		return false, true

	case "/run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /run <skill> [input]")
			return false, true
		}
		name := strings.TrimSpace(args[0])
		input := strings.TrimSpace(strings.Join(args[1:], " "))
		if input == "" {
			input = "Analyze the current project."
		}
		_ = runNamedSkill(ctx, eng, name, input, false)
		return false, true
	}

	return false, false
}

func printChatHelp() {
	fmt.Println(`Chat slash commands:
  /help                         Show this help
  /exit                         Exit chat
  /clear                        Start a new conversation
  /save                         Save active conversation
  /load <id>                    Load conversation by id
  /history [limit]              List saved conversations
  /provider [name]              Show/set provider
  /model [name]                 Show/set model
  /branch [name]                Switch/create branch
  /branch list                  List branches
  /branch create <name>         Create branch
  /branch switch <name>         Switch branch
  /branch compare <a> <b>       Compare branches
  /context [show]               Show recent context files
  /memory                       Show working memory snapshot
  /tools                        List tools
  /skills                       List skills
  /diff                         Show working tree diff
  /undo                         Undo last conversation exchange
  /apply [--check] [patch.diff] Apply latest assistant unified diff (or diff file)
  /run <skill> [input]          Run skill
  /cost                         Show token/cost summary`)
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

