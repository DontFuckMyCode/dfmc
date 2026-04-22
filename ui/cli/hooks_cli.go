// `dfmc hooks` surfaces every lifecycle hook registered in the current
// config so operators can audit what runs around tool execution and
// session boundaries without opening the TUI. The view intentionally
// mirrors the TUI `/hooks` slash (describeHooks) so both surfaces agree
// on shape and wording.

package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

func runHooksCLI(eng *engine.Engine, args []string, jsonMode bool) int {
	// No subcommands today — the command is a read-only inspector.
	// Keeping the arg slice around means we can layer `dfmc hooks
	// reload` / `dfmc hooks add` later without breaking callers.
	if len(args) > 0 {
		sub := strings.TrimSpace(args[0])
		if sub != "list" {
			fmt.Fprintf(os.Stderr, "unknown hooks subcommand %q (try `dfmc hooks` to list)\n", sub)
			return 2
		}
	}

	inv := map[hooks.Event][]hooks.HookInventoryEntry{}
	if eng != nil && eng.Hooks != nil {
		inv = eng.Hooks.Inventory()
	}

	if jsonMode {
		payload := map[string]any{
			"total":     inventoryTotal(inv),
			"per_event": inventoryToMap(inv),
		}
		mustPrintJSON(payload)
		return 0
	}

	if len(inv) == 0 {
		fmt.Println("hooks: none registered")
		return 0
	}

	events := make([]string, 0, len(inv))
	for e := range inv {
		events = append(events, string(e))
	}
	sort.Strings(events)

	for _, eventName := range events {
		entries := inv[hooks.Event(eventName)]
		if len(entries) == 0 {
			continue
		}
		fmt.Printf("%s (%d)\n", eventName, len(entries))
		for _, e := range entries {
			parts := []string{}
			if n := strings.TrimSpace(e.Name); n != "" {
				parts = append(parts, "name="+n)
			}
			if c := strings.TrimSpace(e.Command); c != "" {
				// Truncate overlong commands — operators need enough to
				// identify the hook, not the whole shell script.
				if len(c) > 80 {
					c = c[:77] + "..."
				}
				parts = append(parts, "cmd="+c)
			}
			if cond := strings.TrimSpace(e.Condition); cond != "" {
				parts = append(parts, "when="+cond)
			}
			if e.Timeout > 0 {
				parts = append(parts, fmt.Sprintf("timeout=%s", e.Timeout))
			}
			fmt.Printf("  - %s\n", strings.Join(parts, " · "))
		}
	}
	return 0
}

func inventoryTotal(inv map[hooks.Event][]hooks.HookInventoryEntry) int {
	total := 0
	for _, entries := range inv {
		total += len(entries)
	}
	return total
}

func inventoryToMap(inv map[hooks.Event][]hooks.HookInventoryEntry) map[string]any {
	out := map[string]any{}
	for event, entries := range inv {
		key := strings.TrimSpace(string(event))
		if key == "" {
			continue
		}
		list := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			list = append(list, map[string]any{
				"name":      e.Name,
				"command":   e.Command,
				"condition": e.Condition,
				"timeout":   e.Timeout.String(),
			})
		}
		out[key] = list
	}
	return out
}
