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
	case "add", "rm", "remove":
		fmt.Println("context add/remove is not available in this REPL yet")
	default:
		fmt.Fprintln(os.Stderr, "usage: /context [show|add <file>|rm <file>]")
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
