package tui

// slash_handlers.go — concrete implementations for the expanded slash-command
// surface (F1c / F1d / F1e). Each helper is self-contained: it takes the
// command's raw args, does the work (either composing a prompt to feed the
// chat pipeline or calling an engine method directly), and returns either a
// formatted string to append to the transcript or a (Model, tea.Cmd, bool)
// triple matching the switch's signature.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// runTemplateSlash composes a natural-language prompt for one of the
// skill-style shortcuts (/review, /explain, /refactor, /test, /doc) and feeds
// it through the normal chat pipeline so it benefits from streaming, context
// injection, and agent-loop tooling without duplicating any of that code.
func (m Model) runTemplateSlash(verb string, args []string, raw string) (Model, tea.Cmd, bool) {
	verb = strings.ToLower(strings.TrimSpace(verb))
	if verb == "" {
		return m, nil, false
	}
	_ = raw
	payload := strings.TrimSpace(strings.Join(args, " "))
	targets, tail := splitTargetsAndTail(args)
	pinned := strings.TrimSpace(m.pinnedFile)
	if len(targets) == 0 && pinned != "" {
		targets = []string{pinned}
	}

	var prompt string
	switch verb {
	case "review":
		prompt = composeReviewPrompt(targets, tail)
	case "explain":
		prompt = composeExplainPrompt(targets, tail)
	case "refactor":
		prompt = composeRefactorPrompt(targets, tail)
	case "test":
		prompt = composeTestPrompt(targets, tail)
	case "doc":
		prompt = composeDocPrompt(targets, tail)
	default:
		return m, nil, false
	}
	if prompt == "" {
		prompt = payload
	}
	if strings.TrimSpace(prompt) == "" {
		m.notice = "/" + verb + " needs a file or topic."
		return m.appendSystemMessage("Usage: /" + verb + " <path|topic>"), nil, true
	}
	m.input = ""
	m = m.appendSystemMessage(fmt.Sprintf("/%s → submitting as chat: %s", verb, truncateSingleLine(prompt, 120)))
	next, cmdOut := m.submitChatQuestion(prompt, nil)
	return next, cmdOut, true
}

func composeReviewPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Review the code base. Focus on correctness, risks, missing tests. " + tail)
	}
	return fmt.Sprintf("Review the following file(s) for correctness, risks, readability, and missing tests: %s\n%s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

func composeExplainPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Explain the recent changes or the listed topic: " + tail)
	}
	return fmt.Sprintf("Explain what this code does, its structure, and any non-obvious invariants: %s\n%s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

func composeRefactorPrompt(targets []string, tail string) string {
	goal := strings.TrimSpace(tail)
	if goal == "" {
		goal = "propose a scoped, reversible refactor plan"
	}
	if len(targets) == 0 {
		return "Refactor target unspecified — " + goal
	}
	return fmt.Sprintf("Refactor %s. Goal: %s. Produce a scoped, reversible plan with file-level edits.",
		joinFileMarkers(targets), goal)
}

func composeTestPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Draft tests for the recent changes. " + tail)
	}
	return fmt.Sprintf("Draft tests for %s. Cover happy path, edge cases, and one regression. %s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

func composeDocPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Draft or update documentation. " + tail)
	}
	return fmt.Sprintf("Draft or update documentation for %s. Keep it concise and reference-style. %s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

// splitTargetsAndTail separates path-looking args from the free-form tail
// (used for `--goal <text>`, `--framework pytest`, etc.). An arg is treated as
// a target if it contains a path separator, a file extension, or is a bare
// identifier that would plausibly be a filename.
func splitTargetsAndTail(args []string) ([]string, string) {
	targets := make([]string, 0, len(args))
	tail := make([]string, 0, len(args))
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if looksLikePath(a) {
			targets = append(targets, a)
		} else {
			tail = append(tail, a)
		}
	}
	return targets, strings.Join(tail, " ")
}

func looksLikePath(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	if strings.Contains(s, ":") && !strings.HasPrefix(s, "http") {
		return true // PATH:LINE form
	}
	if ext := filepath.Ext(s); ext != "" {
		return true
	}
	return false
}

func joinFileMarkers(targets []string) string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, fileMarker(t))
	}
	return strings.Join(out, " ")
}

// runAnalyzeSlash executes /analyze or /scan and returns a compact transcript
// entry. Both paths go through engine.AnalyzeWithOptions so results stay
// consistent with the CLI surface.
func (m Model) runAnalyzeSlash(args []string, securityOnly bool) Model {
	if m.eng == nil {
		return m.appendSystemMessage("Engine unavailable — cannot analyze.")
	}
	path := ""
	for _, a := range args {
		if a = strings.TrimSpace(a); a != "" && !strings.HasPrefix(a, "-") {
			path = a
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opts := engine.AnalyzeOptions{Path: path}
	if securityOnly {
		opts.Security = true
	} else {
		opts.Full = true
	}
	report, err := m.eng.AnalyzeWithOptions(ctx, opts)
	if err != nil {
		return m.appendSystemMessage("Analyze failed: " + err.Error())
	}
	if securityOnly {
		return m.appendSystemMessage(formatSecurityReport(report))
	}
	return m.appendSystemMessage(formatAnalyzeReport(report))
}

func formatAnalyzeReport(r engine.AnalyzeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Analyze: %d files, %d nodes, %d edges, %d cycles\n",
		r.Files, r.Nodes, r.Edges, r.Cycles)
	if len(r.HotSpots) > 0 {
		b.WriteString("Hotspots:\n")
		for i, h := range r.HotSpots {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  %d. %s (%s)\n", i+1, h.Name, h.Kind)
		}
	}
	if r.Security != nil && (len(r.Security.Secrets)+len(r.Security.Vulnerabilities)) > 0 {
		fmt.Fprintf(&b, "Security: %d secrets, %d vulns (scanned %d files)\n",
			len(r.Security.Secrets), len(r.Security.Vulnerabilities), r.Security.FilesScanned)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatSecurityReport(r engine.AnalyzeReport) string {
	if r.Security == nil {
		return "Scan produced no security report."
	}
	sec := r.Security
	var b strings.Builder
	fmt.Fprintf(&b, "Scan: %d files scanned\n", sec.FilesScanned)
	fmt.Fprintf(&b, "  Secrets: %d\n", len(sec.Secrets))
	for i, f := range sec.Secrets {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "    [%s] %s:%d %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Pattern)
	}
	fmt.Fprintf(&b, "  Vulnerabilities: %d\n", len(sec.Vulnerabilities))
	for i, f := range sec.Vulnerabilities {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "    [%s] %s:%d %s (%s)\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Kind, f.CWE)
	}
	return strings.TrimRight(b.String(), "\n")
}

// codemapSummary renders a one-paragraph snapshot of the codemap graph.
func (m Model) codemapSummary() string {
	if m.eng == nil || m.eng.CodeMap == nil || m.eng.CodeMap.Graph() == nil {
		return "Codemap not built yet. Run /analyze or restart with -v."
	}
	g := m.eng.CodeMap.Graph()
	nodes := g.Nodes()
	edges := g.Edges()
	return fmt.Sprintf("Codemap: %d nodes, %d edges. Use `dfmc map --format svg --out map.svg` for a visual.",
		len(nodes), len(edges))
}

// versionSummary composes a short runtime readout for /version.
func (m Model) versionSummary() string {
	st := m.eng.Status()
	return fmt.Sprintf("DFMC (Go %s, %s/%s)\nProvider: %s / %s\nAST backend: %s",
		runtime.Version(), runtime.GOOS, runtime.GOARCH,
		blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"),
		blankFallback(st.ASTBackend, "unknown"))
}

// magicDocSlash handles /magicdoc show (read file) and /magicdoc update
// (delegates to CLI for now — implementation lives in ui/cli).
func (m Model) magicDocSlash(args []string) string {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	root := ""
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.Status().ProjectRoot)
	}
	if root == "" {
		return "Project root unknown — run /reload after opening a project."
	}
	path := filepath.Join(root, ".dfmc", "magic", "MAGIC_DOC.md")
	switch sub {
	case "", "show", "cat":
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "MAGIC_DOC.md not found. Generate it with: dfmc magicdoc update"
			}
			return "magicdoc read failed: " + err.Error()
		}
		return "MAGIC_DOC (" + filepath.ToSlash(path) + "):\n" + truncateCommandBlock(string(data), 4000)
	case "update", "sync", "generate":
		return "Run from CLI for now: `dfmc magicdoc update`. TUI in-place update is planned."
	default:
		return "magicdoc: unknown subcommand. Try: show | update"
	}
}

// conversationSlash exposes the branch/history surface through chat.
func (m Model) conversationSlash(args []string) string {
	if m.eng == nil {
		return "Engine unavailable."
	}
	sub := "active"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := args
	if len(args) > 0 {
		rest = args[1:]
	}
	switch sub {
	case "list":
		items, err := m.eng.ConversationList()
		if err != nil {
			return "conversation list: " + err.Error()
		}
		if len(items) == 0 {
			return "No saved conversations."
		}
		var b strings.Builder
		b.WriteString("Conversations:\n")
		for i, item := range items {
			if i >= 20 {
				fmt.Fprintf(&b, "  +%d more\n", len(items)-i)
				break
			}
			fmt.Fprintf(&b, "  %s (%d msgs)\n", item.ID, item.MessageN)
		}
		return strings.TrimRight(b.String(), "\n")
	case "active":
		active := m.eng.ConversationActive()
		if active == nil {
			return "No active conversation."
		}
		return fmt.Sprintf("Active: %s — %d messages, branch %q",
			active.ID, len(active.Messages()), blankFallback(active.Branch, "main"))
	case "new":
		c := m.eng.ConversationStart()
		if c == nil {
			return "Failed to start a new conversation."
		}
		return "Started new conversation: " + c.ID
	case "save":
		if err := m.eng.ConversationSave(); err != nil {
			return "save failed: " + err.Error()
		}
		return "Conversation saved."
	case "load":
		if len(rest) == 0 {
			return "Usage: /conversation load <id>"
		}
		c, err := m.eng.ConversationLoad(strings.TrimSpace(rest[0]))
		if err != nil {
			return "load failed: " + err.Error()
		}
		return fmt.Sprintf("Loaded %s (%d messages).", c.ID, len(c.Messages()))
	case "undo":
		n, err := m.eng.ConversationUndoLast()
		if err != nil {
			return "undo failed: " + err.Error()
		}
		return fmt.Sprintf("Undid %d assistant message(s).", n)
	case "search":
		query := strings.TrimSpace(strings.Join(rest, " "))
		if query == "" {
			return "Usage: /conversation search <query>"
		}
		items, err := m.eng.ConversationSearch(query, 15)
		if err != nil {
			return "search failed: " + err.Error()
		}
		if len(items) == 0 {
			return "No matching conversations."
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Matches (%d):\n", len(items))
		for _, item := range items {
			fmt.Fprintf(&b, "  %s (%d msgs)\n", item.ID, item.MessageN)
		}
		return strings.TrimRight(b.String(), "\n")
	case "branch":
		return conversationBranchSlash(m, rest)
	default:
		return "conversation: unknown subcommand. Try: list | active | new | save | load <id> | undo | search <q> | branch <sub>"
	}
}

func conversationBranchSlash(m Model, args []string) string {
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := args
	if len(args) > 0 {
		rest = args[1:]
	}
	switch sub {
	case "list":
		branches := m.eng.ConversationBranchList()
		if len(branches) == 0 {
			return "No branches."
		}
		sort.Strings(branches)
		return "Branches: " + strings.Join(branches, ", ")
	case "create", "new":
		if len(rest) == 0 {
			return "Usage: /conversation branch create <name>"
		}
		name := strings.TrimSpace(rest[0])
		if err := m.eng.ConversationBranchCreate(name); err != nil {
			return "branch create failed: " + err.Error()
		}
		return "Created branch: " + name
	case "switch", "use":
		if len(rest) == 0 {
			return "Usage: /conversation branch switch <name>"
		}
		name := strings.TrimSpace(rest[0])
		if err := m.eng.ConversationBranchSwitch(name); err != nil {
			return "branch switch failed: " + err.Error()
		}
		return "Switched to branch: " + name
	default:
		return "branch: unknown sub. Try: list | create <name> | switch <name>"
	}
}

// memorySlash exposes the three-tier memory store for chat-level inspection.
func (m Model) memorySlash(args []string) string {
	if m.eng == nil {
		return "Engine unavailable."
	}
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := args
	if len(args) > 0 {
		rest = args[1:]
	}
	tier := parseMemoryTier(rest)
	switch sub {
	case "list":
		entries, err := m.eng.MemoryList(tier, 20)
		if err != nil {
			return "memory list: " + err.Error()
		}
		if len(entries) == 0 {
			return fmt.Sprintf("No %s memory entries.", tierLabel(tier))
		}
		return formatMemoryEntries(entries, tier)
	case "search":
		query := strings.TrimSpace(strings.Join(rest, " "))
		if query == "" {
			return "Usage: /memory search <query>"
		}
		entries, err := m.eng.MemorySearch(query, tier, 20)
		if err != nil {
			return "memory search: " + err.Error()
		}
		if len(entries) == 0 {
			return "No matches."
		}
		return formatMemoryEntries(entries, tier)
	case "add":
		if len(rest) < 2 {
			return "Usage: /memory add <key> <value...>"
		}
		key := strings.TrimSpace(rest[0])
		value := strings.TrimSpace(strings.Join(rest[1:], " "))
		entry := types.MemoryEntry{
			Tier:       types.MemoryWorking,
			Key:        key,
			Value:      value,
			Confidence: 1.0,
		}
		if err := m.eng.MemoryAdd(entry); err != nil {
			return "memory add: " + err.Error()
		}
		return "Added to working memory."
	case "clear":
		if err := m.eng.MemoryClear(tier); err != nil {
			return "memory clear: " + err.Error()
		}
		return fmt.Sprintf("Cleared %s memory.", tierLabel(tier))
	default:
		return "memory: unknown subcommand. Try: list [tier] | search <q> [tier] | add <k> <v> | clear [tier]"
	}
}

func parseMemoryTier(args []string) types.MemoryTier {
	for _, a := range args {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "working", "work", "w":
			return types.MemoryWorking
		case "episodic", "episode", "ep", "e":
			return types.MemoryEpisodic
		case "semantic", "sem", "s":
			return types.MemorySemantic
		}
	}
	return ""
}

func tierLabel(t types.MemoryTier) string {
	if t == "" {
		return "all-tier"
	}
	return string(t)
}

func formatMemoryEntries(entries []types.MemoryEntry, tier types.MemoryTier) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Memory (%s, %d):\n", tierLabel(tier), len(entries))
	for i, e := range entries {
		if i >= 15 {
			fmt.Fprintf(&b, "  +%d more\n", len(entries)-i)
			break
		}
		fmt.Fprintf(&b, "  [%s] %s = %s\n", e.Tier, e.Key, truncateSingleLine(e.Value, 80))
	}
	return strings.TrimRight(b.String(), "\n")
}

// suggestSlashCommand picks the closest canonical slash command for an
// unknown token. Prefix match first, then containment — returns "/name" form
// or empty string when nothing is reasonably close.
func suggestSlashCommand(token string) string {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return ""
	}
	reg := commands.DefaultRegistry()
	// Canonical names + aliases from the registry.
	candidates := make([]string, 0, 32)
	for _, cmd := range reg.ForSurface(commands.SurfaceTUI) {
		candidates = append(candidates, cmd.Name)
		candidates = append(candidates, cmd.Aliases...)
	}
	// TUI-only slash utilities.
	candidates = append(candidates,
		"help", "status", "reload", "context", "tools", "tool", "ls", "read",
		"grep", "run", "diff", "patch", "undo", "apply", "providers", "provider",
		"models", "model",
	)
	// Dedup + lowercase.
	seen := map[string]struct{}{}
	norm := candidates[:0]
	for _, c := range candidates {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		norm = append(norm, c)
	}
	// Prefix match wins.
	for _, c := range norm {
		if strings.HasPrefix(c, token) {
			return "/" + c
		}
	}
	for _, c := range norm {
		if strings.Contains(c, token) {
			return "/" + c
		}
	}
	return ""
}

// promptSlash exposes the merged prompt-template catalog (embedded
// defaults + ~/.dfmc/prompts + .dfmc/prompts) in chat. Supports:
//
//	/prompt                      → first page of templates (alias: /prompt list)
//	/prompt list [query]         → all templates, optionally filtered by substring
//	/prompt show <id>            → full body of a single template
//	/prompt recommend [question] → engine recommendation for a task
//	/prompt render <id>          → render the template with sample vars
//
// Runs purely off the local catalog — no provider call — so it's safe
// on the offline/placeholder router.
func (m Model) promptSlash(args []string) string {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}
	lib := promptlib.New()
	if m.eng != nil {
		// LoadOverrides is a no-op when the project root is blank; safe
		// to call in degraded-startup.
		_ = lib.LoadOverrides(m.eng.ProjectRoot)
	}
	templates := lib.List()

	switch sub {
	case "", "list", "ls":
		query := strings.TrimSpace(strings.Join(rest, " "))
		return formatPromptSlashList(templates, query)
	case "show", "cat", "body":
		if len(rest) == 0 {
			return "Usage: /prompt show <id> — pass a template id from /prompt list."
		}
		target := strings.TrimSpace(rest[0])
		for _, t := range templates {
			if strings.EqualFold(strings.TrimSpace(t.ID), target) {
				return formatPromptSlashShow(t)
			}
		}
		return fmt.Sprintf("No prompt template with id %q. Run /prompt list to see available ids.", target)
	case "recommend", "suggest":
		if m.eng == nil {
			return "Engine unavailable."
		}
		question := strings.TrimSpace(strings.Join(rest, " "))
		if question == "" {
			question = "Summarise the project"
		}
		rec := m.eng.PromptRecommendation(question)
		return fmt.Sprintf("Prompt recommendation for %q:\n  profile:    %s\n  role:       %s\n  task:       %s\n  budget:     %d tokens\n  tool_style: %s",
			question,
			nonEmpty(rec.Profile, "-"),
			nonEmpty(rec.Role, "-"),
			nonEmpty(rec.Task, "-"),
			rec.PromptBudgetTokens,
			nonEmpty(rec.ToolStyle, "-"),
		)
	case "render":
		if len(rest) == 0 {
			return "Usage: /prompt render <id> [query...] — renders the template with the sample query."
		}
		target := strings.TrimSpace(rest[0])
		var picked *promptlib.Template
		for i := range templates {
			if strings.EqualFold(strings.TrimSpace(templates[i].ID), target) {
				picked = &templates[i]
				break
			}
		}
		if picked == nil {
			return fmt.Sprintf("No prompt template with id %q.", target)
		}
		query := strings.TrimSpace(strings.Join(rest[1:], " "))
		if query == "" {
			query = "Example query for template preview."
		}
		req := promptlib.RenderRequest{
			Task:     picked.Task,
			Role:     picked.Role,
			Language: picked.Language,
			Profile:  picked.Profile,
			Vars:     map[string]string{"query": query, "task": picked.Task},
		}
		body := lib.Render(req)
		if strings.TrimSpace(body) == "" {
			body = "(empty render — template has no body after overlay composition)"
		}
		return fmt.Sprintf("Render of %s:\n%s", picked.ID, truncateCommandBlock(body, 4000))
	default:
		return "prompt: unknown subcommand. Try: list [query] | show <id> | recommend [question] | render <id> [query]"
	}
}

func formatPromptSlashList(templates []promptlib.Template, query string) string {
	filtered := filteredPrompts(templates, query)
	if len(filtered) == 0 {
		if query == "" {
			return "No prompt templates registered."
		}
		return fmt.Sprintf("No prompt templates match %q.", query)
	}
	var b strings.Builder
	if query == "" {
		fmt.Fprintf(&b, "Prompt templates (%d):\n", len(filtered))
	} else {
		fmt.Fprintf(&b, "Prompt templates matching %q (%d):\n", query, len(filtered))
	}
	for i, t := range filtered {
		if i >= 30 {
			fmt.Fprintf(&b, "  +%d more — narrow with /prompt list <query>\n", len(filtered)-i)
			break
		}
		axes := []string{}
		if t.Task != "" {
			axes = append(axes, "task="+t.Task)
		}
		if t.Role != "" {
			axes = append(axes, "role="+t.Role)
		}
		if t.Language != "" {
			axes = append(axes, "lang="+t.Language)
		}
		if t.Profile != "" {
			axes = append(axes, "profile="+t.Profile)
		}
		suffix := ""
		if len(axes) > 0 {
			suffix = "  (" + strings.Join(axes, " ") + ")"
		}
		fmt.Fprintf(&b, "  %s%s\n", t.ID, suffix)
	}
	b.WriteString("Show one with: /prompt show <id>")
	return strings.TrimRight(b.String(), "\n")
}

func formatPromptSlashShow(t promptlib.Template) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ %s", t.ID)
	if t.Type != "" {
		fmt.Fprintf(&b, "  [%s]", t.Type)
	}
	b.WriteString("\n")
	if t.Description != "" {
		fmt.Fprintf(&b, "  description: %s\n", t.Description)
	}
	if t.Task != "" {
		fmt.Fprintf(&b, "  task:        %s\n", t.Task)
	}
	if t.Role != "" {
		fmt.Fprintf(&b, "  role:        %s\n", t.Role)
	}
	if t.Language != "" {
		fmt.Fprintf(&b, "  language:    %s\n", t.Language)
	}
	if t.Profile != "" {
		fmt.Fprintf(&b, "  profile:     %s\n", t.Profile)
	}
	if t.Compose != "" {
		fmt.Fprintf(&b, "  compose:     %s\n", t.Compose)
	}
	if t.Priority != 0 {
		fmt.Fprintf(&b, "  priority:    %d\n", t.Priority)
	}
	b.WriteString("  body:\n")
	b.WriteString(truncateCommandBlock(t.Body, 4000))
	return strings.TrimRight(b.String(), "\n")
}

// skillSlash lists and describes skills (built-in template-family verbs
// plus YAML files in .dfmc/skills and ~/.dfmc/skills). Mirrors the
// /api/v1/skills surface so the TUI carries its own view. Supports:
//
//	/skill                → list (alias: /skill list)
//	/skill show <name>    → inline body for YAML skills
//	/skill run <name>     → pointer to the template-slash that runs it
func (m Model) skillSlash(args []string) string {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}
	skills := collectSkills(m.projectRoot())
	switch sub {
	case "", "list", "ls":
		return formatSkillsList(skills)
	case "show", "cat":
		if len(rest) == 0 {
			return "Usage: /skill show <name>"
		}
		name := strings.TrimSpace(rest[0])
		for _, s := range skills {
			if strings.EqualFold(s.Name, name) {
				return formatSkillsShow(s)
			}
		}
		return fmt.Sprintf("No skill with name %q. Run /skill list.", name)
	case "run", "call":
		if len(rest) == 0 {
			return "Usage: /skill run <name> [args...]. For builtin skills prefer the dedicated slash (/review, /explain, /refactor, /test, /doc)."
		}
		name := strings.TrimSpace(rest[0])
		lower := strings.ToLower(name)
		switch lower {
		case "review", "explain", "refactor", "test", "doc":
			return fmt.Sprintf("Run the builtin skill directly: /%s %s", lower, strings.Join(rest[1:], " "))
		}
		return "YAML skill execution is CLI-only for now: dfmc skill run " + name + " " + strings.Join(rest[1:], " ")
	default:
		return "skill: unknown subcommand. Try: list | show <name> | run <name>"
	}
}

// skillEntry captures one skill row — either a builtin template-family
// verb or a YAML file discovered under .dfmc/skills / ~/.dfmc/skills.
type skillEntry struct {
	Name    string
	Source  string // "builtin" / "project" / "global"
	Path    string // "" for builtin
	Summary string
}

func collectSkills(projectRoot string) []skillEntry {
	builtins := []skillEntry{
		{Name: "review", Source: "builtin", Summary: "Review a target for bugs, smells, and hazards."},
		{Name: "explain", Source: "builtin", Summary: "Explain what a target does and why."},
		{Name: "refactor", Source: "builtin", Summary: "Propose a scoped, reversible refactor."},
		{Name: "test", Source: "builtin", Summary: "Draft tests for a target."},
		{Name: "doc", Source: "builtin", Summary: "Draft or update documentation."},
	}
	out := make([]skillEntry, 0, len(builtins)+8)
	out = append(out, builtins...)
	seen := map[string]struct{}{}
	for _, b := range builtins {
		seen[strings.ToLower(b.Name)] = struct{}{}
	}
	roots := []struct {
		path   string
		source string
	}{
		{path: filepath.Join(projectRoot, ".dfmc", "skills"), source: "project"},
		{path: filepath.Join(config.UserConfigDir(), "skills"), source: "global"},
	}
	for _, root := range roots {
		if strings.TrimSpace(root.path) == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root.path, "*.y*ml"))
		for _, p := range matches {
			name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, skillEntry{Name: name, Source: root.source, Path: p})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Builtins first, then by name.
		if (out[i].Source == "builtin") != (out[j].Source == "builtin") {
			return out[i].Source == "builtin"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func formatSkillsList(skills []skillEntry) string {
	if len(skills) == 0 {
		return "No skills found. Place YAML files in .dfmc/skills/ or ~/.dfmc/skills/."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (%d):\n", len(skills))
	for _, s := range skills {
		if s.Summary != "" {
			fmt.Fprintf(&b, "  %-12s [%s]  %s\n", s.Name, s.Source, s.Summary)
		} else {
			fmt.Fprintf(&b, "  %-12s [%s]\n", s.Name, s.Source)
		}
	}
	b.WriteString("Show body: /skill show <name>  ·  Run builtin: /review /explain /refactor /test /doc")
	return strings.TrimRight(b.String(), "\n")
}

func formatSkillsShow(s skillEntry) string {
	if s.Source == "builtin" {
		return fmt.Sprintf("▸ %s (builtin)\n  %s\n  Run it with: /%s <target>", s.Name, s.Summary, s.Name)
	}
	if s.Path == "" {
		return fmt.Sprintf("▸ %s [%s] — no path on disk", s.Name, s.Source)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return fmt.Sprintf("▸ %s [%s]\n  path: %s\n  read error: %v", s.Name, s.Source, s.Path, err)
	}
	return fmt.Sprintf("▸ %s [%s]\n  path: %s\n  body:\n%s",
		s.Name, s.Source, filepath.ToSlash(s.Path), truncateCommandBlock(string(data), 4000))
}
