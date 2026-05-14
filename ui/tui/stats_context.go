package tui

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

type statsContextSnapshot struct {
	Task             string
	FileCount        int
	MaxFiles         int
	BudgetTokens     int
	AvailableTokens  int
	MaxTokensPerFile int
	Compression      string
	Reasons          []string
	TopFiles         []string
	SystemTokens     int
	HistoryTokens    int
	HistoryReserve   int
	ResponseTokens   int
	ToolTokens       int
	WindowTokens     int
	EvidenceTokens   int
	MaxContext       int
	Provider         string
	Model            string
	LimitSource      string
	MessageCount     int
	ToolCallCount    int
	Payload          theme.ContextPayloadSnapshot
}

func (m Model) statsContextSnapshot(head chatHeaderInfo) statsContextSnapshot {
	snap := statsContextSnapshot{
		WindowTokens:   head.ContextWindowTokens,
		EvidenceTokens: head.ContextTokens,
		MaxContext:     head.MaxContext,
		Provider:       strings.TrimSpace(head.Provider),
		Model:          strings.TrimSpace(head.Model),
	}
	if report := m.status.ContextIn; report != nil {
		snap.Task = strings.TrimSpace(report.Task)
		snap.FileCount = report.FileCount
		snap.MaxFiles = report.MaxFiles
		snap.BudgetTokens = report.MaxTokensTotal
		snap.AvailableTokens = report.ContextAvailable
		snap.MaxTokensPerFile = report.MaxTokensPerFile
		snap.Compression = strings.TrimSpace(report.Compression)
		for _, reason := range report.Reasons {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				continue
			}
			snap.Reasons = append(snap.Reasons, reason)
			if len(snap.Reasons) >= 2 {
				break
			}
		}
		for _, file := range report.Files {
			path := strings.TrimSpace(file.Path)
			if path == "" {
				continue
			}
			snap.TopFiles = append(snap.TopFiles, path)
			if len(snap.TopFiles) >= 2 {
				break
			}
		}
	}
	if live := m.liveContextSnapshot(); live.ok {
		if live.codeTokens > 0 {
			snap.EvidenceTokens = live.codeTokens
		}
		if live.maxContext > 0 {
			snap.MaxContext = live.maxContext
		}
		snap.WindowTokens = live.windowTokens
		snap.AvailableTokens = live.available
		snap.SystemTokens = live.systemTokens
		snap.HistoryTokens = live.historyTokens
		snap.HistoryReserve = live.historyReserve
		snap.ResponseTokens = live.responseTokens
		snap.ToolTokens = live.toolTokens
		if live.provider != "" {
			snap.Provider = live.provider
		}
		if live.model != "" {
			snap.Model = live.model
		}
		if live.task != "" {
			snap.Task = live.task
		}
		if live.compression != "" {
			snap.Compression = live.compression
		}
		if len(live.topFiles) > 0 {
			snap.TopFiles = live.topFiles
		}
	}
	messageCount, toolCallCount, messageTokens := m.contextConversationStats()
	snap.MessageCount = messageCount
	snap.ToolCallCount = toolCallCount
	if snap.HistoryTokens <= 0 {
		snap.HistoryTokens = messageTokens
	}
	snap.LimitSource = m.contextWindowLimitSource(snap.Provider, snap.Model, snap.MaxContext)
	snap.Payload = theme.ContextPayloadSnapshot{
		Provider:             snap.Provider,
		Model:                snap.Model,
		LimitSource:          snap.LimitSource,
		MaxContext:           snap.MaxContext,
		WindowTokens:         snap.WindowTokens,
		EvidenceTokens:       snap.EvidenceTokens,
		EvidenceBudgetTokens: snap.BudgetTokens,
		SystemTokens:         snap.SystemTokens,
		MessageTokens:        snap.HistoryTokens,
		ResponseReserve:      snap.ResponseTokens,
		ToolReserve:          snap.ToolTokens,
		HistoryReserve:       snap.HistoryReserve,
		MessageCount:         snap.MessageCount,
		ToolCallCount:        snap.ToolCallCount,
		WorkspaceEvidenceOff: statsContextReasonContains(snap.Reasons, "conversation history only"),
	}
	if snap.Payload.MaxContext > 0 && snap.Payload.WindowTokens > 0 {
		snap.Payload.FreeTokens = snap.Payload.MaxContext - snap.Payload.WindowTokens
	}
	return snap
}

func statsContextReasonContains(reasons []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, reason := range reasons {
		if strings.Contains(strings.ToLower(reason), needle) {
			return true
		}
	}
	return false
}
