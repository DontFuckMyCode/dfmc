// In-chat summaries for /status and /context slash commands. These
// read-only Model methods format the current engine snapshot plus any
// ContextIn report into a transcript-friendly block. Extracted from
// tui.go.

package tui

import (
	"fmt"
	"strings"
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
