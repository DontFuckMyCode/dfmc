package tui

// describe_health.go — health/hooks/approval/intent chat cards: the
// read-only snapshots /doctor, /health, /hooks, /approve, /intent
// paste into the transcript.
//
// Split out of describe.go so the "is DFMC configured right, and what
// is gating the agent" surface lives next to its friends. Each
// function here is a pure read over Model + Engine state returning a
// single multi-line string suitable for appendSystemMessage.
//
// Workflow/stats describe helpers live in describe_workflow.go;
// transcript export + compaction stay in describe.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

// describeHealth renders a compact health snapshot: provider/model/AST
// readiness, tool surface, approval gate, hooks count, recent denials,
// memory store liveness. Intended as a "quick sanity check" the user
// runs from chat (/doctor or /health) without leaving the TUI. Full
// diagnostics still live in the `dfmc doctor` CLI (network, auto-fix).
func (m Model) describeHealth() string {
	lines := []string{"▸ DFMC health snapshot"}

	// Engine presence. If nil something is very wrong — but NewModel can
	// be passed nil in tests, so guard for it.
	if m.eng == nil {
		lines = append(lines, "  engine:   ✗ not initialized (no engine attached to model)")
		return strings.Join(lines, "\n")
	}

	// Provider profile. A misconfigured provider is the #1 reason users
	// report "agent isn't doing anything" — surface it first.
	provider := strings.TrimSpace(m.status.Provider)
	model := strings.TrimSpace(m.status.Model)
	var providerLine string
	switch {
	case provider == "":
		providerLine = "✗ no provider selected (run `dfmc config provider anthropic` or edit .dfmc/config.yaml)"
	case strings.EqualFold(provider, "offline") || strings.EqualFold(provider, "placeholder"):
		providerLine = fmt.Sprintf("◈ %s/%s — read-only (no mutating tool calls)", provider, blankFallback(model, "offline"))
	case !m.status.ProviderProfile.Configured:
		providerLine = fmt.Sprintf("⚠ %s/%s — profile not fully configured (missing API key or base_url?)", provider, blankFallback(model, "?"))
	default:
		providerLine = fmt.Sprintf("✓ %s/%s", provider, blankFallback(model, "?"))
	}
	lines = append(lines, "  provider: "+providerLine)

	// AST backend — regex is a warning because tree-sitter needs CGO.
	ast := strings.TrimSpace(m.status.ASTBackend)
	switch ast {
	case "":
		lines = append(lines, "  ast:      ⚠ backend unavailable")
	case "regex":
		lines = append(lines, "  ast:      ⚠ regex fallback (build with CGO_ENABLED=1 for tree-sitter)")
	default:
		lines = append(lines, "  ast:      ✓ "+ast)
	}

	// Tools surface.
	if m.eng.Tools == nil {
		lines = append(lines, "  tools:    ✗ engine.Tools is nil")
	} else {
		tools := m.eng.Tools.List()
		lines = append(lines, fmt.Sprintf("  tools:    ✓ %d registered", len(tools)))
	}

	// Memory store reachability. The degraded flag surfaces a failed
	// Memory.Load() from Init so the operator doesn't silently run on
	// an empty store when the bbolt file is corrupt or locked.
	switch {
	case m.eng.Memory == nil:
		lines = append(lines, "  memory:   ⚠ store not initialized")
	case m.status.MemoryDegraded:
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		lines = append(lines, "  memory:   ⚠ degraded — "+reason+" (running with empty store)")
	default:
		lines = append(lines, "  memory:   ✓ bbolt store open")
	}

	// Approval gate condensed to one line (/approve has the long form).
	gated := 0
	if m.eng.Config != nil {
		for _, s := range m.eng.Config.Tools.RequireApproval {
			if strings.TrimSpace(s) != "" {
				gated++
			}
		}
	}
	if gated == 0 {
		lines = append(lines, "  gate:     off — no tools require approval (/approve to learn more)")
	} else {
		lines = append(lines, fmt.Sprintf("  gate:     ON — %d tool(s) gated (/approve for details)", gated))
	}

	// Hooks count.
	hookTotal := 0
	for _, entries := range m.eng.Hooks.Inventory() {
		hookTotal += len(entries)
	}
	if hookTotal == 0 {
		lines = append(lines, "  hooks:    none registered (/hooks to see)")
	} else {
		lines = append(lines, fmt.Sprintf("  hooks:    %d registered (/hooks for details)", hookTotal))
	}

	// Recent denials — useful when user wonders why the agent "did
	// nothing" last turn.
	denials := m.eng.RecentDenials()
	if len(denials) > 0 {
		newest := denials[len(denials)-1]
		lines = append(lines, fmt.Sprintf("  denials:  %d this session — last: %s (%s ago)",
			len(denials), newest.Tool, time.Since(newest.At).Round(time.Second)))
	}

	// Project root — helps users verify DFMC is looking at the right tree.
	if root := strings.TrimSpace(m.projectRoot()); root != "" {
		lines = append(lines, "  project:  "+root)
	}

	return strings.Join(lines, "\n")
}

// describeHooks renders a snapshot of every lifecycle hook registered
// with the engine's dispatcher, grouped by event. Paired with /approve
// so the user can see the whole tool-lifecycle surface without digging
// through config.yaml. Returns a single multi-line string suitable for
// appendSystemMessage.
func (m Model) describeHooks() string {
	var dispatcher *hooks.Dispatcher
	if m.eng != nil {
		dispatcher = m.eng.Hooks
	}
	inventory := dispatcher.Inventory()
	lines := []string{"▸ Lifecycle hooks"}
	if len(inventory) == 0 {
		lines = append(lines,
			"  state:  none registered",
			"  enable: add entries under `hooks:` in .dfmc/config.yaml",
			"  events: user_prompt_submit, pre_tool, post_tool, session_start, session_end",
		)
		return strings.Join(lines, "\n")
	}
	// Render events in a stable order so repeated /hooks doesn't
	// reshuffle the output and confuse the reader.
	eventOrder := []hooks.Event{
		hooks.EventSessionStart,
		hooks.EventUserPromptSubmit,
		hooks.EventPreTool,
		hooks.EventPostTool,
		hooks.EventSessionEnd,
	}
	seen := make(map[hooks.Event]bool, len(eventOrder))
	for _, ev := range eventOrder {
		if entries, ok := inventory[ev]; ok {
			seen[ev] = true
			lines = append(lines, formatHookEvent(ev, entries)...)
		}
	}
	// Fold in any unknown events the dispatcher happened to register
	// (plugins, future additions) so nothing silently disappears.
	for ev, entries := range inventory {
		if seen[ev] {
			continue
		}
		lines = append(lines, formatHookEvent(ev, entries)...)
	}
	return strings.Join(lines, "\n")
}

// formatHookEvent emits a header line per event plus one line per hook.
// "cond=..." is only shown when the entry carries a condition expression
// — otherwise it adds noise.
func formatHookEvent(ev hooks.Event, entries []hooks.HookInventoryEntry) []string {
	out := make([]string, 0, 1+len(entries))
	out = append(out, fmt.Sprintf("  %s (%d)", ev, len(entries)))
	for _, h := range entries {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			name = "(unnamed)"
		}
		cmd := truncateSingleLine(h.Command, 80)
		if cond := strings.TrimSpace(h.Condition); cond != "" {
			out = append(out, fmt.Sprintf("    · %s [cond: %s] → %s", name, cond, cmd))
		} else {
			out = append(out, fmt.Sprintf("    · %s → %s", name, cmd))
		}
	}
	return out
}

// describeApprovalGate returns a human-readable snapshot of the current
// tool-approval configuration for the /approve slash command. Lists the
// gated tools, whether a TUI approver is wired, and whether a prompt
// is currently pending. Read-only: editing the gate is a config change,
// not a slash action.
func (m Model) describeApprovalGate() string {
	var gated []string
	if m.eng != nil && m.eng.Config != nil {
		for _, raw := range m.eng.Config.Tools.RequireApproval {
			if s := strings.TrimSpace(raw); s != "" {
				gated = append(gated, s)
			}
		}
	}
	lines := []string{"▸ Tool approval gate"}
	if len(gated) == 0 {
		lines = append(lines,
			"  state:    off — no tools require approval (tools.require_approval is empty)",
			"  enable:   add tool names to .dfmc/config.yaml under tools.require_approval (or '*' for every tool)",
			"  bypass:   user-initiated /tool calls are never gated",
		)
	} else {
		lines = append(lines,
			"  state:    ON",
			"  gated:    "+strings.Join(gated, ", "),
			"  bypass:   user-initiated /tool calls are never gated; only agent/subagent calls prompt",
		)
	}
	if m.pendingApproval != nil {
		lines = append(lines, fmt.Sprintf("  pending:  %s (source=%s) — press y/enter to approve, n/esc to deny", m.pendingApproval.Req.Tool, m.pendingApproval.Req.Source))
	} else {
		lines = append(lines, "  pending:  none")
	}
	if m.eng != nil {
		denials := m.eng.RecentDenials()
		if len(denials) == 0 {
			lines = append(lines, "  recent:   no denials this session")
		} else {
			lines = append(lines, fmt.Sprintf("  recent:   %d denial(s) — newest first", len(denials)))
			// Walk oldest-first storage in reverse so the newest denial
			// is the first line the user reads.
			for i := len(denials) - 1; i >= 0; i-- {
				d := denials[i]
				age := time.Since(d.At).Round(time.Second)
				lines = append(lines, fmt.Sprintf("    · %s (%s, %s ago) — %s", d.Tool, d.Source, age, d.Reason))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// describeLastIntent prints the most recent intent-router decision in a
// dense chat-system block. Surfaces the rewrite, the routing reason, the
// classifier latency, and (when populated) the follow-up question. When
// no decision has fired yet (fresh session, intent layer disabled, or
// only fallbacks so far), the block explains why so the user can tell
// "the layer ran" from "the layer is off".
func (m Model) describeLastIntent() string {
	lines := []string{"▸ Intent layer"}
	if m.intent.lastDecisionAtMs == 0 {
		lines = append(lines,
			"  state:    no decisions yet this session",
			"  hint:     send a message; the engine will publish intent:decision events on every submit",
			"  toggles:  /intent (verbose), /intent show (this view)",
		)
		return strings.Join(lines, "\n")
	}
	when := time.UnixMilli(m.intent.lastDecisionAtMs)
	age := time.Since(when).Round(time.Second)
	lines = append(lines,
		fmt.Sprintf("  intent:   %s (source=%s, %s ago, %dms)",
			defaultStr(m.intent.lastIntent, "?"),
			defaultStr(m.intent.lastSource, "?"),
			age, m.intent.lastLatencyMs),
	)
	if m.intent.lastRaw != "" {
		lines = append(lines, "  raw:      "+truncateSingleLine(m.intent.lastRaw, 120))
	}
	if m.intent.lastEnriched != "" && m.intent.lastEnriched != m.intent.lastRaw {
		lines = append(lines, "  enriched: "+truncateSingleLine(m.intent.lastEnriched, 200))
	}
	if m.intent.lastReasoning != "" {
		lines = append(lines, "  reason:   "+truncateSingleLine(m.intent.lastReasoning, 200))
	}
	if m.intent.lastFollowUp != "" {
		lines = append(lines, "  follow:   "+truncateSingleLine(m.intent.lastFollowUp, 200))
	}
	verbose := "off"
	if m.intent.verbose {
		verbose = "on (transcript shows raw → enriched pairs)"
	}
	lines = append(lines, "  verbose:  "+verbose+" — toggle with /intent")
	return strings.Join(lines, "\n")
}

func defaultStr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
