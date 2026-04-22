package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/drive"
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
		"  ctrl+p palette · f1=chat f2=providers f3=files f4=patch f5=workflow f6=tools f7=activity f8=memory f9=codemap f10=conversations f11=prompts f12=security · alt+i=status alt+y=plans alt+w=context alt+t=prompts alt+o=providers · ctrl+h help · ctrl+s stats",
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
	case "workflow":
		return []string{
			"j/k select run · enter expand · esc deselect · r refresh",
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
		return []string{"f1=chat f2=providers f3=files f4=patch f5=workflow f6=tools f7=activity f8=memory f9=codemap f10=conversations f11=prompts f12=security · alt+i=status alt+y=plans alt+w=context · ctrl+p palette · ctrl+q quit"}
	}
}
// renderWorkflowView shows the Drive TODO tree panel. Two-column layout:
// LEFT (30%): run selector list — ID, task truncated, status chip, age
// RIGHT (70%): TODO tree for the selected run — hierarchical view of
// pending/running/done/blocked/skipped TODOs with expand/collapse detail.
func (m Model) renderWorkflowView(width int) string {
	listWidth := width * 30 / 100
	if listWidth < 28 {
		listWidth = 28
	}
	if listWidth > width-28 {
		listWidth = width / 2
	}
	detailWidth := width - listWidth - 3

	runs := m.workflow.runs
	listLines := []string{
		sectionHeader("\u26a1", "Workflow"),
		subtleStyle.Render("enter select \u00b7 j/k move \u00b7 r routing"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(runs) == 0 {
		listLines = append(listLines,
			subtleStyle.Render("No drive runs yet."),
			"",
			subtleStyle.Render("Start one from Chat:"),
			subtleStyle.Render("  /drive <task description>"),
		)
	} else {
		for i, r := range runs {
			prefix := "  "
			label := truncateForLine(r.ID, 8) + "  " + truncateForLine(r.Task, listWidth-14)
			if r.ID == m.workflow.selectedRunID {
				prefix = "> "
				label = titleStyle.Render(label)
			} else if i == m.workflow.selectedIndex {
				prefix = "> "
			}
			statusChip := renderRunStatusChip(r.Status)
			listLines = append(listLines, prefix+label+"  "+statusChip)
		}
	}

	// Show routing summary in the header when routing draft has entries
	if routing := m.workflow.routingDraft; len(routing) > 0 {
		var parts []string
		keys := make([]string, 0, len(routing))
		for k := range routing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, tag := range keys {
			parts = append(parts, tag+"\u2192"+routing[tag])
		}
		if len(parts) > 0 {
			listLines = append(listLines, "", subtleStyle.Render("routing: "+strings.Join(parts, " \u00b7 ")))
		}
	}

	var detailLines []string
	selectedRun := m.selectedRunForWorkflow()
	if selectedRun == nil {
		detailLines = []string{
			sectionHeader("\u25c9", "Run Details"),
			renderDivider(detailWidth - 2),
			"",
			subtleStyle.Render("Select a run from the list to inspect its TODO tree."),
		}
	} else {
		_done, _blocked, _skipped, _pending := selectedRun.Counts()
		running := 0
		for _, t := range selectedRun.Todos {
			if t.Status == drive.TodoRunning {
				running++
			}
		}
		done, blocked, skipped, pending := _done, _blocked, _skipped, _pending
		detailLines = []string{
			sectionHeader("\u26a1", truncateForLine(selectedRun.Task, detailWidth-20)),
			fmt.Sprintf("%s  %s  %s  %s  %s  %s",
				renderRunStatusChip(selectedRun.Status),
				doneStyle.Render(fmt.Sprintf("%d done", done)),
				pendingStyle.Render(fmt.Sprintf("%d pending", pending)),
				runningStyle.Render(fmt.Sprintf("%d running", running)),
				blockedStyle.Render(fmt.Sprintf("%d blocked", blocked)),
				skippedStyle.Render(fmt.Sprintf("%d skipped", skipped)),
			),
			renderDivider(detailWidth - 2),
			"",
		}
		rows := m.renderWorkflowTreeRows(selectedRun, detailWidth)
		detailLines = append(detailLines, rows...)

		// Show TODO detail below tree when a TODO is selected
		if m.workflow.selectedTodoID != "" {
			detailLines = append(detailLines, "", renderDivider(detailWidth-2), "")
			detailLines = append(detailLines, m.renderWorkflowTodoDetail(selectedRun, detailWidth)...)
		}
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))

	// Show routing editor overlay when active
	if m.workflow.showRoutingEditor {
		routingPanel := m.renderRoutingEditor(width)
		return routingPanel
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) selectedRunForWorkflow() *drive.Run {
	if m.workflow.selectedRunID == "" {
		return nil
	}
	for _, r := range m.workflow.runs {
		if r.ID == m.workflow.selectedRunID {
			return r
		}
	}
	return nil
}

func (m Model) renderWorkflowTreeRows(run *drive.Run, width int) []string {
	if run == nil || len(run.Todos) == 0 {
		return []string{subtleStyle.Render("(no TODOs \u2014 run may still be planning)")}
	}

	kids := make(map[string][]*drive.Todo)
	var roots []*drive.Todo
	todoMap := make(map[string]*drive.Todo)
	for i := range run.Todos {
		t := &run.Todos[i]
		todoMap[t.ID] = t
	}
	for i := range run.Todos {
		t := &run.Todos[i]
		if t.ParentID == "" || todoMap[t.ParentID] == nil {
			roots = append(roots, t)
		} else {
			kids[t.ParentID] = append(kids[t.ParentID], t)
		}
	}

	var rows []string
	var walk func(t *drive.Todo, depth int)
	walk = func(t *drive.Todo, depth int) {
		prefix := strings.Repeat("  ", depth)
		icon := todoStatusIcon(t.Status)
		expanded := m.workflow.expandedTodo[t.ID]
		expandMark := " "
		if _, hasKids := kids[t.ID]; hasKids {
			if expanded {
				expandMark = "\u25bc"
			} else {
				expandMark = "\u25b6"
			}
		}
		title := truncateForLine(t.Title, width-depth*2-8)
		line := prefix + icon + expandMark + " " + title
		tagStr := ""
		if t.ProviderTag != "" {
			tagStr += subtleStyle.Render("[" + t.ProviderTag + "]")
		}
		if t.WorkerClass != "" {
			tagStr += subtleStyle.Render("[" + t.WorkerClass + "]")
		}
		if tagStr != "" {
			line += "  " + tagStr
		}
		rows = append(rows, line)
		if expanded {
			for _, child := range kids[t.ID] {
				walk(child, depth+1)
			}
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
}

func todoStatusIcon(s drive.TodoStatus) string {
	switch s {
	case drive.TodoPending:
		return "\u23f3"
	case drive.TodoRunning:
		return "\U0001f504"
	case drive.TodoDone:
		return "\u2705"
	case drive.TodoBlocked:
		return "\u274c"
	case drive.TodoSkipped:
		return "\u23ed"
	default:
		return "\u25cb"
	}
}

func renderRunStatusChip(s drive.RunStatus) string {
	switch s {
	case drive.RunPlanning:
		return subtleStyle.Render("\u25a1 planning")
	case drive.RunRunning:
		return accentStyle.Render("\u25b8 running")
	case drive.RunDone:
		return doneStyle.Render("\u2713 done")
	case drive.RunStopped:
		return subtleStyle.Render("\u25a0 stopped")
	case drive.RunFailed:
		return blockedStyle.Render("\u2717 failed")
	default:
		return subtleStyle.Render(string(s))
	}
}

// renderWorkflowTodoDetail shows the expanded detail of a selected TODO:
// ID, status, ProviderTag, WorkerClass, Brief, Detail, and the routed
// profile name from the drive.Config.Routing map.
func (m Model) renderWorkflowTodoDetail(run *drive.Run, width int) []string {
	if run == nil || m.workflow.selectedTodoID == "" {
		return nil
	}
	var todo *drive.Todo
	for i := range run.Todos {
		if run.Todos[i].ID == m.workflow.selectedTodoID {
			todo = &run.Todos[i]
			break
		}
	}
	if todo == nil {
		return nil
	}

	lines := []string{
		titleStyle.Render("TODO Detail"),
		"",
		fmt.Sprintf("  ID:       %s", subtleStyle.Render(todo.ID)),
		fmt.Sprintf("  Status:   %s", todoStatusIcon(todo.Status)+" "+subtleStyle.Render(string(todo.Status))),
	}
	if todo.ProviderTag != "" {
		lines = append(lines, fmt.Sprintf("  Tag:      %s", accentStyle.Render(todo.ProviderTag)))
		// Show which profile this tag routes to
		if m.eng != nil && m.eng.Config != nil {
			routing := m.workflow.routingDraft
			if profile, ok := routing[todo.ProviderTag]; ok {
				lines = append(lines, fmt.Sprintf("  Routed:   %s \u2192 %s", subtleStyle.Render(todo.ProviderTag), accentStyle.Render(profile)))
			} else {
				lines = append(lines, fmt.Sprintf("  Routed:   %s \u2192 %s", subtleStyle.Render(todo.ProviderTag), subtleStyle.Render("(default)")))
			}
		}
	}
	if todo.WorkerClass != "" {
		lines = append(lines, fmt.Sprintf("  Worker:   %s", subtleStyle.Render(todo.WorkerClass)))
	}
	if todo.Brief != "" {
		lines = append(lines, fmt.Sprintf("  Brief:    %s", truncateForPanel(todo.Brief, width)))
	}
	if todo.Detail != "" {
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render("  Detail:"))
		for _, detailLine := range strings.Split(todo.Detail, "\n") {
			lines = append(lines, "  "+subtleStyle.Render(truncateForPanel(detailLine, width-2)))
		}
	}
	if todo.FileScope != nil && len(todo.FileScope) > 0 {
		lines = append(lines, fmt.Sprintf("  Scope:    %s", subtleStyle.Render(strings.Join(todo.FileScope, ", "))))
	}
	if len(todo.DependsOn) > 0 {
		lines = append(lines, fmt.Sprintf("  Depends:  %s", subtleStyle.Render(strings.Join(todo.DependsOn, ", "))))
	}
	if todo.Error != "" {
		lines = append(lines, "")
		lines = append(lines, failStyle.Render("  Error:   ")+failStyle.Render(truncateForPanel(todo.Error, width-8)))
	}
	lines = append(lines, "", subtleStyle.Render("esc deselect"))
	return lines
}

// handleWorkflowKey keyboard handler for the Workflow tab.
// Two-level navigation: run selector (left column, no run selected) →
// TODO tree (right column, run selected). 'r' opens the routing editor
// overlay when no run is selected, letting the user configure the
// drive.Config.Routing map (ProviderTag → profile name).
func (m Model) handleWorkflowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Routing editor has its own key handler
	if m.workflow.showRoutingEditor {
		return m.handleRoutingEditorKey(msg)
	}

	total := len(m.workflow.runs)
	step := 1

	switch msg.String() {
	case "j", "down":
		if m.workflow.selectedRunID == "" {
			// run selector: move selectedIndex down
			if m.workflow.selectedIndex+step < total {
				m.workflow.selectedIndex += step
			}
		} else {
			// TODO tree: scroll down
			m.workflow.scrollY += step
		}
	case "k", "up":
		if m.workflow.selectedRunID == "" {
			if m.workflow.selectedIndex >= step {
				m.workflow.selectedIndex -= step
			} else {
				m.workflow.selectedIndex = 0
			}
		} else {
			if m.workflow.scrollY >= step {
				m.workflow.scrollY -= step
			} else {
				m.workflow.scrollY = 0
			}
		}
	case "g":
		if m.workflow.selectedRunID == "" {
			m.workflow.selectedIndex = 0
		} else {
			m.workflow.scrollY = 0
		}
	case "G":
		if m.workflow.selectedRunID == "" {
			if total > 0 {
				m.workflow.selectedIndex = total - 1
			}
		}
	case "enter", "o":
		if m.workflow.selectedRunID == "" {
			// select a run from the selector list
			if m.workflow.selectedIndex >= 0 && m.workflow.selectedIndex < total {
				run := m.workflow.runs[m.workflow.selectedIndex]
				m.workflow.selectedRunID = run.ID
				m.workflow.scrollY = 0
			}
		} else {
			// toggle TODO expand + set selectedTodoID
			m = m.cycleWorkflowTodoExpand()
			// Set selectedTodoID to the TODO at current scroll position
			run := m.selectedRunForWorkflow()
			if run != nil {
				visible := 0
				for _, t := range run.Todos {
					if t.ParentID == "" || m.workflow.expandedTodo[t.ParentID] {
						if visible == m.workflow.scrollY {
							m.workflow.selectedTodoID = t.ID
							break
						}
						visible++
					}
				}
			}
		}
	case "r":
		// routing editor: only when no run is selected
		if m.workflow.selectedRunID == "" {
			m.workflow.showRoutingEditor = true
			m.workflow.routingEditTag = ""
			m.workflow.routingEditProfile = ""
			m.workflow.routingEditIndex = 0
			m.workflow.routingEditMode = false
			if m.workflow.routingDraft == nil {
				m.workflow.routingDraft = make(map[string]string)
			}
		}
	case "esc":
		if m.workflow.showRoutingEditor {
			m.workflow.showRoutingEditor = false
		} else if m.workflow.selectedTodoID != "" {
			// deselect TODO — hide detail
			m.workflow.selectedTodoID = ""
		} else if m.workflow.selectedRunID != "" {
			// deselect run — back to run selector
			m.workflow.selectedRunID = ""
			m.workflow.scrollY = 0
		}
	}
	return m, nil
}

// handleRoutingEditorKey handles keystrokes within the routing editor overlay.
// 'routingDraft' holds the tag→profile map being edited.
func (m Model) handleRoutingEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	routing := m.workflow.routingDraft
	keys := make([]string, 0, len(routing))
	for k := range routing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if keys == nil {
		keys = []string{}
	}
	step := 1

	switch msg.String() {
	case "j", "down":
		if m.workflow.routingEditMode {
			// cycling through available profiles
			profiles := m.availableProviders()
			if len(profiles) > 0 {
				cur := m.workflow.routingEditProfile
				idx := 0
				for i, p := range profiles {
					if p == cur {
						idx = i
						break
					}
				}
				idx = (idx + 1) % len(profiles)
				m.workflow.routingEditProfile = profiles[idx]
			}
		} else {
			if m.workflow.routingEditIndex+step < len(keys) {
				m.workflow.routingEditIndex += step
			}
		}
	case "k", "up":
		if m.workflow.routingEditMode {
			profiles := m.availableProviders()
			if len(profiles) > 0 {
				cur := m.workflow.routingEditProfile
				idx := len(profiles) - 1
				for i, p := range profiles {
					if p == cur {
						idx = i
						break
					}
				}
				idx = (idx - 1 + len(profiles)) % len(profiles)
				m.workflow.routingEditProfile = profiles[idx]
			}
		} else {
			if m.workflow.routingEditIndex >= step {
				m.workflow.routingEditIndex -= step
			} else {
				m.workflow.routingEditIndex = 0
			}
		}
	case "enter":
		if m.workflow.routingEditMode {
			// commit the edit
			tag := m.workflow.routingEditTag
			if tag != "" && m.workflow.routingEditProfile != "" {
				m.workflow.routingDraft[tag] = m.workflow.routingEditProfile
			}
			m.workflow.routingEditMode = false
			m.workflow.routingEditTag = ""
			m.workflow.routingEditProfile = ""
		} else {
			// start editing the selected row's profile
			if m.workflow.routingEditIndex >= 0 && m.workflow.routingEditIndex < len(keys) {
				tag := keys[m.workflow.routingEditIndex]
				m.workflow.routingEditTag = tag
				m.workflow.routingEditProfile = routing[tag]
				m.workflow.routingEditMode = true
			}
		}
	case "+":
		// add a new entry (cycle through available profiles as tag)
		profiles := m.availableProviders()
		if len(profiles) > 0 {
			// Use the first profile as a starting point for a new tag
			tag := profiles[0]
			// Make tag name from profile
			m.workflow.routingEditTag = tag
			m.workflow.routingEditProfile = profiles[0]
			m.workflow.routingEditMode = true
			// Add to draft with current profile as default
			m.workflow.routingDraft[tag] = profiles[0]
			// Select the new row
			for i, k := range keys {
				if k == tag {
					m.workflow.routingEditIndex = i
					break
				}
			}
		}
	case "d":
		// delete the selected entry
		if m.workflow.routingEditIndex >= 0 && m.workflow.routingEditIndex < len(keys) {
			tag := keys[m.workflow.routingEditIndex]
			delete(m.workflow.routingDraft, tag)
			if m.workflow.routingEditIndex >= len(m.workflow.routingDraft) {
				m.workflow.routingEditIndex = max(0, len(m.workflow.routingDraft)-1)
			}
		}
	case "esc":
		m.workflow.showRoutingEditor = false
		m.workflow.routingEditMode = false
	}
	return m, nil
}

// cycleWorkflowTodoExpand finds the TODO at the current scroll position
// and toggles its expanded state.
func (m Model) cycleWorkflowTodoExpand() Model {
	run := m.selectedRunForWorkflow()
	if run == nil || len(run.Todos) == 0 {
		return m
	}
	// find Nth visible TODO at current scroll
	visible := 0
	var targetID string
	for _, t := range run.Todos {
		if t.ParentID == "" || m.workflow.expandedTodo[t.ParentID] {
			if visible == m.workflow.scrollY {
				targetID = t.ID
				break
			}
			visible++
		}
	}
	if targetID != "" {
		m.workflow.expandedTodo[targetID] = !m.workflow.expandedTodo[targetID]
	}
	return m
}

// renderRoutingEditor shows the drive.Config.Routing editor overlay.
// The routing map controls which provider profile is used for each
// TODO ProviderTag (plan/code/review/test/etc.) when starting a drive run.
func (m Model) renderRoutingEditor(width int) string {
	if m.eng == nil || m.eng.Config == nil {
		return subtleStyle.Render("(engine not available)")
	}

	lines := []string{
		sectionHeader("\u2699", "Routing Editor"),
		subtleStyle.Render("enter edit \u00b7 + add \u00b7 d delete \u00b7 esc close"),
		renderDivider(width - 4),
		"",
	}

	profiles := m.availableProviders()
	if len(profiles) == 0 {
		lines = append(lines, subtleStyle.Render("(no provider profiles configured)"))
		return strings.Join(lines, "\n")
	}

	routing := m.workflowRouting()
	if len(routing) == 0 {
		lines = append(lines, subtleStyle.Render("No routing entries yet."))
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render("Press + to add a tag\u2192profile mapping."))
	} else {
		keys := make([]string, 0, len(routing))
		for k := range routing {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for i, tag := range keys {
			profile := routing[tag]
			prefix := "  "
			if i == m.workflow.routingEditIndex {
				prefix = "> "
			}
			// If this row is being edited
			if m.workflow.routingEditMode && i == m.workflow.routingEditIndex {
				profile = codeStyle.Render(m.workflow.routingEditProfile) + subtleStyle.Render("|")
			}
			lines = append(lines, prefix+titleStyle.Render(tag)+subtleStyle.Render(" \u2192 ")+accentStyle.Render(profile))
		}
	}

	lines = append(lines, "")
	lines = append(lines, subtleStyle.Render("Profiles: ")+strings.Join(profiles, ", "))

	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// workflowRouting returns the current routing map from the workflow state.
// This is stored on the model so it persists while the editor is open.
func (m Model) workflowRouting() map[string]string {
	// m.workflow.routing is not persisted — it's built from the engine config
	// on each render call.
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	// Build a routing map from the engine's drive config
	// For now, return an empty map — the user can add entries via the editor.
	// A future enhancement would load this from a saved config.
	return m.workflow.routingDraft
}
