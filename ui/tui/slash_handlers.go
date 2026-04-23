package tui

// slash_handlers.go — concrete implementations for the expanded slash-command
// surface (F1c / F1d / F1e). Each helper is self-contained: it takes the
// command's raw args, does the work (either composing a prompt to feed the
// chat pipeline or calling an engine method directly), and returns either a
// formatted string to append to the transcript or a (Model, tea.Cmd, bool)
// triple matching the switch's signature.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)


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


