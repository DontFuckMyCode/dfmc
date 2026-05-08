package tui

// engine_events_context.go — handlers for context:* events plus
// history:trimmed (which is part of the context lifecycle from the
// user's perspective). Covers chunk-build telemetry, errors, the
// auto-compact / proactive-compact / handoff transitions, and the
// LLM-requested cleanup/prune signal.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func registerContextEventHandlers(r *engineEventRegistry) {
	r.register("context:built", handleContextBuilt)
	r.register("context:error", handleContextError)
	r.register("context:lifecycle:compacted", handleContextLifecycleCompacted)
	r.register("context:lifecycle:proactive_compacted", handleContextLifecycleCompacted)
	r.register("context:lifecycle:handoff", handleContextLifecycleHandoff)
	r.register("context:cleanup", handleContextCleanup)
	r.register("history:trimmed", handleHistoryTrimmed)
}

func handleContextBuilt(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	files := payloadInt(payload, "files", 0)
	tokens := payloadInt(payload, "tokens", 0)
	budget := payloadInt(payload, "budget", 0)
	perFile := payloadInt(payload, "per_file", 0)
	task := payloadString(payload, "task", "general")
	comp := payloadString(payload, "compression", "-")
	maxCtx := m.status.ProviderProfile.MaxContext
	if maxCtx == 0 && m.status.ContextIn != nil {
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	prev := m.status.ContextIn
	query := ""
	available := 0
	maxFiles := files
	var contextFiles []engine.ContextInFileStatus
	if prev != nil {
		query = prev.Query
		available = prev.ContextAvailable
		maxFiles = prev.MaxFiles
		contextFiles = append([]engine.ContextInFileStatus(nil), prev.Files...)
	}
	if maxFiles <= 0 {
		maxFiles = files
	}
	m.status.ContextIn = &engine.ContextInStatus{
		Query:              query,
		Task:               task,
		TokenCount:         tokens,
		ProviderMaxContext: maxCtx,
		ContextAvailable:   available,
		MaxTokensTotal:     budget,
		MaxTokensPerFile:   perFile,
		Compression:        comp,
		FileCount:          files,
		MaxFiles:           maxFiles,
		Files:              contextFiles,
		Reasons:            payloadStringSlice(payload, "reasons"),
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "context:built",
		Kind:   "context",
		Status: "ok",
		Title:  "context",
		Detail: contextBuiltChatDetail(files, tokens, budget, perFile, task, comp),
	})
	return m, fmt.Sprintf("Context built: %d files, %d tokens (%s, %s)", files, tokens, task, comp)
}

func handleContextError(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	// Payload here is a plain string (err.Error()), not a map.
	errText := ""
	if s, ok := event.Payload.(string); ok {
		errText = strings.TrimSpace(s)
	}
	if errText == "" {
		errText = payloadString(payload, "error", "context build failed")
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "context:error",
		Kind:   "context",
		Status: "error",
		Title:  "context build failed",
		Detail: truncateSingleLine(errText, 160),
	})
	return m, "Context build failed (answering with reduced context): " + truncateSingleLine(errText, 140)
}

func handleContextLifecycleCompacted(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	before := payloadInt(payload, "before_tokens", 0)
	after := payloadInt(payload, "after_tokens", 0)
	collapsed := payloadInt(payload, "rounds_collapsed", 0)
	removed := payloadInt(payload, "messages_removed", 0)
	step := payloadInt(payload, "step", 0)
	reclaimed := max(before-after, 0) // defensive: compact never grows the buffer
	m.agentLoop.compactsThisTurn++
	m.agentLoop.compactReclaimedTurn += reclaimed
	title := "context compacted"
	if eventType == "context:lifecycle:proactive_compacted" {
		title = "context proactive compact"
	}
	preview := fmt.Sprintf("%d→%d tok · -%d reclaimed · %d rounds",
		before, after, reclaimed, collapsed)
	m.pushToolChip(toolChip{
		Name:    "auto-compact",
		Status:  "compact",
		Preview: preview,
	})
	if cap := m.agentLoop.liveLoopBudgetCap; cap > 0 && after > 0 {
		pctAfter := int((int64(after) * 100) / int64(cap))
		for _, band := range headroomThresholds {
			if pctAfter < band.pct {
				m.agentLoop.headroomThresholdsHit &^= band.bit
			}
		}
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    fmt.Sprintf("%s:%d", eventType, step),
		Kind:   "context",
		Status: "ok",
		Title:  title,
		Detail: contextLifecycleChatDetail(payload),
		Step:   step,
		Reason: "tool-loop history was summarized to keep provider context headroom",
	})
	line := ""
	if collapsed > 0 {
		line = fmt.Sprintf("Context auto-compacted: %d→%d tok · -%d reclaimed (%d rounds, %d msgs removed).",
			before, after, reclaimed, collapsed, removed)
	} else {
		line = fmt.Sprintf("Context auto-compacted: %d→%d tok · -%d reclaimed.",
			before, after, reclaimed)
	}
	return m, line
}

func handleContextLifecycleHandoff(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	historyTokens := payloadInt(payload, "history_tokens", 0)
	briefTokens := payloadInt(payload, "brief_tokens", 0)
	sealed := payloadInt(payload, "messages_sealed", 0)
	newConv := payloadString(payload, "new_conversation", "")
	preview := fmt.Sprintf("%d→%d tok · %d msgs sealed", historyTokens, briefTokens, sealed)
	m.pushToolChip(toolChip{
		Name:    "auto-handoff",
		Status:  "handoff",
		Preview: preview,
	})
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "context:handoff",
		Kind:   "context",
		Status: "ok",
		Title:  "context handoff",
		Detail: fmt.Sprintf("%s -> %s tok | %d msgs sealed", compactMetric(historyTokens), compactMetric(briefTokens), sealed),
	})
	line := ""
	if newConv != "" {
		line = fmt.Sprintf("Auto-new-session: rotated to %s (%d→%d tokens, %d msgs sealed).", newConv, historyTokens, briefTokens, sealed)
	} else {
		line = fmt.Sprintf("Auto-new-session: fresh conversation seeded (%d→%d tokens).", historyTokens, briefTokens)
	}
	return m, line
}

func handleContextCleanup(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	dropped := payloadInt(payload, "dropped", 0)
	requested := payloadInt(payload, "requested", 0)
	if dropped > 0 {
		return m, fmt.Sprintf("✂ Context pruned: dropped %d message(s) (model requested %d)", dropped, requested)
	}
	return m, ""
}

func handleHistoryTrimmed(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	keptMsgs := payloadInt(payload, "kept_messages", 0)
	keptTokens := payloadInt(payload, "kept_tokens", 0)
	omitted := payloadInt(payload, "omitted_messages", 0)
	summaryTokens := payloadInt(payload, "summary_tokens", 0)
	preview := strings.TrimSpace(payloadString(payload, "summary_preview", ""))
	detail := fmt.Sprintf("kept %d msgs (%s tok) · summarized %d older into %s tok",
		keptMsgs, compactMetric(keptTokens), omitted, compactMetric(summaryTokens))
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "history:trimmed",
		Kind:   "context",
		Status: "ok",
		Title:  "history trimmed",
		Detail: detail,
	})
	if preview != "" {
		return m, fmt.Sprintf("History trimmed: %s — %s", detail, truncateSingleLine(preview, 120))
	}
	return m, fmt.Sprintf("History trimmed: %s.", detail)
}
