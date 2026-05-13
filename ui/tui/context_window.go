package tui

import "strings"

type liveContextSnapshot struct {
	ok             bool
	provider       string
	model          string
	maxContext     int
	codeTokens     int
	windowTokens   int
	available      int
	systemTokens   int
	historyTokens  int
	historyReserve int
	responseTokens int
	toolTokens     int
	task           string
	compression    string
	topFiles       []string
}

func (m Model) liveContextSnapshot() liveContextSnapshot {
	if m.eng == nil {
		return liveContextSnapshot{}
	}
	query := m.liveContextQuery()
	breakdown := m.eng.ContextBreakdown(query)

	maxContext := breakdown.MaxContext
	if maxContext <= 0 && m.status.ContextIn != nil {
		maxContext = m.status.ContextIn.ProviderMaxContext
	}
	if maxContext <= 0 {
		maxContext = m.status.ProviderProfile.MaxContext
	}

	codeTokens := breakdown.ContextChunks
	if codeTokens <= 0 && m.status.ContextIn != nil {
		codeTokens = m.status.ContextIn.TokenCount
	}
	queryTokens := estimatedChatTokens(query)
	// windowTokens = ACTUAL input footprint, NOT reserve sum. The old
	// formula added Response (16K) and Tool (0.5K) reserves — output
	// headroom that's never part of the input — plus History reserve
	// (24K) instead of HistoryActual, so a fresh empty session showed
	// ~42K "context dolu" while the real input was a few thousand tokens.
	// InputFootprint already covers system prompt + tools[] + actual
	// history + chunks; we only add the live query tokens because the
	// breakdown was built BEFORE the user pressed enter (the query is
	// the composer text, not yet in conversation).
	windowTokens := breakdown.InputFootprint + queryTokens
	if windowTokens <= 0 {
		windowTokens = breakdown.UsedTotal + queryTokens
	}
	if windowTokens <= 0 {
		windowTokens = codeTokens
	}

	available := 0
	if maxContext > 0 {
		available = maxContext - windowTokens
		if available < 0 {
			available = 0
		}
	}

	topFiles := make([]string, 0, 2)
	for _, file := range breakdown.FilesInContext {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		topFiles = append(topFiles, file)
		if len(topFiles) >= 2 {
			break
		}
	}

	return liveContextSnapshot{
		ok:             maxContext > 0 || windowTokens > 0 || codeTokens > 0,
		provider:       strings.TrimSpace(breakdown.Provider),
		model:          strings.TrimSpace(breakdown.Model),
		maxContext:     maxContext,
		codeTokens:     codeTokens,
		windowTokens:   windowTokens,
		available:      available,
		systemTokens:   breakdown.SystemPrompt,
		historyTokens:  breakdown.HistoryActual,
		historyReserve: breakdown.History,
		responseTokens: breakdown.Response,
		toolTokens:     breakdown.ToolReserve,
		task:           strings.TrimSpace(breakdown.Task),
		compression:    strings.TrimSpace(breakdown.Compression),
		topFiles:       topFiles,
	}
}

func (m Model) liveContextQuery() string {
	if input := strings.TrimSpace(m.composeInput()); input != "" {
		return input
	}
	if m.chat.sending {
		if last := strings.TrimSpace(m.latestUserTranscriptInput()); last != "" {
			return last
		}
	}
	if m.status.ContextIn != nil {
		return strings.TrimSpace(m.status.ContextIn.Query)
	}
	return ""
}

func (m Model) latestUserTranscriptInput() string {
	for i := len(m.chat.transcript) - 1; i >= 0; i-- {
		line := m.chat.transcript[i]
		if line.Role.Eq(chatRoleUser) && strings.TrimSpace(line.Content) != "" {
			return line.Content
		}
	}
	return ""
}
