// Chat slash-command dispatcher. Extracted from cli_ask_chat.go so
// the interactive loop entry points stay terse. Owns the slash command
// switch table; the heavyweight per-command handlers (/branch, /context,
// /apply) plus the /cost helpers live in cli_chat_slash_handlers.go.

package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
		handleSlashBranch(eng, args)
		return false, true

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
		handleSlashContext(eng, args)
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
		handleSlashApply(eng, args)
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

