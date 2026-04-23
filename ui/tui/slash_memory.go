// slash_memory.go — the /memory slash family. Exposes the engine's
// three-tier memory store (working / episodic / semantic) for chat-
// level inspection. Drives through Engine.Memory* passthroughs so the
// store stays the single source of truth.
//
//   - memorySlash: dispatcher (list | search | add | clear).
//   - parseMemoryTier: coerces a subcommand-tail token into a
//     types.MemoryTier (accepts short aliases like "work" / "ep" /
//     "sem"). Empty return means "all tiers".
//   - tierLabel: human-readable tier label for transcript lines.
//   - formatMemoryEntries: one-line-per-entry renderer capped at 15
//     rows with a "+N more" tail.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

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
