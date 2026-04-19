package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderStatusView(width int) string {
	inner := min(width, 80)
	divider := renderDivider(inner)
	group := func(icon, title string, rows []string) []string {
		out := []string{accentStyle.Bold(true).Render(icon) + " " + sectionTitleStyle.Render(strings.ToUpper(title))}
		for _, r := range rows {
			if strings.TrimSpace(r) == "" {
				continue
			}
			out = append(out, "  "+truncateForPanel(r, width-2))
		}
		return out
	}
	parts := []string{
		sectionHeader("◉", "Status"),
		subtleStyle.Render("r refresh · ctrl+h keys"),
		divider,
		"",
	}
	parts = append(parts, group("◉", "Project", []string{
		"Root:     " + blankFallback(m.status.ProjectRoot, "(none)"),
	})...)
	parts = append(parts, "")
	parts = append(parts, group("⎈", "Provider", []string{
		"Provider: " + blankFallback(m.status.Provider, "-") + " / " + blankFallback(m.status.Model, "-"),
		"Profile:  " + formatProviderProfileSummaryTUI(m.status.ProviderProfile),
		"Runtime:  " + providerConnectivityHintTUI(m.status),
		"Catalog:  " + formatModelsDevCacheSummaryTUI(m.status.ModelsDevCache),
	})...)
	parts = append(parts, "")
	parts = append(parts, group("≡", "AST", []string{
		"Backend:  " + blankFallback(m.status.ASTBackend, "-"),
		"Langs:    " + formatASTLanguageSummaryTUI(m.status.ASTLanguages),
		"Metrics:  " + formatASTMetricsSummaryTUI(m.status.ASTMetrics),
		"CodeMap:  " + formatCodeMapMetricsSummaryTUI(m.status.CodeMap),
	})...)
	if m.status.MemoryDegraded {
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		parts = append(parts, "", warnStyle.Render("⚠ memory degraded — "+reason+" (running with empty store)"))
	}
	if summary := formatContextInSummaryTUI(m.status.ContextIn); summary != "" {
		parts = append(parts, "")
		rows := []string{"Last:     " + summary}
		if why := formatContextInReasonSummaryTUI(m.status.ContextIn); why != "" {
			rows = append(rows, "Why:      "+why)
		}
		if files := formatContextInTopFilesTUI(m.status.ContextIn, 3); files != "" {
			rows = append(rows, "Top:      "+files)
		}
		if details := formatContextInDetailedFileLinesTUI(m.status.ContextIn, 2); len(details) > 0 {
			for _, line := range details {
				rows = append(rows, "File:     "+line)
			}
		}
		parts = append(parts, group("▦", "Context In", rows)...)
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		parts = append(parts, "", subtleStyle.Render(note))
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderFilesView(width int) string {
	return m.renderFilesViewSized(width, 24)
}

func (m Model) renderFilesViewSized(width, height int) string {
	listWidth := width / 3
	if listWidth < 28 {
		listWidth = 28
	}
	if listWidth > width-24 {
		listWidth = width / 2
	}
	previewWidth := width - listWidth - 3
	if previewWidth < 20 {
		previewWidth = 20
	}

	const listChrome = 6
	listRows := height - listChrome
	if listRows < 8 {
		listRows = 8
	}
	const previewChrome = 6
	previewRows := height - previewChrome
	if previewRows < 8 {
		previewRows = 8
	}

	listLines := []string{
		sectionHeader("▦", "Files"),
		subtleStyle.Render("j/k move · enter preview · r reload · p pin · i/e/v chat actions · ctrl+h keys"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(m.filesView.entries) == 0 {
		listLines = append(listLines,
			warnStyle.Render("No indexed project files yet."),
			"",
			subtleStyle.Render("Try one of these:"),
			subtleStyle.Render("  • switch to Chat and run ")+codeStyle.Render("/analyze"),
			subtleStyle.Render("  • press ")+codeStyle.Render("r")+subtleStyle.Render(" to refresh the file index"),
			subtleStyle.Render("  • confirm you launched ")+codeStyle.Render("dfmc")+subtleStyle.Render(" from a project root"),
		)
	} else {
		half := listRows / 2
		start := m.filesView.index - half
		if start < 0 {
			start = 0
		}
		end := start + listRows
		if end > len(m.filesView.entries) {
			end = len(m.filesView.entries)
			start = end - listRows
			if start < 0 {
				start = 0
			}
		}
		for i := start; i < end; i++ {
			prefix := "  "
			label := truncateSingleLine(m.filesView.entries[i], listWidth-4)
			if m.filesView.entries[i] == strings.TrimSpace(m.filesView.pinned) {
				label = "[p] " + label
			}
			if i == m.filesView.index {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			listLines = append(listLines, prefix+label)
		}
		listLines = append(listLines, "", subtleStyle.Render(fmt.Sprintf("%d/%d files", m.filesView.index+1, len(m.filesView.entries))))
	}

	previewLines := []string{
		sectionHeader("❐", "Preview"),
		subtleStyle.Render(blankFallback(m.filesView.path, "Select a file")),
		renderDivider(previewWidth - 2),
		"",
	}
	if strings.TrimSpace(m.filesView.path) != "" && m.filesView.path == strings.TrimSpace(m.filesView.pinned) {
		previewLines = append(previewLines, subtleStyle.Render("Pinned for chat context"), "")
	}
	content := truncateForPanelSized(m.filesView.preview, previewWidth, previewRows)
	if content == "" {
		content = subtleStyle.Render("No preview loaded.")
	}
	previewLines = append(previewLines, content)
	if m.filesView.size > 0 {
		previewLines = append(previewLines, "", subtleStyle.Render(fmt.Sprintf("size=%d bytes", m.filesView.size)))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(previewWidth).Render(strings.Join(previewLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) renderSetupView(width int) string {
	providers := m.availableProviders()
	m.setupWizard.index = clampIndex(m.setupWizard.index, len(providers))

	listWidth := width / 3
	if listWidth < 24 {
		listWidth = 24
	}
	if listWidth > width-24 {
		listWidth = width / 2
	}
	detailWidth := width - listWidth - 3
	if detailWidth < 20 {
		detailWidth = 20
	}

	listLines := []string{
		sectionHeader("⚙", "Setup"),
		subtleStyle.Render("enter apply · m edit model · s save · ctrl+h keys"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(providers) == 0 {
		listLines = append(listLines,
			warnStyle.Render("No providers configured."),
			"",
			subtleStyle.Render("Get online in under a minute:"),
			subtleStyle.Render("  • set ")+codeStyle.Render("ANTHROPIC_API_KEY")+subtleStyle.Render(", ")+codeStyle.Render("OPENAI_API_KEY")+subtleStyle.Render(", or ")+codeStyle.Render("DEEPSEEK_API_KEY"),
			subtleStyle.Render("  • then run ")+codeStyle.Render("dfmc config sync-models")+subtleStyle.Render(" to refresh the catalog"),
			subtleStyle.Render("  • or keep using ")+accentStyle.Render("offline")+subtleStyle.Render(" provider for local analysis"),
		)
	} else {
		for i, name := range providers {
			prefix := "  "
			label := truncateSingleLine(name, listWidth-4)
			if i == m.setupWizard.index {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			if strings.EqualFold(name, m.currentProvider()) {
				label += subtleStyle.Render("  (active)")
			}
			listLines = append(listLines, prefix+label)
		}
	}

	detailLines := []string{
		sectionHeader("◉", "Selection"),
		renderDivider(detailWidth - 2),
	}
	if len(providers) == 0 {
		detailLines = append(detailLines, subtleStyle.Render("Provider config unavailable."))
	} else {
		selected := providers[m.setupWizard.index]
		model := m.defaultModelForProvider(selected)
		profile := m.providerProfile(selected)
		detailLines = append(detailLines,
			fmt.Sprintf("Provider: %s", selected),
			fmt.Sprintf("Model:    %s", blankFallback(model, "-")),
			fmt.Sprintf("Protocol: %s", blankFallback(profile.Protocol, "-")),
			fmt.Sprintf("Context:  %s tokens", formatToolTokenCount(profile.MaxContext)),
			fmt.Sprintf("Output:   %s tokens", formatToolTokenCount(profile.MaxTokens)),
			fmt.Sprintf("Endpoint: %s", blankFallback(profile.BaseURL, "(default)")),
			"",
			subtleStyle.Render("enter applies · s saves to .dfmc/config.yaml · slash: /provider /model"),
		)
		if m.setupWizard.editing {
			draft := m.setupWizard.draft
			if draft == "" {
				draft = model
			}
			detailLines = append(detailLines,
				"",
				subtleStyle.Render("Model Editor (enter apply, esc cancel)"),
				"> "+draft+"|",
			)
		}
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) renderToolsView(width int) string {
	tools := m.availableTools()
	m.toolView.index = clampIndex(m.toolView.index, len(tools))

	listWidth := width / 3
	if listWidth < 24 {
		listWidth = 24
	}
	if listWidth > width-28 {
		listWidth = width / 2
	}
	detailWidth := width - listWidth - 3
	if detailWidth < 24 {
		detailWidth = 24
	}

	listLines := []string{
		sectionHeader("⚒", "Tools"),
		subtleStyle.Render("enter run · e edit params · x reset · ctrl+h keys"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(tools) == 0 {
		listLines = append(listLines,
			warnStyle.Render("No registered tools."),
			"",
			subtleStyle.Render("Tool engine isn't wired up. Check the engine was started with"),
			subtleStyle.Render("tools enabled in ")+codeStyle.Render(".dfmc/config.yaml")+subtleStyle.Render(" or rerun ")+codeStyle.Render("dfmc init")+subtleStyle.Render("."),
		)
	} else {
		for i, name := range tools {
			prefix := "  "
			label := truncateSingleLine(name, listWidth-4)
			if i == m.toolView.index {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			listLines = append(listLines, prefix+label)
		}
	}

	detailLines := []string{
		sectionHeader("▸", "Tool Detail"),
		renderDivider(detailWidth - 2),
	}
	if len(tools) == 0 {
		detailLines = append(detailLines, subtleStyle.Render("Tool engine unavailable."))
	} else {
		selected := tools[m.toolView.index]
		if m.eng != nil && m.eng.Tools != nil {
			if spec, ok := m.eng.Tools.Spec(selected); ok {
				detailLines = append(detailLines,
					highlightToolSpecLines(formatToolSpec(spec), detailWidth)...,
				)
			} else {
				detailLines = append(detailLines,
					fmt.Sprintf("Name:        %s", selected),
					subtleStyle.Render("(no spec registered)"),
				)
			}
		} else {
			detailLines = append(detailLines,
				fmt.Sprintf("Name:        %s", selected),
				fmt.Sprintf("Description: %s", truncateForPanel(m.toolDescription(selected), detailWidth)),
			)
		}
		detailLines = append(detailLines,
			"",
			subtleStyle.Render("Effective params"),
			truncateForPanelSized(m.toolPresetSummary(selected), detailWidth, 6),
			"",
		)
		if selected == "run_command" {
			if suggestions := m.runCommandSuggestions(); len(suggestions) > 0 {
				detailLines = append(detailLines, subtleStyle.Render("Suggested presets"))
				for _, suggestion := range suggestions {
					detailLines = append(detailLines, truncateForPanel("- "+suggestion, detailWidth))
				}
				detailLines = append(detailLines, "")
			}
		}
		if m.toolView.editing {
			detailLines = append(detailLines,
				subtleStyle.Render("Param Editor"),
				truncateForPanel(m.toolView.draft, detailWidth),
				"",
			)
		}
		detailLines = append(detailLines, sectionHeader("✓", "Last Result"))
		resultText := strings.TrimSpace(m.toolView.output)
		if resultText == "" {
			resultText = subtleStyle.Render("No tool run yet — press enter to run the selected tool.")
		}
		detailLines = append(detailLines, truncateForPanel(resultText, detailWidth))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) renderFooter(width int) string {
	maxWidth := max(width-4, 16)

	tab := m.tabs[m.activeTab]
	segments := []string{titleStyle.Render(" " + tab + " ")}
	segments = append(segments, m.footerSegments()...)
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
		segments = append(segments, accentStyle.Render("◆ "+truncateSingleLine(pinned, 22)))
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		segments = append(segments, subtleStyle.Render("· ")+truncateSingleLine(note, 80))
	}
	sep := subtleStyle.Render("  ·  ")
	return truncateSingleLine(strings.Join(segments, sep), maxWidth)
}

func (m Model) footerSegments() []string {
	out := []string{}
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	out = append(out, renderContextBar(tokens, maxCtx, 10))

	info := m.gitInfo
	if strings.TrimSpace(info.Branch) != "" {
		label := info.Branch
		if info.Detached {
			label = "(" + label + ")"
		}
		chip := accentStyle.Render("⎇ ") + boldStyle.Render(label)
		if info.Dirty {
			chip += warnStyle.Render("*")
		}
		out = append(out, chip)
	}
	if info.Inserted > 0 || info.Deleted > 0 {
		churn := okStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
			subtleStyle.Render(",") +
			failStyle.Render(fmt.Sprintf("-%d", info.Deleted))
		out = append(out, churn)
	}
	if !m.sessionStart.IsZero() {
		out = append(out, subtleStyle.Render("⏱ ")+boldStyle.Render(formatSessionDuration(time.Since(m.sessionStart))))
	}
	return out
}

func (m Model) renderHelpOverlay(width int) string {
	if width < 40 {
		width = 40
	}
	tab := m.tabs[m.activeTab]
	lines := []string{
		titleStyle.Render(" Keys ") + subtleStyle.Render("  ctrl+h to close"),
		"",
		boldStyle.Render(tab + " tab"),
	}
	for _, hint := range helpOverlayTabHints(tab) {
		lines = append(lines, "  "+hint)
	}
	lines = append(lines,
		"",
		boldStyle.Render("Global"),
		"  ctrl+p palette · alt+1..0/alt+t/alt+y/alt+w/alt+o or f1..f12 tabs · ctrl+h help · ctrl+s stats",
		"  ctrl+y plans · ctrl+g activity · chat stats: alt+a overview · alt+s todos · alt+d tasks · alt+f agents",
		"  ctrl+c/ctrl+q quit · ctrl+u clear chat input · esc cancels streaming turn (or dismisses parked banner)",
		"",
		boldStyle.Render("Chat composer"),
		"  ↑/↓ history · tab accept suggestion · @ mention file · / browse commands",
		"  @file:10-50 or @file#L10-L50 attaches a line range to the mention",
		"  ctrl+←/→ jump word · ctrl+w kill word · ctrl+k kill to end · ctrl+u clear line",
		"  ctrl+a/ctrl+e line home/end · home/end same · backspace deletes char",
		"  ctrl+t or /file open file picker (alias for @, useful on AltGr layouts)",
		"  /continue resumes a parked agent loop · /btw queues a note",
		"  /clear wipes transcript · /quit exits · /coach mutes notes · /hints toggles trajectory",
		"  /plan enters investigate-only mode · /code exits and re-enables mutations",
		"  /retry resends last user msg · /edit pulls last msg back to the composer",
	)
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, truncateSingleLine(ln, width))
	}
	return strings.Join(out, "\n")
}

func helpOverlayTabHints(tab string) []string {
	switch strings.TrimSpace(strings.ToLower(tab)) {
	case "chat":
		return []string{
			"enter send · ctrl+j or alt+enter newline · / commands · @ mention",
			"wheel · shift+↑/↓ · pgup/pgdn scroll transcript",
			"alt+a overview · alt+s todos · alt+d tasks · alt+f subagents in the right stats panel",
			"when parked: enter resumes · esc dismisses · type a note first to steer",
		}
	case "status":
		return []string{"r refresh status"}
	case "files":
		return []string{
			"j/k or alt+j/alt+k move · p pin · i insert marker",
			"e explain · v review",
		}
	case "patch":
		return []string{
			"d diff · l patch · n/b next/prev file · j/k next/prev hunk",
		}
	case "setup":
		return []string{
			"j/k provider · enter apply · m edit model · s save · r reload",
		}
	case "tools":
		return []string{
			"j/k select · enter run · e edit params · x reset · r rerun",
		}
	case "activity":
		return []string{
			"j/k scroll · pgup/pgdn page · g/G top/tail · p toggle follow · c clear",
		}
	case "memory":
		return []string{
			"j/k scroll · t cycle tier · / search · r refresh · c clear",
		}
	case "codemap":
		return []string{
			"j/k scroll · v cycle view (overview/hotspots/orphans/cycles) · r refresh",
		}
	case "conversations":
		return []string{
			"j/k scroll · enter preview (loads as active) · / search · r refresh · c clear search",
		}
	case "prompts":
		return []string{
			"j/k scroll · enter preview · / search · r refresh · c clear search",
		}
	case "security":
		return []string{
			"r rescan · v toggle secrets/vulns · j/k scroll · / search · c clear search",
		}
	case "plans":
		return []string{
			"e edit task · enter run · esc cancel edit · j/k scroll · c clear",
		}
	case "context":
		return []string{
			"e edit query · enter preview · esc cancel edit · c clear",
		}
	case "providers":
		return []string{
			"j/k scroll · r refresh · g/G top/bottom",
		}
	default:
		return []string{"alt+1..0/alt+t/alt+y/alt+w/alt+o tabs · ctrl+p palette · ctrl+q quit"}
	}
}
