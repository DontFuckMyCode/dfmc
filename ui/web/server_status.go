// Status/diagnostic handlers for the web API. Extracted from server.go to
// keep the construction/wiring lean. handleStatus plus the two helper
// summarisers (approval gate, hooks) live here because they all describe
// posture/health — the same payload operators hit via `dfmc status`.

package web

import (
	"net/http"
	"sort"
	"strings"
)

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.engine.Status()

	// Gate and hooks mirror the CLI `dfmc status` payload so operators who
	// hit the HTTP surface see the same posture signals. We wrap Status
	// instead of embedding these fields on the engine.Status struct to
	// keep that contract stable for existing consumers.
	payload := map[string]any{
		"state":            st.State,
		"project_root":     st.ProjectRoot,
		"provider":         st.Provider,
		"model":            st.Model,
		"provider_profile": st.ProviderProfile,
		"models_dev_cache": st.ModelsDevCache,
		"context_in":       st.ContextIn,
		"ast_backend":      st.ASTBackend,
		"ast_reason":       st.ASTReason,
		"ast_languages":    st.ASTLanguages,
		"ast_metrics":      st.ASTMetrics,
		"codemap_metrics":  st.CodeMap,
		"approval_gate":    s.approvalGateSummary(),
		"hooks":            s.hooksSummary(),
		"recent_denials":   len(s.engine.RecentDenials()),
		"open_circuits":    st.OpenCircuits,
	}
	// Enrich with context breakdown when a query is not required
	// (the web UI always has a current query in context).
	if s.engine != nil {
		q := ""
		if st.ContextIn != nil {
			q = st.ContextIn.Query
		}
		if q != "" {
			payload["context_breakdown"] = s.engine.ContextBreakdown(q)
		}
		payload["conversation_memory"] = s.conversationMemorySummary(q)
	}
	writeJSON(w, http.StatusOK, payload)
}

type webApprovalGateSummary struct {
	Active   bool     `json:"active"`
	Wildcard bool     `json:"wildcard"`
	Count    int      `json:"count"`
	Tools    []string `json:"tools,omitempty"`
}

func (s *Server) approvalGateSummary() webApprovalGateSummary {
	out := webApprovalGateSummary{}
	if s.engine == nil || s.engine.Config == nil {
		return out
	}
	raw := s.engine.Config.Tools.RequireApproval
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
		out.Count = -1
	}
	out.Active = out.Wildcard || len(tools) > 0
	return out
}

// conversationMemorySummary mirrors the CLI `dfmc status`
// "conversation_memory" object so HTTP / remote consumers see the
// same stored-count + ceilings the user can read in chat. Without
// this field, the memory floor / message-count bumps users tweak
// in config are invisible from the API surface.
func (s *Server) conversationMemorySummary(query string) map[string]any {
	out := map[string]any{
		"stored_msgs":          0,
		"max_history_tokens":   0,
		"max_history_messages": 0,
	}
	if s.engine == nil {
		return out
	}
	if active := s.engine.ConversationActive(); active != nil {
		out["stored_msgs"] = len(active.Messages())
	}
	preview := s.engine.ContextBudgetPreview(query)
	out["max_history_tokens"] = preview.MaxHistoryTokens
	out["max_history_messages"] = preview.MaxHistoryMessages
	return out
}

type webHooksSummary struct {
	Total    int            `json:"total"`
	PerEvent map[string]int `json:"per_event,omitempty"`
}

func (s *Server) hooksSummary() webHooksSummary {
	out := webHooksSummary{PerEvent: map[string]int{}}
	if s.engine == nil || s.engine.Hooks == nil {
		return out
	}
	inv := s.engine.Hooks.Inventory()
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
