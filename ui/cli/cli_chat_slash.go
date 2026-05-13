// Chat slash-command dispatcher. Extracted from cli_ask_chat.go so
// the interactive loop entry points stay terse. Owns the slash command
// switch table; the heavyweight per-command handlers (/branch, /context,
// /apply) plus the /cost helpers live in cli_chat_slash_handlers.go.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
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

	case "/cancel", "/stop":
		if eng.HasParkedAgent() {
			eng.ClearParkedAgent()
			fmt.Println("parked agent cleared")
		} else {
			fmt.Println("no active agent loop to cancel")
		}
		return false, true

	case "/retry":
		active := eng.ConversationActive()
		if active == nil {
			fmt.Fprintln(os.Stderr, "no active conversation")
			return false, true
		}
		msgs := active.Messages()
		var lastUser string
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == types.RoleUser {
				lastUser = msgs[i].Content
				break
			}
		}
		if lastUser == "" {
			fmt.Fprintln(os.Stderr, "no prior user message to retry")
			return false, true
		}
		_, _ = eng.ConversationUndoLast()
		fmt.Println("(resending last question…)")
		stream, err := eng.StreamAsk(ctx, lastUser)
		if err != nil {
			fmt.Fprintf(os.Stderr, "retry error: %v\n", err)
			return false, true
		}
		printed := false
		endsWithNL := true
		for ev := range stream {
			if ev.Type == "delta" {
				fmt.Print(ev.Delta)
				printed = true
				endsWithNL = strings.HasSuffix(ev.Delta, "\n")
			}
			if ev.Type == "error" {
				if printed && !endsWithNL {
					fmt.Println()
				}
				fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
				printed = false
			}
		}
		if printed && !endsWithNL {
			fmt.Println()
		}
		return false, true

	case "/continue":
		if !eng.HasParkedAgent() {
			fmt.Fprintln(os.Stderr, "nothing to resume — no parked agent loop")
			return false, true
		}
		note := strings.TrimSpace(strings.Join(args, " "))
		fmt.Println("(resuming agent loop…)")
		stream, err := eng.StreamAsk(ctx, note)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resume error: %v\n", err)
			return false, true
		}
		printed := false
		endsWithNL := true
		for ev := range stream {
			if ev.Type == "delta" {
				fmt.Print(ev.Delta)
				printed = true
				endsWithNL = strings.HasSuffix(ev.Delta, "\n")
			}
			if ev.Type == "error" {
				if printed && !endsWithNL {
					fmt.Println()
				}
				fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
				printed = false
			}
		}
		if printed && !endsWithNL {
			fmt.Println()
		}
		return false, true

	case "/agents":
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
		return false, true

	case "/stats":
		st := eng.Status()
		fmt.Printf("provider=%s model=%s project=%s\n", st.Provider, st.Model, st.ProjectRoot)
		return false, true

	case "/version":
		fmt.Printf("dfmc version=%s\n", eng.Version)
		return false, true

	case "/drive":
		dir := eng.DriveReportDir()
		if dir == "" {
			fmt.Println("no active drive run")
		} else {
			fmt.Printf("drive reports: %s\n", dir)
		}
		return false, true

	case "/plan":
		fmt.Println("plan mode is not available in CLI chat; use the TUI (/tui) for plan mode")
		return false, true

	case "/compact":
		fmt.Println("(CLI does not compact the transcript; use /clear instead)")
		return false, true

	case "/btw":
		note := strings.TrimSpace(strings.Join(args, " "))
		if note == "" {
			fmt.Fprintln(os.Stderr, "usage: /btw <note>")
			return false, true
		}
		eng.QueueAgentNote(note)
		fmt.Println("/btw queued: " + note)
		return false, true

	case "/split":
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			fmt.Fprintln(os.Stderr, "usage: /split <task description>")
			return false, true
		}
		result := planning.SplitTask(query)
		if len(result.Subtasks) == 0 {
			fmt.Println("(no subtasks detected)")
			return false, true
		}
		fmt.Printf("subtasks (%d):\n", len(result.Subtasks))
		for i, t := range result.Subtasks {
			title := t.Title
			if title == "" {
				title = t.Description
			}
			fmt.Printf("  %d. %s\n", i+1, title)
		}
		return false, true

	case "/doctor":
		st := eng.Status()
		ready := "ready"
		if st.Provider == "" || st.Model == "" {
			ready = "not configured"
		}
		fmt.Printf("version=%s provider=%s model=%s status=%s\n", eng.Version, st.Provider, st.Model, ready)
		return false, true

	// ── Agent/template commands (ask, review, explain, …) ─────────

	case "/ask":
		payload := strings.TrimSpace(strings.Join(args, " "))
		if payload == "" {
			fmt.Fprintln(os.Stderr, "usage: /ask <question>")
			return false, true
		}
		stream, err := eng.StreamAsk(ctx, payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask error: %v\n", err)
			return false, true
		}
		printed := false
		endsWithNL := true
		for ev := range stream {
			if ev.Type == "delta" {
				fmt.Print(ev.Delta)
				printed = true
				endsWithNL = strings.HasSuffix(ev.Delta, "\n")
			}
			if ev.Type == "error" {
				if printed && !endsWithNL {
					fmt.Println()
				}
				fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
				printed = false
			}
		}
		if printed && !endsWithNL {
			fmt.Println()
		}
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
		st := eng.Status()
		fmt.Printf("project root: %s\n", st.ProjectRoot)
		if st.ProjectRoot != "" {
			// Walk top-level dirs as a lightweight "codemap" for CLI.
			entries, _ := os.ReadDir(st.ProjectRoot)
			fmt.Println("top-level:")
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "_") {
					fmt.Printf("  📁 %s/\n", e.Name())
				} else if !e.IsDir() {
					fmt.Printf("  📄 %s\n", e.Name())
				}
			}
		}
		return false, true

	case "/setup":
		fmt.Println("(run 'dfmc setup' from the shell for the full interactive setup wizard)")
		st := eng.Status()
		if st.Provider == "" || st.Model == "" {
			fmt.Println("  provider/model not configured yet — run: dfmc setup")
		} else {
			fmt.Printf("  provider=%s model=%s ✓\n", st.Provider, st.Model)
		}
		return false, true

	case "/magicdoc":
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			fmt.Fprintln(os.Stderr, "usage: /magicdoc <target> — describe a file, package, or symbol")
			return false, true
		}
		stream, err := eng.StreamAsk(ctx, "Generate documentation for: "+query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc error: %v\n", err)
			return false, true
		}
		printed := false
		endsWithNL := true
		for ev := range stream {
			if ev.Type == "delta" {
				fmt.Print(ev.Delta)
				printed = true
				endsWithNL = strings.HasSuffix(ev.Delta, "\n")
			}
			if ev.Type == "error" {
				if printed && !endsWithNL {
					fmt.Println()
				}
				fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
				printed = false
			}
		}
		if printed && !endsWithNL {
			fmt.Println()
		}
		return false, true

	case "/conversation", "/conv":
		sub := strings.TrimSpace(strings.Join(args, " "))
		switch sub {
		case "list", "":
			items, err := eng.ConversationList()
			if err != nil {
				fmt.Fprintf(os.Stderr, "list error: %v\n", err)
				return false, true
			}
			if len(items) == 0 {
				fmt.Println("no saved conversations")
				return false, true
			}
			fmt.Printf("saved conversations (%d):\n", len(items))
			activeConv := eng.ConversationActive()
			for _, it := range items {
				active := ""
				if activeConv != nil && it.ID == activeConv.ID {
					active = " [active]"
				}
				fmt.Printf("  - %s (%d msgs)%s\n", it.ID, it.MessageN, active)
			}
			return false, true
		case "save":
			if err := eng.ConversationSave(); err != nil {
				fmt.Fprintf(os.Stderr, "save error: %v\n", err)
			} else {
				fmt.Println("conversation saved")
			}
			return false, true
		case "clear", "reset":
			eng.ConversationStart()
			fmt.Println("conversation cleared — started fresh")
			return false, true
		default:
			fmt.Fprintf(os.Stderr, "usage: /conversation [list|save|clear]\n")
			return false, true
		}

	case "/memory":
		sub := strings.TrimSpace(strings.Join(args, " "))
		switch sub {
		case "show", "":
			w := eng.MemoryWorking()
			fmt.Printf("last question: %s\n", w.LastQuestion)
			fmt.Printf("last answer: %s\n", truncateLine(w.LastAnswer, 120))
			fmt.Printf("recent files (%d):\n", len(w.RecentFiles))
			for _, f := range w.RecentFiles {
				fmt.Printf("  - %s\n", f)
			}
			return false, true
		case "clear":
			// Memory clear is engine-internal; expose the signal.
			fmt.Println("(memory is managed automatically — /clear resets conversation context)")
			return false, true
		default:
			fmt.Fprintf(os.Stderr, "usage: /memory [show|clear]\n")
			return false, true
		}

	case "/prompt":
		sub := strings.TrimSpace(strings.Join(args, " "))
		switch sub {
		case "show", "":
			st := eng.Status()
			fmt.Println("active provider/model:")
			fmt.Printf("  provider: %s\n", st.Provider)
			fmt.Printf("  model:    %s\n", st.Model)
			fmt.Println("prompt context: conversation history + project root")
			return false, true
		case "context":
			ctx := eng.MemoryWorking()
			fmt.Printf("working context size estimate: %d recent files\n", len(ctx.RecentFiles))
			return false, true
		default:
			fmt.Fprintf(os.Stderr, "usage: /prompt [show|context]\n")
			return false, true
		}

	case "/skill":
		sub := strings.TrimSpace(strings.Join(args, " "))
		if sub == "" || sub == "list" {
			for _, s := range discoverSkills(eng.Status().ProjectRoot) {
				source := s.Source
				if s.Builtin {
					source = "builtin"
				}
				fmt.Printf("- %s [%s]\n", s.Name, source)
			}
			return false, true
		}
		fmt.Printf("(skill '%s' — run 'dfmc skill %s' for details)\n", sub, sub)
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
		target := "."
		if len(args) > 0 {
			target = args[0]
		}
		root := eng.Status().ProjectRoot
		base := root
		if !filepath.IsAbs(target) {
			base = filepath.Join(root, target)
		} else {
			base = target
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ls error: %v\n", err)
			return false, true
		}
		for _, e := range entries {
			rel, _ := filepath.Rel(root, filepath.Join(base, e.Name()))
			if e.IsDir() {
				fmt.Printf("  📁 %s/\n", rel)
			} else {
				fmt.Printf("  📄 %s\n", rel)
			}
		}
		return false, true

	case "/read":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /read <path> [line_start [line_end]]")
			return false, true
		}
		path := args[0]
		st := eng.Status()
		if !filepath.IsAbs(path) && st.ProjectRoot != "" {
			path = filepath.Join(st.ProjectRoot, path)
		}
		start, end := 1, 200
		if len(args) > 1 {
			n, _ := strconv.Atoi(args[1])
			if n > 0 {
				start = n
			}
		}
		if len(args) > 2 {
			n, _ := strconv.Atoi(args[2])
			if n > 0 {
				end = n
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			return false, true
		}
		lines := strings.Split(string(data), "\n")
		for i := start - 1; i < len(lines) && i < end; i++ {
			fmt.Printf("%4d  %s\n", i+1, lines[i])
		}
		return false, true

	case "/grep":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /grep <pattern> [path]")
			return false, true
		}
		pattern := args[0]
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		root := eng.Status().ProjectRoot
		if !filepath.IsAbs(dir) && root != "" {
			dir = filepath.Join(root, dir)
		}
		count := 0
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && (info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "_build") {
				return filepath.SkipDir
			}
			if info.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bad regex: %v\n", err)
				return nil
			}
			for i, line := range strings.Split(string(data), "\n") {
				if re.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					if rel == "" || rel == "." {
						rel = path
					}
					fmt.Printf("%s:%d: %s\n", rel, i+1, truncateLine(line, 120))
					count++
					if count >= 50 {
						return errors.New("limit")
					}
				}
			}
			return nil
		})
		if count == 0 {
			fmt.Println("(no matches)")
		}
		return false, true

	case "/run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /run <command> [args…]")
			return false, true
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = eng.Status().ProjectRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		}
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
