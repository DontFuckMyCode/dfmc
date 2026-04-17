// `dfmc approvals` surfaces the tool approval gate — which tools are
// gated, whether an approver is registered, and the recent denial log.
// Mirrors the TUI `/approve` slash (describeApprovalGate) so the CLI
// gives operators the same at-a-glance view without launching the TUI.

package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runApprovalsCLI(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) > 0 {
		sub := strings.TrimSpace(args[0])
		if sub != "show" && sub != "list" {
			fmt.Fprintf(os.Stderr, "unknown approvals subcommand %q (try `dfmc approvals`)\n", sub)
			return 2
		}
	}

	gate := summarizeApprovalGate(eng)
	denials := eng.RecentDenials()

	if jsonMode {
		payload := map[string]any{
			"gate":           gate,
			"recent_denials": denialsPayload(denials),
		}
		_ = printJSON(payload)
		return 0
	}

	fmt.Printf("approval gate: %s\n", formatApprovalGateSummary(gate))
	if !gate.Active {
		fmt.Println("  (no tools gated — agent-initiated writes run without prompting)")
	} else if gate.Wildcard {
		fmt.Println("  (wildcard * — every tool must be approved before running)")
	} else if len(gate.Tools) > 0 {
		// Full list, not just the preview: this surface is explicitly for
		// operators who want to audit.
		sorted := append([]string(nil), gate.Tools...)
		sort.Strings(sorted)
		fmt.Println("  tools:")
		for _, t := range sorted {
			fmt.Printf("    - %s\n", t)
		}
	}

	if len(denials) == 0 {
		fmt.Println("recent denials: none")
		return 0
	}
	fmt.Printf("recent denials (%d, newest last):\n", len(denials))
	for _, d := range denials {
		reason := strings.TrimSpace(d.Reason)
		if reason == "" {
			reason = "(no reason)"
		}
		src := strings.TrimSpace(d.Source)
		if src == "" {
			src = "unknown"
		}
		fmt.Printf("  - %s  %s via %s — %s\n",
			d.At.Format(time.RFC3339),
			d.Tool,
			src,
			reason,
		)
	}
	return 0
}

func denialsPayload(entries []engine.RecentDenial) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, d := range entries {
		out = append(out, map[string]any{
			"tool":   d.Tool,
			"source": d.Source,
			"reason": d.Reason,
			"at":     d.At.Format(time.RFC3339),
		})
	}
	return out
}
