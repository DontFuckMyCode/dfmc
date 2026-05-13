package theme

import "strings"

// ContextPayloadFromStats returns the canonical next-request context payload
// snapshot. If the caller already populated ContextPayload it is normalized
// and returned; otherwise the legacy StatsPanelInfo fields are folded into the
// same shape. Renderers should use this function instead of open-coding
// context-window arithmetic.
func ContextPayloadFromStats(info StatsPanelInfo) ContextPayloadSnapshot {
	p := info.ContextPayload
	if !contextPayloadEmpty(p) {
		return normalizeContextPayload(p)
	}
	provider := strings.TrimSpace(info.ContextProvider)
	if provider == "" {
		provider = strings.TrimSpace(info.Provider)
	}
	model := strings.TrimSpace(info.ContextModel)
	if model == "" {
		model = strings.TrimSpace(info.Model)
	}
	windowTokens := info.ContextWindowTokens
	if windowTokens <= 0 {
		windowTokens = info.ContextSystemTokens + info.ContextHistoryTokens + info.ContextTokens
	}
	if windowTokens <= 0 {
		windowTokens = info.ContextTokens
	}
	p = ContextPayloadSnapshot{
		Provider:             provider,
		Model:                model,
		LimitSource:          strings.TrimSpace(info.ContextLimitSource),
		MaxContext:           info.MaxContext,
		WindowTokens:         windowTokens,
		EvidenceTokens:       info.ContextTokens,
		EvidenceBudgetTokens: info.ContextBudgetTokens,
		SystemTokens:         info.ContextSystemTokens,
		MessageTokens:        info.ContextHistoryTokens,
		ResponseReserve:      info.ContextResponseTokens,
		ToolReserve:          info.ContextToolTokens,
		HistoryReserve:       info.ContextHistoryReserve,
		MessageCount:         info.ContextMessageCount,
		ToolCallCount:        info.ContextToolCallCount,
		WorkspaceEvidenceOff: contextReasonContains(info.ContextReasons, "conversation history only"),
	}
	return normalizeContextPayload(p)
}

func contextPayloadEmpty(p ContextPayloadSnapshot) bool {
	return strings.TrimSpace(p.Provider) == "" &&
		strings.TrimSpace(p.Model) == "" &&
		strings.TrimSpace(p.LimitSource) == "" &&
		p.MaxContext == 0 &&
		p.WindowTokens == 0 &&
		p.EvidenceTokens == 0 &&
		p.EvidenceBudgetTokens == 0 &&
		p.SystemTokens == 0 &&
		p.MessageTokens == 0 &&
		p.ResponseReserve == 0 &&
		p.ToolReserve == 0 &&
		p.HistoryReserve == 0 &&
		p.MessageCount == 0 &&
		p.ToolCallCount == 0 &&
		!p.WorkspaceEvidenceOff
}

func normalizeContextPayload(p ContextPayloadSnapshot) ContextPayloadSnapshot {
	p.Provider = strings.TrimSpace(p.Provider)
	p.Model = strings.TrimSpace(p.Model)
	p.LimitSource = strings.TrimSpace(p.LimitSource)
	if p.FreeTokens == 0 && p.MaxContext > 0 && p.WindowTokens > 0 {
		p.FreeTokens = p.MaxContext - p.WindowTokens
	}
	return p
}

func (p ContextPayloadSnapshot) Identity() string {
	switch {
	case p.Provider != "" && p.Model != "":
		return p.Provider + " / " + p.Model
	case p.Provider != "":
		return p.Provider
	default:
		return p.Model
	}
}
