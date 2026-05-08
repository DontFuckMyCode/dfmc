// Approval-gate + hooks-dispatcher summarisers used by the admin
// commands. Companion siblings:
//
//   - cli_admin.go        runVersion / runStatus / runInit / initNextSteps
//   - cli_admin_format.go provider profile, models.dev cache, AST, and
//                         codemap metric formatters
//
// The two summary structs each have a (collect → render) pair so the
// JSON payload and the human one-liner stay aligned. Active reflects
// whether the gate would actually stop anything today (non-empty list
// + registered approver). Tools is the raw configured list so
// operators can confirm exactly what is gated.

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// approvalGateSummary is the JSON-serialized shape returned by
// summarizeApprovalGate.
type approvalGateSummary struct {
	Active   bool     `json:"active"`
	Wildcard bool     `json:"wildcard"`
	Count    int      `json:"count"`
	Tools    []string `json:"tools,omitempty"`
}

func summarizeApprovalGate(eng *engine.Engine) approvalGateSummary {
	out := approvalGateSummary{}
	if eng == nil || eng.Config == nil {
		return out
	}
	raw := eng.Config.Tools.RequireApproval
	tools := make([]string, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			out.Wildcard = true
			continue
		}
		tools = append(tools, entry)
	}
	sort.Strings(tools)
	out.Tools = tools
	out.Count = len(tools)
	if out.Wildcard {
		out.Count = -1 // sentinel: every tool gated
	}
	out.Active = out.Wildcard || len(tools) > 0
	return out
}

func formatApprovalGateSummary(g approvalGateSummary) string {
	if !g.Active {
		return "off"
	}
	if g.Wildcard {
		return "on (*)"
	}
	if len(g.Tools) == 0 {
		return "on"
	}
	preview := g.Tools
	if len(preview) > 4 {
		preview = preview[:4]
		return fmt.Sprintf("on (%d: %s, …)", len(g.Tools), strings.Join(preview, ", "))
	}
	return fmt.Sprintf("on (%s)", strings.Join(preview, ", "))
}

// hooksSummary serializes the dispatcher inventory into a shape that is
// cheap to render in both JSON and human output. PerEvent maps event
// name → count so readers can see which lifecycle phases have hooks.
type hooksSummary struct {
	Total    int            `json:"total"`
	PerEvent map[string]int `json:"per_event,omitempty"`
}

func summarizeHooks(eng *engine.Engine) hooksSummary {
	out := hooksSummary{PerEvent: map[string]int{}}
	if eng == nil || eng.Hooks == nil {
		return out
	}
	inv := eng.Hooks.Inventory()
	for event, entries := range inv {
		key := strings.TrimSpace(string(event))
		if key == "" {
			continue
		}
		out.PerEvent[key] = len(entries)
		out.Total += len(entries)
	}
	return out
}

func formatHooksSummary(h hooksSummary) string {
	if h.Total == 0 {
		return "none registered"
	}
	events := make([]string, 0, len(h.PerEvent))
	for k := range h.PerEvent {
		events = append(events, k)
	}
	sort.Strings(events)
	parts := make([]string, 0, len(events))
	for _, e := range events {
		parts = append(parts, fmt.Sprintf("%s=%d", e, h.PerEvent[e]))
	}
	return fmt.Sprintf("%d (%s)", h.Total, strings.Join(parts, ", "))
}
