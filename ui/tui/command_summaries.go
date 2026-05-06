// In-chat summaries for /status and /context slash commands. These
// read-only Model methods format the current engine snapshot plus any
// ContextIn report into a transcript-friendly block. Extracted from
// tui.go.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m Model) statusCommandSummary() string {
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
	}
	parts := []string{
		fmt.Sprintf("State: %v", st.State),
		fmt.Sprintf("Provider/Model: %s / %s", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-")),
		fmt.Sprintf("Project: %s", blankFallback(st.ProjectRoot, "(none)")),
		fmt.Sprintf("AST: %s", blankFallback(st.ASTBackend, "-")),
	}
	if summary := formatProviderProfileSummaryTUI(st.ProviderProfile); summary != "" {
		parts = append(parts, "Profile: "+summary)
	}
	if summary := formatContextInSummaryTUI(st.ContextIn); summary != "" {
		parts = append(parts, "Context In: "+summary)
	}
	if why := formatContextInReasonSummaryTUI(st.ContextIn); why != "" {
		parts = append(parts, "Context Why: "+why)
	}
	return strings.Join(parts, "\n")
}

// historyConfigLine surfaces the conversation-memory knobs in the
// /context detailed summary so a user who just bumped MaxHistoryTokens
// or MaxHistoryMessages can confirm the value made it through. Without
// this line, the budget breakdown above shows the resolved per-request
// share but not the underlying ceiling — and the user is left
// guessing whether their config edit took effect.
func (m Model) historyConfigLine() string {
	if m.eng == nil || m.eng.Config == nil {
		return ""
	}
	tokensCfg := m.eng.Config.Context.MaxHistoryTokens
	msgsCfg := m.eng.Config.Context.MaxHistoryMessages
	tokensTxt := fmt.Sprintf("%d", tokensCfg)
	if tokensCfg <= 0 {
		tokensTxt = "auto"
	}
	msgsTxt := fmt.Sprintf("%d", msgsCfg)
	if msgsCfg <= 0 {
		msgsTxt = "auto"
	}
	storedUser, storedAssistant := 0, 0
	if active := m.eng.ConversationActive(); active != nil {
		for _, msg := range active.Messages() {
			switch msg.Role {
			case types.RoleUser:
				storedUser++
			case types.RoleAssistant:
				storedAssistant++
			}
		}
	}
	storedTotal := storedUser + storedAssistant
	return fmt.Sprintf("History config: max_tokens=%s max_messages=%s · stored=%d msgs (%dU/%dA)",
		tokensTxt, msgsTxt, storedTotal, storedUser, storedAssistant)
}

func (m Model) contextCommandSummary() string {
	recent := []string{}
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
		recent = append(recent, m.eng.MemoryWorking().RecentFiles...)
	}
	parts := []string{
		"Pinned: " + blankFallback(strings.TrimSpace(m.filesView.pinned), "(none)"),
	}
	if len(recent) == 0 {
		parts = append(parts, "Recent context files: (none)")
	} else {
		parts = append(parts, "Recent context files: "+strings.Join(recent, ", "))
	}
	if summary := formatContextInSummaryTUI(m.status.ContextIn); summary != "" {
		parts = append(parts, "Last Context In: "+summary)
	}
	if why := formatContextInReasonSummaryTUI(st.ContextIn); why != "" {
		parts = append(parts, "Why: "+why)
	}
	if files := formatContextInTopFilesTUI(st.ContextIn, 3); files != "" {
		parts = append(parts, "Top files: "+files)
	}
	return strings.Join(parts, "\n")
}

func (m Model) contextCommandWhySummary() string {
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
	}
	report := st.ContextIn
	parts := []string{"Context why report:"}
	if report == nil {
		parts = append(parts, "No context report available yet.")
		return strings.Join(parts, "\n")
	}
	if len(report.Reasons) == 0 {
		parts = append(parts, "No explicit context reasons were recorded.")
		return strings.Join(parts, "\n")
	}
	for i, reason := range report.Reasons {
		if i >= 8 {
			parts = append(parts, fmt.Sprintf("... %d more reason(s)", len(report.Reasons)-i))
			break
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d. %s", i+1, reason))
	}
	return strings.Join(parts, "\n")
}

func (m Model) contextCommandDetailedSummary() string {
	recent := []string{}
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
		recent = append(recent, m.eng.MemoryWorking().RecentFiles...)
	}
	report := st.ContextIn
	parts := []string{
		"Context report:",
		"Provider/Model: " + blankFallback(st.Provider, "-") + " / " + blankFallback(st.Model, "-"),
		"Pinned: " + blankFallback(strings.TrimSpace(m.filesView.pinned), "(none)"),
	}
	if len(recent) == 0 {
		parts = append(parts, "Recent context files: (none)")
	} else {
		parts = append(parts, "Recent context files: "+strings.Join(recent, ", "))
	}
	if report == nil {
		parts = append(parts, "No context build report available yet.")
		return strings.Join(parts, "\n")
	}
	parts = append(parts,
		"Summary: "+blankFallback(formatContextInSummaryTUI(report), "-"),
		fmt.Sprintf("Runtime cap: provider_ctx=%d available_ctx=%d", report.ProviderMaxContext, report.ContextAvailable),
		fmt.Sprintf("Flags: include_tests=%t include_docs=%t compression=%s", report.IncludeTests, report.IncludeDocs, blankFallback(strings.TrimSpace(report.Compression), "-")),
	)
	if m.eng != nil && strings.TrimSpace(report.Query) != "" {
		bd := m.eng.ContextBreakdown(report.Query)
		parts = append(parts,
			fmt.Sprintf("Budget breakdown: system=%d history=%d code=%d response=%d tools=%d available=%d",
				bd.SystemPrompt, bd.History, bd.ContextChunks, bd.Response, bd.ToolReserve, bd.Available),
			fmt.Sprintf("Budget percentages: system=%.1f%% history=%.1f%% code=%.1f%% response=%.1f%%",
				bd.SystemPromptPct*100, bd.HistoryPct*100, bd.ContextChunksPct*100, bd.ResponsePct*100),
		)
	} else {
		parts = append(parts,
			fmt.Sprintf("Budget breakdown: code=%d max_code=%d per_file=%d available=%d",
				report.TokenCount, report.MaxTokensTotal, report.MaxTokensPerFile, report.ContextAvailable),
		)
	}
	if line := m.historyConfigLine(); line != "" {
		parts = append(parts, line)
	}
	if why := formatContextInReasonSummaryTUI(report); why != "" {
		parts = append(parts, "Why summary: "+why)
	}
	details := formatContextInDetailedFileLinesTUI(report, 6)
	if len(details) == 0 {
		parts = append(parts, "File evidence: (none)")
	} else {
		parts = append(parts, "File evidence:")
		for _, line := range details {
			parts = append(parts, " - "+line)
		}
	}
	return strings.Join(parts, "\n")
}
