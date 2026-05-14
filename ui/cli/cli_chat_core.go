package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func handleSlashClear(eng *engine.Engine) {
	conv := eng.ConversationStart()
	if conv == nil {
		fmt.Fprintln(os.Stderr, "unable to create conversation")
		return
	}
	fmt.Printf("Started new conversation: %s\n", conv.ID)
}

func handleSlashSave(eng *engine.Engine) {
	if err := eng.ConversationSave(); err != nil {
		fmt.Fprintf(os.Stderr, "save error: %v\n", err)
	} else {
		fmt.Println("conversation saved")
	}
}

func handleSlashLoad(eng *engine.Engine, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: /load <conversation-id>")
		return
	}
	conv, err := eng.ConversationLoad(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load error: %v\n", err)
		return
	}
	fmt.Printf("Loaded conversation %s (%d messages)\n", conv.ID, len(conv.Messages()))
}

func handleSlashHistory(eng *engine.Engine, args []string) {
	limit := 10
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := eng.ConversationList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "history error: %v\n", err)
		return
	}
	for i, item := range items {
		if i >= limit {
			break
		}
		fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
	}
}

func handleSlashProvider(eng *engine.Engine, args []string) {
	if len(args) == 0 {
		st := eng.Status()
		fmt.Printf("provider=%s model=%s\n", st.Provider, st.Model)
		return
	}
	eng.SetProviderModel(strings.TrimSpace(args[0]), "")
	st := eng.Status()
	fmt.Printf("provider set to %s (model=%s)\n", st.Provider, st.Model)
}

func handleSlashModel(eng *engine.Engine, args []string) {
	if len(args) == 0 {
		st := eng.Status()
		fmt.Printf("provider=%s model=%s\n", st.Provider, st.Model)
		return
	}
	st := eng.Status()
	eng.SetProviderModel(st.Provider, strings.TrimSpace(args[0]))
	st = eng.Status()
	fmt.Printf("model set to %s (provider=%s)\n", st.Model, st.Provider)
}

func handleSlashTools(eng *engine.Engine) {
	for _, t := range eng.ListTools() {
		fmt.Printf("- %s\n", t)
	}
}

func handleSlashSkills(eng *engine.Engine) {
	for _, s := range discoverSkills(eng.Status().ProjectRoot) {
		source := s.Source
		if s.Builtin {
			source = "builtin"
		}
		fmt.Printf("- %s [%s]\n", s.Name, source)
	}
}

func handleSlashCost(eng *engine.Engine) {
	active := eng.ConversationActive()
	if active == nil {
		fmt.Println("no active conversation")
		return
	}
	msgN, userN, assistantN, tokenN := summarizeMessageUsage(active.Messages())
	usd := estimateConversationCostUSD(strings.ToLower(strings.TrimSpace(active.Provider)), tokenN)
	fmt.Printf("messages=%d user=%d assistant=%d tokens=%d", msgN, userN, assistantN, tokenN)
	if usd >= 0 {
		fmt.Printf(" approx_cost=$%.6f", usd)
	}
	fmt.Println()
}

func handleSlashDiff(eng *engine.Engine) {
	diff, err := gitWorkingDiff(eng.Status().ProjectRoot, 200_000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff error: %v\n", err)
		return
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("working tree is clean")
		return
	}
	fmt.Print(diff)
	if !strings.HasSuffix(diff, "\n") {
		fmt.Println()
	}
}

func handleSlashUndo(eng *engine.Engine) {
	removed, err := eng.ConversationUndoLast()
	if err != nil {
		fmt.Fprintf(os.Stderr, "undo error: %v\n", err)
		return
	}
	fmt.Printf("undone messages: %d\n", removed)
}

func handleSlashCancel(eng *engine.Engine) {
	if eng.HasParkedAgent() {
		eng.ClearParkedAgent()
		fmt.Println("parked agent cleared")
	} else {
		fmt.Println("no active agent loop to cancel")
	}
}

func handleSlashAgents(eng *engine.Engine) {
	cat := eng.Agents()
	if len(cat.Roles) > 0 {
		fmt.Println("roles:")
		for _, r := range cat.Roles {
			fmt.Printf("  - %s: %s\n", r.Role, truncateLine(r.Summary, 80))
		}
	}
	if len(cat.Profiles) > 0 {
		fmt.Println("profiles:")
		for _, p := range cat.Profiles {
			cfg := ""
			if !p.Configured {
				cfg = " (not configured)"
			}
			active := ""
			if p.Active {
				active = " [active]"
			}
			model := p.Model
			if model == "" {
				model = "(inherited)"
			}
			fmt.Printf("  - %s model=%s%s%s\n", p.Name, model, cfg, active)
		}
	}
	if len(cat.Roles) == 0 && len(cat.Profiles) == 0 {
		fmt.Println("no agents configured")
	}
}

func handleSlashStats(eng *engine.Engine) {
	st := eng.Status()
	fmt.Printf("provider=%s model=%s project=%s\n", st.Provider, st.Model, st.ProjectRoot)
}

func handleSlashDrive(eng *engine.Engine) {
	dir := eng.DriveReportDir()
	if dir == "" {
		fmt.Println("no active drive run")
	} else {
		fmt.Printf("drive reports: %s\n", dir)
	}
}

func handleSlashBtw(eng *engine.Engine, args []string) {
	note := strings.TrimSpace(strings.Join(args, " "))
	if note == "" {
		fmt.Fprintln(os.Stderr, "usage: /btw <note>")
		return
	}
	eng.QueueAgentNote(note)
	fmt.Println("/btw queued: " + note)
}

func handleSlashSplit(args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: /split <task description>")
		return
	}
	result := planning.SplitTask(query)
	if len(result.Subtasks) == 0 {
		fmt.Println("(no subtasks detected)")
		return
	}
	fmt.Printf("subtasks (%d):\n", len(result.Subtasks))
	for i, t := range result.Subtasks {
		title := t.Title
		if title == "" {
			title = t.Description
		}
		fmt.Printf("  %d. %s\n", i+1, title)
	}
}

func handleSlashDoctor(eng *engine.Engine) {
	st := eng.Status()
	ready := "ready"
	if st.Provider == "" || st.Model == "" {
		ready = "not configured"
	}
	fmt.Printf("version=%s provider=%s model=%s status=%s\n", eng.Version, st.Provider, st.Model, ready)
}

func handleSlashSetup(eng *engine.Engine) {
	fmt.Println("(run 'dfmc setup' from the shell for the full interactive setup wizard)")
	st := eng.Status()
	if st.Provider == "" || st.Model == "" {
		fmt.Println("  provider/model not configured yet - run: dfmc setup")
	} else {
		fmt.Printf("  provider=%s model=%s ok\n", st.Provider, st.Model)
	}
}
