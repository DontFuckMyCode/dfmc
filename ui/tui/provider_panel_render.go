package tui

// provider_panel_render.go

import (
	"fmt"
	"strings"
	"github.com/charmbracelet/lipgloss"
	"github.com/dontfuckmycode/dfmc/internal/config"
)

func formatProviderRow(row providerRow, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = accentStyle.Render("▶ ")
	}
	tag := providerStatusStyle(row.Status)
	name := row.Name
	if row.IsPrimary {
		name = accentStyle.Render(name) + subtleStyle.Render("*")
	}
	tools := "tools=off"
	if row.SupportsTools {
		tools = "tools=on"
	}
	model := row.Model
	if strings.TrimSpace(model) == "" {
		model = "(no model)"
	}
	line := marker + tag + "  " + name + "  " + subtleStyle.Render(model) +
		"  " + subtleStyle.Render(fmt.Sprintf("max=%d", row.MaxContext)) +
		"  " + subtleStyle.Render(tools)
	if row.ToolStyle != "" {
		line += "  " + subtleStyle.Render(row.ToolStyle)
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatProviderRowNumbered renders one numbered line. Shape:
// `▶ 1. [READY] anthropic*  claude-opus-4  [key:ok]  max=200k  tools=on`.
func formatProviderRowNumbered(row providerRow, num int, selected bool, fallbackPos map[string]int, width int) string {
	marker := "  "
	if selected {
		marker = accentStyle.Render("▶ ")
	}
	tag := providerStatusStyle(row.Status)
	name := row.Name
	if row.IsPrimary {
		name = accentStyle.Render(name) + subtleStyle.Render("*")
	} else if pos, ok := fallbackPos[strings.ToLower(strings.TrimSpace(row.Name))]; ok {
		name = subtleStyle.Render(name) + subtleStyle.Render(fmt.Sprintf("↓[%d]", pos))
	}

	keyBadge := subtleStyle.Render("[key:--]")
	if row.Status == "ready" {
		keyBadge = accentStyle.Render("[key:ok]")
	}

	model := row.Model
	if strings.TrimSpace(model) == "" {
		model = "(no model)"
	}
	modelCount := subtleStyle.Render(fmt.Sprintf("models=%d", len(row.Models)))
	if len(row.Models) == 0 {
		modelCount = warnStyle.Render("models=0")
	}
	line := fmt.Sprintf("%s%d. %s  %s  %s  %s  %s  %s  %s",
		marker, num, tag, name,
		subtleStyle.Render(model),
		keyBadge,
		modelCount,
		subtleStyle.Render(fmt.Sprintf("max=%d", row.MaxContext)),
		subtleStyle.Render(fmt.Sprintf("tools=%v", row.SupportsTools)),
	)
	if row.ToolStyle != "" {
		line += "  " + subtleStyle.Render(row.ToolStyle)
	}
	if strings.TrimSpace(row.Protocol) != "" {
		line += "  " + subtleStyle.Render("protocol="+row.Protocol)
	}
	if len(row.BestFor) > 0 {
		tags := strings.Join(row.BestFor, ",")
		if len(tags) > 20 {
			tags = tags[:20] + "..."
		}
		line += "  " + subtleStyle.Render("best_for="+tags)
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatProviderDetail renders the selected row's extended info beneath the list.
func formatProviderDetail(row providerRow, width int) []string {
	var out []string
	head := row.Name
	if row.IsPrimary {
		head = accentStyle.Render(row.Name) + subtleStyle.Render(" · primary")
	}
	out = append(out, "    "+head)
	out = append(out, "    "+subtleStyle.Render(fmt.Sprintf(
		"model=%s  max_context=%d  tool_style=%s  tools=%v",
		nonEmpty(row.Model, "(none)"), row.MaxContext, nonEmpty(row.ToolStyle, "(none)"), row.SupportsTools,
	)))
	if len(row.Models) > 1 {
		out = append(out, "    "+subtleStyle.Render(fmt.Sprintf("(%d models configured)", len(row.Models))))
	}
	if len(row.BestFor) > 0 {
		out = append(out, "    "+subtleStyle.Render("best_for: ")+strings.Join(row.BestFor, ", "))
	}
	switch row.Status {
	case "no-key":
		var hint string
		if envVar := config.EnvVarForProvider(row.Name); envVar != "" {
			hint = "missing API key — set " + envVar + " or add api_key to providers.profiles." + row.Name
		} else {
			hint = "missing API key — add api_key to providers.profiles." + row.Name
		}
		out = append(out, "    "+warnStyle.Render(hint))
	case "offline":
		out = append(out, "    "+subtleStyle.Render("offline provider — deterministic fallback, no network."))
	}
	_ = width
	return out
}

func (m Model) renderProvidersView(width int) string {
	if m.providers.confirmAction != "" {
		return m.renderProvidersConfirm(width)
	}
	switch m.providers.viewMode {
	case "detail":
		return m.renderProviderDetailView(width)
	case "pipelines":
		return m.renderPipelinesView(width)
	case "new_provider":
		return m.renderNewProviderView(width)
	default:
		return m.renderProviderListView(width)
	}
}

func (m Model) renderProviderListView(width int) string {
	width = clampInt(width, 24, 1000)
	hint := subtleStyle.Render("j/k scroll · g/G/home/end top/bottom · pgup/pgdown page · enter menu · / search · c clear")
	header := sectionHeader("⚑", "Providers")

	rows := filteredProviderRows(m.providers.rows, m.providers.query)
	order := resolveProviderOrder(m.eng)

	lines := []string{header, hint}

	if m.providers.activePipeline != "" {
		lines = append(lines, subtleStyle.Render("active pipeline: ")+accentStyle.Render(m.providers.activePipeline))
	}
	if len(order) > 0 {
		var numbered []string
		for i, name := range order {
			numbered = append(numbered, fmt.Sprintf("%d.%s", i+1, accentStyle.Render(name)))
		}
		chainLine := subtleStyle.Render("fallback chain: ") + strings.Join(numbered, subtleStyle.Render(" → "))
		if width > 0 {
			chainLine = truncateSingleLine(chainLine, width-2)
		}
		lines = append(lines, chainLine)
	}
	lines = append(lines, renderDivider(width-2))

	if m.providers.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.providers.err))
		return strings.Join(lines, "\n")
	}

	if m.providers.searchActive {
		searchLine := "search: " + accentStyle.Render(m.providers.query) + subtleStyle.Render("  · enter confirm · esc cancel")
		lines = append(lines, "", searchLine)
	}

	if len(rows) == 0 {
		if m.providers.query != "" {
			lines = append(lines, "", subtleStyle.Render("No providers match your search."))
		} else {
			lines = append(lines,
				"",
				warnStyle.Render("No providers registered"),
				subtleStyle.Render("The engine is in degraded startup."),
				"",
				subtleStyle.Render("Press Enter → New Provider to add one."),
			)
		}
		return strings.Join(lines, "\n")
	}

	readyCount := 0
	noKeyCount := 0
	for _, r := range m.providers.rows {
		switch r.Status {
		case "ready":
			readyCount++
		case "no-key":
			noKeyCount++
		}
	}
	offlineCount := len(m.providers.rows) - readyCount - noKeyCount
	primaryName := ""
	if m.eng != nil {
		primaryName = strings.TrimSpace(m.eng.Config.Providers.Primary)
	}
	summary := fmt.Sprintf("%d providers · %d ready · %d missing keys · %d offline", len(m.providers.rows), readyCount, noKeyCount, offlineCount)
	if primaryName != "" {
		summary += " · primary: " + primaryName
	}
	if m.providers.syncing {
		summary = runningStyle.Render("syncing models...") + "  " + subtleStyle.Render(summary)
	} else if !m.providers.lastSyncedAt.IsZero() {
		summary += subtleStyle.Render(" · synced " + formatRelativeTime(m.providers.lastSyncedAt))
	}
	if m.providers.query != "" {
		summary += subtleStyle.Render(fmt.Sprintf("  · showing %d of %d", len(rows), len(m.providers.rows)))
	}
	lines = append(lines, subtleStyle.Render(summary), "")

	// Build fallback map for markers (name -> 1-based position)
	fallbackPos := map[string]int{}
	if m.eng != nil {
		for idx, n := range m.eng.FallbackProviders() {
			fallbackPos[strings.ToLower(strings.TrimSpace(n))] = idx + 1
		}
	}

	scroll := clampScroll(m.providers.scroll, len(rows))
	lastStatus := ""
	for i, row := range rows {
		if lastStatus != "" && row.Status != lastStatus {
			label := strings.ToUpper(row.Status)
			lines = append(lines, subtleStyle.Render("  ─── "+label+" ───"))
		}
		lastStatus = row.Status
		selected := i == scroll
		lines = append(lines, formatProviderRowNumbered(row, i+1, selected, fallbackPos, width-2))
	}

	if scroll >= 0 && scroll < len(rows) {
		lines = append(lines, "")
		lines = append(lines, formatProviderDetail(rows[scroll], width-2)...)
	}

	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderProviderDetailView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "Provider Detail")
	hint := subtleStyle.Render("j/k scroll · g/G/home/end top/bottom · pgup/pgdown page · enter menu · esc/q back")
	if m.providers.modelPickerActive {
		if m.providers.modelPickerManual {
			hint = subtleStyle.Render("type model · enter confirm · esc cancel")
		} else {
			hint = subtleStyle.Render("j/k scroll · g/G home/end · pgup/pgdown page · enter pick · m manual · esc cancel")
		}
	} else if m.providers.profileEditMode {
		hint = subtleStyle.Render("tab field · enter commit · esc cancel")
	}
	lines := []string{header, hint, renderDivider(width - 2)}

	if m.eng == nil || m.eng.Config == nil {
		lines = append(lines, warnStyle.Render("engine unavailable"))
		return strings.Join(lines, "\n")
	}

	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		lines = append(lines, warnStyle.Render("provider not found: "+m.providers.detailProvider))
		return strings.Join(lines, "\n")
	}

	name := m.providers.detailProvider
	status := "no-key"
	if strings.EqualFold(name, "offline") {
		status = "offline"
	} else if strings.TrimSpace(prof.APIKey) != "" || strings.TrimSpace(prof.BaseURL) != "" {
		status = "ready"
	}

	// Header badges
	badges := []string{providerStatusStyle(status)}
	badges = append(badges, apiKeySourceBadge(name, prof))
	if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		badges = append(badges, accentStyle.Render("[primary]"))
	}
	if m.eng != nil {
		fbs := m.eng.FallbackProviders()
		for fi, fb := range fbs {
			if strings.EqualFold(fb, name) {
				pos := fmt.Sprintf("%d", fi+1)
				suffix := "th"
				switch pos {
				case "1":
					suffix = "st"
				case "2":
					suffix = "nd"
				case "3":
					suffix = "rd"
				}
				badges = append(badges, subtleStyle.Render("[fallback: "+pos+suffix+"]"))
				break
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, accentStyle.Render(name))
	badgeLine := "  " + strings.Join(badges, "  ")
	if width > 0 {
		badgeLine = truncateSingleLine(badgeLine, width-2)
	}
	lines = append(lines, badgeLine)
	lines = append(lines, "")
	lines = append(lines, sectionTitleStyle.Render("Profile"))

	// Profile fields
	protocol := nonEmpty(prof.Protocol, "(auto)")
	baseURL := nonEmpty(prof.BaseURL, "(default)")
	maxContext := fmt.Sprintf("%d", prof.MaxContext)
	if prof.MaxContext == 0 {
		maxContext = "(default)"
	}
	maxTokens := fmt.Sprintf("%d", prof.MaxTokens)
	if prof.MaxTokens == 0 {
		maxTokens = "(default)"
	}
	if m.providers.profileEditMode {
		fields := []struct {
			label  string
			value  string
			active bool
		}{
			{"protocol", protocol, m.providers.profileEditField == 0},
			{"base_url", baseURL, m.providers.profileEditField == 1},
			{"max_context", maxContext, m.providers.profileEditField == 2},
			{"max_tokens", maxTokens, m.providers.profileEditField == 3},
		}
		for _, f := range fields {
			prefix := "  "
			var label string
			val := f.value
			if f.active {
				prefix = accentStyle.Render("▶ ")
				label = accentStyle.Render(f.label)
				if m.providers.profileEditDraft != "" {
					val = accentStyle.Render(m.providers.profileEditDraft)
				} else {
					val = accentStyle.Render(val)
				}
			} else {
				label = subtleStyle.Render(f.label)
				val = subtleStyle.Render(val)
			}
			lines = append(lines, prefix+label+"="+val)
		}
	} else {
		lines = append(lines, "  "+subtleStyle.Render("protocol:")+" "+protocol)
		lines = append(lines, "  "+subtleStyle.Render("base_url:")+" "+baseURL)
		lines = append(lines, "  "+subtleStyle.Render("max_context:")+" "+maxContext)
		lines = append(lines, "  "+subtleStyle.Render("max_tokens:")+" "+maxTokens)
	}

	// Best-for tags from the provider row
	for _, r := range m.providers.rows {
		if strings.EqualFold(r.Name, m.providers.detailProvider) {
			if len(r.BestFor) > 0 {
				lines = append(lines, "")
				lines = append(lines, "  "+subtleStyle.Render("best_for: ")+strings.Join(r.BestFor, ", "))
			}
			break
		}
	}

	// Models section with numbered list and scroll window
	models := prof.AllModels()
	lines = append(lines, "")
	selectedIdx := m.providers.modelEditIdx
	const modelWindow = 12
	start := 0
	if selectedIdx >= modelWindow {
		start = selectedIdx - modelWindow + 1
	}
	end := start + modelWindow
	if end > len(models) {
		end = len(models)
	}
	var modelTitle string
	if len(models) > modelWindow {
		modelTitle = fmt.Sprintf("Models (%d-%d of %d)", start+1, end, len(models))
	} else {
		modelTitle = fmt.Sprintf("Models (%d)", len(models))
	}
	lines = append(lines, sectionTitleStyle.Render(modelTitle))
	if len(models) == 0 {
		lines = append(lines, "  "+warnStyle.Render("No models configured"))
		lines = append(lines, "  "+subtleStyle.Render("Press Enter → Add Model to add one."))
	} else {
		if start > 0 {
			lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("... %d more above", start)))
		}
		activeModel := strings.TrimSpace(prof.Model)
		for i := start; i < end; i++ {
			model := models[i]
			num := fmt.Sprintf("%2d.", i+1)
			prefix := "  " + num + " "
			label := model
			isActive := strings.EqualFold(model, activeModel)
			if i == m.providers.modelEditIdx {
				prefix = accentStyle.Render("▶ ") + num + " "
				label = accentStyle.Render(label)
				if isActive {
					label += okStyle.Render(" ← active")
				}
			} else if isActive {
				label = label + okStyle.Render(" ← active")
			}
			lines = append(lines, prefix+label)
		}
		if end < len(models) {
			lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("... %d more below", len(models)-end)))
		}
	}

	// Fallback models
	if len(prof.FallbackModels) > 0 {
		lines = append(lines, "")
		lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf("Fallback Models (%d)", len(prof.FallbackModels))))
		for i, model := range prof.FallbackModels {
			lines = append(lines, fmt.Sprintf("  %2d. %s", i+1, model))
		}
	}

	// Model picker
	if m.providers.modelPickerActive {
		lines = append(lines, "", sectionTitleStyle.Render("Add Model"))
		if m.providers.modelPickerManual {
			lines = append(lines, "  "+accentStyle.Render("▶ ")+accentStyle.Render(m.providers.modelPickerDraft))
		} else {
			items := m.providers.modelPickerItems
			if len(items) == 0 {
				lines = append(lines, "  "+subtleStyle.Render("(no models in cache — press m for manual entry)"))
			} else {
				lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("(%d models in cache)", len(items))))
			}
			const pickerWindow = 12
			idx := m.providers.modelPickerIndex
			start := 0
			if idx >= pickerWindow {
				start = idx - pickerWindow + 1
			}
			end := start + pickerWindow
			if end > len(items) {
				end = len(items)
			}
			if start > 0 {
				lines = append(lines, "    "+subtleStyle.Render(fmt.Sprintf("... %d more above", start)))
			}
			for i := start; i < end; i++ {
				prefix := "    "
				label := items[i]
				if i == idx {
					prefix = accentStyle.Render("▶ ")
					label = accentStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
			if end < len(items) {
				lines = append(lines, "    "+subtleStyle.Render(fmt.Sprintf("... %d more below", len(items)-end)))
			}
		}
	}

	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderPipelinesView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "Pipelines")

	if m.providers.pipelineEditMode {
		return m.renderPipelineEditView(width, header)
	}

	hint := subtleStyle.Render("j/k scroll · g/G/home/end top/bottom · pgup/pgdown page · enter menu · esc/q back")
	lines := []string{header, hint, renderDivider(width - 2)}

	names := m.providers.pipelineNames
	if len(names) == 0 {
		lines = append(lines, "",
			warnStyle.Render("No pipelines configured"),
			"",
			subtleStyle.Render("Press Enter → New Pipeline to create one."),
		)
		return strings.Join(lines, "\n")
	}

	lines = append(lines, subtleStyle.Render(fmt.Sprintf("%d pipelines configured", len(names))), "")

	for i, name := range names {
		selected := i == m.providers.pipelineScroll
		prefix := "  "
		num := fmt.Sprintf("%d.", i+1)
		label := name
		if selected {
			prefix = accentStyle.Render("▶ ")
			num = accentStyle.Render(num)
			label = accentStyle.Render(label)
		} else {
			num = subtleStyle.Render(num)
			label = subtleStyle.Render(label)
		}
		if name == m.providers.activePipeline {
			label += accentStyle.Render(" · active")
		}
		if m.eng != nil {
			if pipe, ok := m.eng.Pipeline(name); ok {
				if len(pipe.Steps) > 0 {
					if selected {
						lines = append(lines, prefix+num+" "+label)
						for j, step := range pipe.Steps {
							stepNum := fmt.Sprintf("%d.", j+1)
							var stepLabel string
							if j == 0 {
								stepLabel = subtleStyle.Render(stepNum+" ") + accentStyle.Render(step.Provider) + subtleStyle.Render(" / ") + accentStyle.Render(step.Model)
								stepLabel += subtleStyle.Render(" ← primary")
							} else {
								stepLabel = subtleStyle.Render(stepNum+" ") + subtleStyle.Render(step.Provider) + subtleStyle.Render(" / ") + subtleStyle.Render(step.Model)
								stepLabel += subtleStyle.Render(fmt.Sprintf(" ← fallback %d", j))
							}
							lines = append(lines, "    "+stepLabel)
						}
					} else {
						label += subtleStyle.Render(fmt.Sprintf(" · %d steps", len(pipe.Steps)))
						lines = append(lines, prefix+num+" "+label)
					}
				} else {
					lines = append(lines, prefix+num+" "+label)
				}
			} else {
				lines = append(lines, prefix+num+" "+label)
			}
		} else {
			lines = append(lines, prefix+num+" "+label)
		}
	}

	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderPipelineEditView(width int, header string) string {
	hint := subtleStyle.Render("j/k step · tab field · enter commit · d delete step · esc cancel")
	if m.providers.pipelineEditStep == -1 {
		hint = subtleStyle.Render("type name · tab next · enter save · esc cancel")
	} else if m.providers.pipelineEditStep == len(m.providers.pipelineDraftSteps) {
		hint = subtleStyle.Render("enter add step · k back · esc cancel")
	}
	lines := []string{header, hint, renderDivider(width - 2), ""}

	nameLabel := "name: "
	if m.providers.pipelineEditStep == -1 {
		nameLabel = accentStyle.Render("▶ name: ") + accentStyle.Render(m.providers.pipelineDraftName)
	} else {
		nameLabel += subtleStyle.Render(m.providers.pipelineDraftName)
	}
	lines = append(lines, "  "+nameLabel)
	lines = append(lines, "")

	steps := m.providers.pipelineDraftSteps
	if len(steps) > 0 {
		lines = append(lines, "  "+sectionTitleStyle.Render(fmt.Sprintf("Steps (%d)", len(steps))))
	}

	for i, step := range steps {
		selected := i == m.providers.pipelineEditStep
		prefix := "    "
		stepLabel := fmt.Sprintf("%d. ", i+1)
		if selected {
			prefix = "  " + accentStyle.Render("▶ ")
			stepLabel = accentStyle.Render(stepLabel)
		}
		provider := step.Provider
		model := step.Model
		if selected {
			if m.providers.pipelineEditField == 0 {
				provider = accentStyle.Render(provider)
				model = subtleStyle.Render(model)
			} else {
				provider = subtleStyle.Render(provider)
				model = accentStyle.Render(model)
			}
			if m.providers.pipelineDraftBuf != "" {
				if m.providers.pipelineEditField == 0 {
					provider = accentStyle.Render(m.providers.pipelineDraftBuf)
				} else {
					model = accentStyle.Render(m.providers.pipelineDraftBuf)
				}
			}
		} else {
			provider = subtleStyle.Render(provider)
			model = subtleStyle.Render(model)
		}
		lines = append(lines, prefix+stepLabel+provider+" / "+model)
	}
	// "+ Add Step" pseudo-row
	if m.providers.pipelineEditStep == len(steps) {
		lines = append(lines, "  "+accentStyle.Render("▶ + Add Step"))
	} else {
		lines = append(lines, "    "+subtleStyle.Render("+ Add Step"))
	}
	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}

// refreshProvidersRows re-reads the router and stamps the fresh rows
// into the Model. Pure — invoked from 'r' and from the tab-switch
// first-activation path.
func (m Model) renderNewProviderView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "New Provider")
	hint := subtleStyle.Render("type name · enter create · esc cancel")
	lines := []string{header, hint, renderDivider(width - 2), ""}
	lines = append(lines, "  name: "+accentStyle.Render(m.providers.newProviderDraft))
	return strings.Join(lines, "\n")
}

func (m Model) renderProvidersMenu(width int) []string {
	if !m.providers.menuActive {
		return nil
	}
	labels := m.providers.menuLabels
	index := m.providers.menuIndex
	if len(labels) == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, "")

	// Title with target context
	title := "Actions"
	switch m.providers.viewMode {
	case "detail":
		title = "Actions for " + m.providers.detailProvider
	case "pipelines":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			title = "Actions for pipeline " + m.providers.pipelineNames[scroll]
		} else {
			title = "Pipeline Actions"
		}
	default:
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			title = "Actions for " + m.providers.rows[scroll].Name
		}
	}
	lines = append(lines, sectionTitleStyle.Render(title))

	disabled := m.providers.menuDisabled
	reasons := m.providers.menuDisabledReasons
	for i, label := range labels {
		num := fmt.Sprintf("%d. ", i+1)
			var prefix string
		l := label
		isDisabled := i < len(disabled) && disabled[i]
		isDanger := strings.Contains(strings.ToLower(label), "delete")
		if i == index {
			prefix = accentStyle.Render("▶ ") + accentStyle.Render(num)
			if isDisabled {
				l = disabledStyle.Render(l)
			} else if isDanger {
				l = failStyle.Render(l)
			} else {
				l = accentStyle.Render(l)
			}
		} else {
			prefix = "   " + subtleStyle.Render(num)
			if isDisabled {
				l = disabledStyle.Render(l)
			} else if isDanger {
				l = warnStyle.Render(l)
			} else {
				l = subtleStyle.Render(l)
			}
		}
		if isDisabled && i < len(reasons) && reasons[i] != "" {
			l += subtleStyle.Render(" (" + reasons[i] + ")")
		}
		lines = append(lines, prefix+l)
	}

	hint := "j/k scroll · 1-9 jump · enter select · esc cancel"
	lines = append(lines, subtleStyle.Render("  "+hint))
	return lines
}

func (m Model) renderProvidersConfirm(width int) string {
	width = clampInt(width, 24, 1000)
	lines := []string{""}

	var icon, question string
	switch m.providers.confirmAction {
	case "delete_provider":
		icon = warnStyle.Render("⚠")
		question = fmt.Sprintf("Delete provider %s?", accentStyle.Render(m.providers.confirmTarget))
	case "delete_model":
		icon = warnStyle.Render("⚠")
		question = fmt.Sprintf("Delete model %s from %s?",
			accentStyle.Render(m.providers.confirmTarget),
			subtleStyle.Render(m.providers.detailProvider))
	case "delete_pipeline":
		icon = warnStyle.Render("⚠")
		question = fmt.Sprintf("Delete pipeline %s?", accentStyle.Render(m.providers.confirmTarget))
	default:
		icon = subtleStyle.Render("?")
		question = "Are you sure?"
	}

	lines = append(lines, "  "+icon+"  "+question)
	if m.providers.confirmAction == "delete_provider" {
		if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, m.providers.confirmTarget) {
			lines = append(lines, "     "+warnStyle.Render("→ currently set as primary"))
		}
		if m.eng != nil {
			for _, fb := range m.eng.Config.Providers.Fallback {
				if strings.EqualFold(fb, m.providers.confirmTarget) {
					lines = append(lines, "     "+warnStyle.Render("→ in fallback chain"))
					break
				}
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, "     "+okStyle.Render("y")+subtleStyle.Render(" confirm  ")+
		failStyle.Render("n")+subtleStyle.Render(" cancel"))

	content := strings.Join(lines, "\n")
	// Frame the dialog with a warning-colored border
	frameStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorWarn).
		Padding(0, 1).
		Width(width - 4)
	return frameStyle.Render(content)
}

