package tui

// providers.go — the Providers panel surfaces the provider router state
// that is otherwise invisible: which providers are registered, which
// have live API keys (vs. a placeholder shim), their max_context, tool
// capability, and the fallback chain the router would walk for a given
// request. This is the "why did my Ask land on offline?" diagnostic.
//
// Shape: a list of providerRow cached on the Model, a scroll offset,
// and optional error text. Computation is synchronous (a map walk
// against router.List() + Hints()), so there is no async load; r
// re-reads in case the user rotated keys or ran `config sync-models`.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// providerRow is one row in the panel — a shaped snapshot of what a
// registered Provider's Hints() + MaxContext() + Model() report, plus
// the derived "ready / no-key / offline" status string that distills
// the three classes (real, placeholder, offline) into one tag.
type providerRow struct {
	Name          string
	Model         string
	Models        []string // ordered model list for cycling
	MaxContext    int
	ToolStyle     string
	SupportsTools bool
	BestFor       []string
	IsOffline     bool
	IsPrimary     bool
	Status        string // "ready" | "offline" | "no-key"
}

// providerStatusTag derives the short status label from a Provider's
// Hints. Offline is detected by the ToolStyle="none" + BestFor hint
// that OfflineProvider sets; missing keys are detected by
// SupportsTools==false on a non-offline provider (PlaceholderProvider
// returns the zero value for SupportsTools).
func providerStatusTag(name string, supportsTools bool) (status string, isOffline bool) {
	if strings.EqualFold(name, "offline") {
		return "offline", true
	}
	if supportsTools {
		return "ready", false
	}
	return "no-key", false
}

func statusPriority(status string) int {
	switch strings.ToLower(status) {
	case "ready":
		return 0
	case "no-key":
		return 1
	case "offline":
		return 2
	default:
		return 3
	}
}

// collectProviderRows walks the registered providers and shapes them
// into rows sorted by status (ready → no-key → offline) then name.
func collectProviderRows(eng *engine.Engine) []providerRow {
	if eng == nil || eng.Providers == nil {
		return nil
	}
	names := eng.Providers.List()
	primary := strings.ToLower(strings.TrimSpace(eng.Config.Providers.Primary))

	rows := make([]providerRow, 0, len(names))
	for _, name := range names {
		p, ok := eng.Providers.Get(name)
		if !ok {
			continue
		}
		hints := p.Hints()
		status, isOffline := providerStatusTag(name, hints.SupportsTools)
		rows = append(rows, providerRow{
			Name:          name,
			Model:         p.Model(),
			Models:        p.Models(),
			MaxContext:    p.MaxContext(),
			ToolStyle:     hints.ToolStyle,
			SupportsTools: hints.SupportsTools,
			BestFor:       append([]string(nil), hints.BestFor...),
			IsOffline:     isOffline,
			IsPrimary:     strings.EqualFold(name, primary),
			Status:        status,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		pi, pj := statusPriority(rows[i].Status), statusPriority(rows[j].Status)
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}

// resolveProviderOrder returns the fallback chain the router would walk
// for a request that does not name an explicit provider. Thin wrapper
// so the panel can render it without depending on provider.Router.
func resolveProviderOrder(eng *engine.Engine) []string {
	if eng == nil || eng.Providers == nil {
		return nil
	}
	return eng.Providers.ResolveOrder("")
}

// providerStatusStyle picks the colour for the status tag so the eye
// catches "no-key" before reading the label.
func providerStatusStyle(status string) string {
	switch strings.ToLower(status) {
	case "ready":
		return accentStyle.Render("READY")
	case "offline":
		return subtleStyle.Render("OFFLINE")
	case "no-key":
		return warnStyle.Render("NO-KEY")
	default:
		return subtleStyle.Render(strings.ToUpper(status))
	}
}

// formatProviderRow renders one line. Shape:
// `▶ READY  anthropic  claude-opus-4   max=200000  tools=on  tool-style`.
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
func formatProviderRowNumbered(row providerRow, num int, selected bool, fallbackSet map[string]bool, width int) string {
	marker := "  "
	if selected {
		marker = accentStyle.Render("▶ ")
	}
	tag := providerStatusStyle(row.Status)
	name := row.Name
	if row.IsPrimary {
		name = accentStyle.Render(name) + subtleStyle.Render("*")
	} else if fallbackSet[strings.ToLower(strings.TrimSpace(row.Name))] {
		name = subtleStyle.Render(name) + subtleStyle.Render("↓")
	}

	keyBadge := subtleStyle.Render("[key:--]")
	if row.Status == "ready" {
		keyBadge = accentStyle.Render("[key:ok]")
	}

	model := row.Model
	if strings.TrimSpace(model) == "" {
		model = "(no model)"
	}
	line := fmt.Sprintf("%s%d. %s  %s  %s  %s  %s  %s",
		marker, num, tag, name,
		subtleStyle.Render(model),
		keyBadge,
		subtleStyle.Render(fmt.Sprintf("max=%d", row.MaxContext)),
		subtleStyle.Render(fmt.Sprintf("tools=%v", row.SupportsTools)),
	)
	if row.ToolStyle != "" {
		line += "  " + subtleStyle.Render(row.ToolStyle)
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatProviderDetail renders the selected row's extended info beneath the list.
func formatProviderDetail(row providerRow, width int) []string {
	out := []string{"  " + subtleStyle.Render("detail")}
	head := row.Name
	if row.IsPrimary {
		head = accentStyle.Render(row.Name) + subtleStyle.Render(" · primary")
	}
	out = append(out, "    "+head)
	out = append(out, "    "+subtleStyle.Render(fmt.Sprintf(
		"model=%s  max_context=%d  tool_style=%s  tools=%v",
		nonEmpty(row.Model, "(none)"), row.MaxContext, nonEmpty(row.ToolStyle, "(none)"), row.SupportsTools,
	)))
	if len(row.BestFor) > 0 {
		out = append(out, "    "+subtleStyle.Render("best_for: ")+strings.Join(row.BestFor, ", "))
	}
	switch row.Status {
	case "no-key":
		out = append(out, "    "+warnStyle.Render("missing API key — set the env var or providers.profiles entry."))
	case "offline":
		out = append(out, "    "+subtleStyle.Render("offline provider — deterministic fallback, no network."))
	}
	_ = width
	return out
}

func (m Model) renderProvidersView(width int) string {
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
	hint := subtleStyle.Render("j/k scroll · enter menu · g/G top/bottom")
	header := sectionHeader("⚑", "Providers")

	rows := m.providers.rows
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
		lines = append(lines, subtleStyle.Render("fallback chain: ")+strings.Join(numbered, subtleStyle.Render(" → ")))
	}
	lines = append(lines, renderDivider(width-2))

	if m.providers.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.providers.err))
		return strings.Join(lines, "\n")
	}

	if len(rows) == 0 {
		lines = append(lines,
			"",
			subtleStyle.Render("No providers registered — engine is in degraded startup."),
			subtleStyle.Render("Press enter for the actions menu once the store is available."),
		)
		return strings.Join(lines, "\n")
	}

	readyCount := 0
	noKeyCount := 0
	for _, r := range rows {
		switch r.Status {
		case "ready":
			readyCount++
		case "no-key":
			noKeyCount++
		}
	}
	summary := fmt.Sprintf("%d providers · %d ready · %d missing keys", len(rows), readyCount, noKeyCount)
	lines = append(lines, subtleStyle.Render(summary), "")

	// Build fallback set for markers
	fallbackSet := map[string]bool{}
	if m.eng != nil {
		for _, n := range m.eng.FallbackProviders() {
			fallbackSet[strings.ToLower(strings.TrimSpace(n))] = true
		}
	}

	scroll := clampScroll(m.providers.scroll, len(rows))
	for i, row := range rows {
		selected := i == scroll
		lines = append(lines, formatProviderRowNumbered(row, i+1, selected, fallbackSet, width-2))
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
	hint := subtleStyle.Render("j/k scroll model list · enter menu · esc/q back")
	if m.providers.modelPickerActive {
		if m.providers.modelPickerManual {
			hint = subtleStyle.Render("type model · enter confirm · esc cancel")
		} else {
			hint = subtleStyle.Render("j/k scroll · enter pick · m manual · esc cancel")
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
	if strings.TrimSpace(prof.APIKey) != "" {
		badges = append(badges, accentStyle.Render("[key:set]"))
	} else {
		badges = append(badges, warnStyle.Render("[key:missing]"))
	}
	if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		badges = append(badges, accentStyle.Render("[primary]"))
	}
	if m.eng != nil {
		for _, fb := range m.eng.FallbackProviders() {
			if strings.EqualFold(fb, name) {
				badges = append(badges, subtleStyle.Render("[fallback]"))
				break
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, accentStyle.Render(name))
	lines = append(lines, "  "+strings.Join(badges, "  "))

	// Profile fields
	protocol := nonEmpty(prof.Protocol, "(auto)")
	baseURL := nonEmpty(prof.BaseURL, "(default)")
	maxContext := fmt.Sprintf("%d", prof.MaxContext)
	maxTokens := fmt.Sprintf("%d", prof.MaxTokens)
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
			label := f.label
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
		lines = append(lines, "")
		lines = append(lines, "  "+subtleStyle.Render("protocol:")+" "+protocol)
		lines = append(lines, "  "+subtleStyle.Render("base_url:")+" "+baseURL)
		lines = append(lines, "  "+subtleStyle.Render("max_context:")+" "+maxContext)
		lines = append(lines, "  "+subtleStyle.Render("max_tokens:")+" "+maxTokens)
	}

	// Models section with numbered list and scroll window
	models := prof.AllModels()
	lines = append(lines, "")
	lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf("Models (%d)", len(models))))
	if len(models) == 0 {
		lines = append(lines, "  "+subtleStyle.Render("(no models configured — use Add Model from the menu)"))
	} else {
		selectedIdx := m.providers.modelEditIdx
		if m.providers.editMode != "model" {
			selectedIdx = 0
		}
		// Show a window of up to 12 models around the selection
		const modelWindow = 12
		start := 0
		if selectedIdx >= modelWindow {
			start = selectedIdx - modelWindow + 1
		}
		end := start + modelWindow
		if end > len(models) {
			end = len(models)
		}
		if start > 0 {
			lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("... %d more above", start)))
		}
		for i := start; i < end; i++ {
			model := models[i]
			num := fmt.Sprintf("%2d.", i+1)
			prefix := "  " + num + " "
			label := model
			if m.providers.editMode == "model" && i == m.providers.modelEditIdx {
				prefix = accentStyle.Render("▶ ") + num + " "
				label = accentStyle.Render(label)
				if i == 0 {
					label += subtleStyle.Render(" ← active")
				}
			} else if i == 0 {
				label = label + subtleStyle.Render(" ← active")
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
			}
			for i, item := range items {
				prefix := "    "
				label := item
				if i == m.providers.modelPickerIndex {
					prefix = accentStyle.Render("▶ ")
					label = accentStyle.Render(label)
				}
				lines = append(lines, prefix+label)
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

	hint := subtleStyle.Render("j/k scroll · enter menu · g/G top/bottom · esc/q back")
	lines := []string{header, hint, renderDivider(width - 2)}

	names := m.providers.pipelineNames
	if len(names) == 0 {
		lines = append(lines, "",
			subtleStyle.Render("No pipelines configured."),
			subtleStyle.Render("Press enter for the actions menu to create a pipeline."),
		)
		return strings.Join(lines, "\n")
	}

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
		lines = append(lines, prefix+num+" "+label)

		if m.eng != nil {
			if pipe, ok := m.eng.Pipeline(name); ok {
				if len(pipe.Steps) > 0 {
					var stepStrs []string
					for j, step := range pipe.Steps {
						stepStrs = append(stepStrs, fmt.Sprintf("%d.%s/%s", j+1, step.Provider, step.Model))
					}
					chain := strings.Join(stepStrs, subtleStyle.Render(" → "))
					lines = append(lines, "    "+subtleStyle.Render("chain: ")+chain)
					if len(pipe.Steps) > 1 {
						fbStrs := []string{}
						for j := 1; j < len(pipe.Steps); j++ {
							fbStrs = append(fbStrs, fmt.Sprintf("%d.%s/%s", j+1, pipe.Steps[j].Provider, pipe.Steps[j].Model))
						}
						lines = append(lines, "    "+subtleStyle.Render("fallback: ")+strings.Join(fbStrs, subtleStyle.Render(" → ")))
					}
				}
			}
		}
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderPipelineEditView(width int, header string) string {
	hint := subtleStyle.Render("j/k step · tab field · enter commit · esc cancel")
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
func (m Model) refreshProvidersRows() Model {
	rows := collectProviderRows(m.eng)
	m.providers.rows = rows
	if m.eng == nil {
		m.providers.err = "engine not ready (degraded startup)"
	} else if len(rows) == 0 {
		m.providers.err = "router has no providers"
	} else {
		m.providers.err = ""
	}
	m.providers.scroll = clampScroll(m.providers.scroll, len(rows))
	return m
}

func (m Model) focusProviderRow(provider string) Model {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return m
	}
	for i, row := range m.providers.rows {
		if strings.EqualFold(strings.TrimSpace(row.Name), provider) {
			m.providers.scroll = i
			return m
		}
	}
	return m
}

func isProvidersInputMode(m Model) bool {
	return m.providers.newProviderDraft != "" ||
		m.providers.profileEditMode ||
		m.providers.modelPickerManual ||
		m.providers.pipelineEditMode
}

func (m Model) handleProvidersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Input modes (typing) bypass menus and global shortcuts
	if isProvidersInputMode(m) {
		switch m.providers.viewMode {
		case "new_provider":
			return m.handleNewProviderKey(msg)
		case "pipelines":
			return m.handlePipelineEditKey(msg)
		case "detail":
			if m.providers.profileEditMode {
				return m.handleProfileEditKey(msg)
			}
			if m.providers.modelPickerManual {
				return m.handleModelPickerKey(msg)
			}
		}
		return m, nil
	}

	if m.providers.menuActive {
		return m.handleProvidersMenuKey(msg)
	}

	switch m.providers.viewMode {
	case "detail":
		if m.providers.modelPickerActive && !m.providers.modelPickerManual {
			return m.handleModelPickerKey(msg)
		}
		return m.handleProvidersDetailKey(msg)
	case "pipelines":
		return m.handleProvidersPipelineKey(msg)
	case "new_provider":
		return m.handleNewProviderKey(msg)
	default:
		return m.handleProvidersListKey(msg)
	}
}

func (m Model) handleProvidersListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.providers.rows)
	step := 1
	pageStep := 10

	switch msg.String() {
	case "j", "down":
		if m.providers.scroll+step < total {
			m.providers.scroll += step
		}
	case "k", "up":
		if m.providers.scroll >= step {
			m.providers.scroll -= step
		} else {
			m.providers.scroll = 0
		}
	case "pgdown":
		if m.providers.scroll+pageStep < total {
			m.providers.scroll += pageStep
		} else if total > 0 {
			m.providers.scroll = total - 1
		}
	case "pgup":
		if m.providers.scroll >= pageStep {
			m.providers.scroll -= pageStep
		} else {
			m.providers.scroll = 0
		}
	case "g":
		m.providers.scroll = 0
	case "G":
		if total > 0 {
			m.providers.scroll = total - 1
		}
	case "enter":
		labels, actions, disabled := m.buildListMenu()
		if len(labels) > 0 {
			m.providers.menuActive = true
			m.providers.menuLabels = labels
			m.providers.menuActions = actions
			m.providers.menuDisabled = disabled
			m.providers.menuIndex = 0
		}
	}
	return m, nil
}

func (m Model) handleProvidersDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.providers.viewMode = "list"
		m.providers.detailProvider = ""
	case "j", "down":
		if m.eng == nil || m.eng.Config == nil {
			return m, nil
		}
		prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
		if !ok {
			return m, nil
		}
		models := prof.AllModels()
		if len(models) == 0 {
			return m, nil
		}
		if m.providers.modelEditIdx+1 < len(models) {
			m.providers.modelEditIdx++
		}
	case "k", "up":
		if m.eng == nil || m.eng.Config == nil {
			return m, nil
		}
		prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
		if !ok {
			return m, nil
		}
		models := prof.AllModels()
		if len(models) == 0 {
			return m, nil
		}
		if m.providers.modelEditIdx > 0 {
			m.providers.modelEditIdx--
		}
	case "enter":
		labels, actions, disabled := m.buildDetailMenu()
		if len(labels) > 0 {
			m.providers.menuActive = true
			m.providers.menuLabels = labels
			m.providers.menuActions = actions
			m.providers.menuDisabled = disabled
			m.providers.menuIndex = 0
		}
	}
	return m, nil
}

func (m Model) handleModelPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.modelPickerManual {
		switch msg.String() {
		case "esc":
			m.providers.modelPickerActive = false
			m.providers.modelPickerDraft = ""
		case "enter":
			draft := strings.TrimSpace(m.providers.modelPickerDraft)
			if draft != "" {
				m.addModelToProvider(m.providers.detailProvider, draft)
			}
			m.providers.modelPickerActive = false
			m.providers.modelPickerDraft = ""
		case "backspace":
			if len(m.providers.modelPickerDraft) > 0 {
				m.providers.modelPickerDraft = m.providers.modelPickerDraft[:len(m.providers.modelPickerDraft)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.providers.modelPickerDraft += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.providers.modelPickerActive = false
		m.providers.modelPickerDraft = ""
	case "enter":
		items := m.providers.modelPickerItems
		if m.providers.modelPickerIndex >= 0 && m.providers.modelPickerIndex < len(items) {
			m.addModelToProvider(m.providers.detailProvider, items[m.providers.modelPickerIndex])
		}
		m.providers.modelPickerActive = false
	case "j", "down":
		if m.providers.modelPickerIndex+1 < len(m.providers.modelPickerItems) {
			m.providers.modelPickerIndex++
		}
	case "k", "up":
		if m.providers.modelPickerIndex > 0 {
			m.providers.modelPickerIndex--
		}
	case "m":
		m.providers.modelPickerManual = true
		m.providers.modelPickerDraft = ""
	}
	return m, nil
}

func (m *Model) addModelToProvider(provider, model string) {
	if m.eng == nil || m.eng.Config == nil {
		return
	}
	prof, ok := m.eng.Config.Providers.Profiles[provider]
	if !ok {
		return
	}
	models := prof.AllModels()
	prof.Models = append(models, model)
	prof.Model = prof.Models[0]
	m.eng.Config.Providers.Profiles[provider] = prof
	m.notice = fmt.Sprintf("added model %s to %s", model, provider)
}

func (m Model) loadModelsDevForProvider(providerName string) []string {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	catalog, err := config.LoadModelsDevCatalog(config.ModelsDevCachePath())
	if err != nil {
		return nil
	}
	alias := ""
	for name, id := range config.ModelsDevProviderAliases() {
		if strings.EqualFold(name, providerName) {
			alias = id
			break
		}
	}
	if alias == "" {
		return nil
	}
	p, ok := catalog[alias]
	if !ok {
		return nil
	}
	var items []string
	for id := range p.Models {
		items = append(items, id)
	}
	sort.Strings(items)
	return items
}

func (m Model) handleProvidersPipelineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.pipelineEditMode {
		return m.handlePipelineEditKey(msg)
	}

	names := m.providers.pipelineNames
	total := len(names)
	switch msg.String() {
	case "esc", "q":
		m.providers.viewMode = "list"
	case "j", "down":
		if m.providers.pipelineScroll+1 < total {
			m.providers.pipelineScroll++
		}
	case "k", "up":
		if m.providers.pipelineScroll > 0 {
			m.providers.pipelineScroll--
		}
	case "g":
		m.providers.pipelineScroll = 0
	case "G":
		if total > 0 {
			m.providers.pipelineScroll = total - 1
		}
	case "enter":
		labels, actions, disabled := m.buildPipelineMenu()
		if len(labels) > 0 {
			m.providers.menuActive = true
			m.providers.menuLabels = labels
			m.providers.menuActions = actions
			m.providers.menuDisabled = disabled
			m.providers.menuIndex = 0
		}
	}
	return m, nil
}

func (m Model) handlePipelineEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	steps := m.providers.pipelineDraftSteps
	stepIdx := m.providers.pipelineEditStep
	field := m.providers.pipelineEditField

	switch msg.String() {
	case "esc":
		m.providers.pipelineEditMode = false
		m.providers.pipelineDraftName = ""
		m.providers.pipelineDraftSteps = nil
		m.providers.pipelineDraftBuf = ""
		m.notice = "pipeline edit cancelled"
	case "enter":
		if stepIdx == len(steps) {
			// "+ Add Step" pseudo-row selected
			m.providers.pipelineDraftSteps = append(steps, config.PipelineStep{})
			m.providers.pipelineEditStep = len(steps)
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
			return m, nil
		}
		if stepIdx == -1 {
			// name field active, move to first step
			if m.providers.pipelineDraftBuf != "" {
				m.providers.pipelineDraftName = m.providers.pipelineDraftBuf
				m.providers.pipelineDraftBuf = ""
			}
			if len(steps) > 0 {
				m.providers.pipelineEditStep = 0
				m.providers.pipelineEditField = 0
			} else {
				// no steps, save immediately
				if err := m.savePipelineDraft(); err != nil {
					m.notice = "save failed: " + err.Error()
				}
			}
			return m, nil
		}
		// commit current field buffer
		if stepIdx >= 0 && stepIdx < len(steps) {
			if field == 0 {
				steps[stepIdx].Provider = m.providers.pipelineDraftBuf
			} else {
				steps[stepIdx].Model = m.providers.pipelineDraftBuf
			}
			m.providers.pipelineDraftBuf = ""
		}
		// if last field of last step, save; else next field
		if stepIdx == len(steps)-1 && field == 1 {
			if err := m.savePipelineDraft(); err != nil {
				m.notice = "save failed: " + err.Error()
			}
		} else if field == 0 {
			m.providers.pipelineEditField = 1
		} else {
			m.providers.pipelineEditStep = stepIdx + 1
			m.providers.pipelineEditField = 0
		}
	case "tab":
		if stepIdx == -1 {
			if m.providers.pipelineDraftBuf != "" {
				m.providers.pipelineDraftName = m.providers.pipelineDraftBuf
				m.providers.pipelineDraftBuf = ""
			}
			if len(steps) > 0 {
				m.providers.pipelineEditStep = 0
				m.providers.pipelineEditField = 0
			} else {
				// no steps yet, jump to add row
				m.providers.pipelineEditStep = 0
				m.providers.pipelineEditField = 0
			}
			return m, nil
		}
		if stepIdx >= 0 && stepIdx < len(steps) {
			if m.providers.pipelineDraftBuf != "" {
				if field == 0 {
					steps[stepIdx].Provider = m.providers.pipelineDraftBuf
				} else {
					steps[stepIdx].Model = m.providers.pipelineDraftBuf
				}
				m.providers.pipelineDraftBuf = ""
			}
			if field == 0 {
				m.providers.pipelineEditField = 1
			} else if stepIdx < len(steps)-1 {
				m.providers.pipelineEditStep = stepIdx + 1
				m.providers.pipelineEditField = 0
			} else {
				// last field of last step → add row
				m.providers.pipelineEditStep = len(steps)
				m.providers.pipelineEditField = 0
			}
		}
	case "j", "down":
		if stepIdx < len(steps) {
			m.providers.pipelineEditStep = stepIdx + 1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
		}
	case "k", "up":
		if stepIdx > -1 {
			m.providers.pipelineEditStep = stepIdx - 1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
		}
	case "backspace":
		if len(m.providers.pipelineDraftBuf) > 0 {
			m.providers.pipelineDraftBuf = m.providers.pipelineDraftBuf[:len(m.providers.pipelineDraftBuf)-1]
		} else if stepIdx >= 0 && stepIdx < len(steps) {
			// delete current step when buffer is empty
			m.providers.pipelineDraftSteps = append(steps[:stepIdx], steps[stepIdx+1:]...)
			if stepIdx >= len(m.providers.pipelineDraftSteps) {
				m.providers.pipelineEditStep = len(m.providers.pipelineDraftSteps) - 1
			}
			if m.providers.pipelineEditStep < -1 {
				m.providers.pipelineEditStep = -1
			}
			m.providers.pipelineEditField = 0
		}
	default:
		// typing into active field
		if msg.Type == tea.KeyRunes {
			m.providers.pipelineDraftBuf += string(msg.Runes)
		}
	}
	return m, nil
}

func (m *Model) savePipelineDraft() error {
	name := strings.TrimSpace(m.providers.pipelineDraftName)
	if name == "" {
		return fmt.Errorf("pipeline name is required")
	}
	if len(m.providers.pipelineDraftSteps) == 0 {
		return fmt.Errorf("pipeline needs at least one step")
	}
	for i, step := range m.providers.pipelineDraftSteps {
		if strings.TrimSpace(step.Provider) == "" {
			return fmt.Errorf("step %d provider is required", i+1)
		}
		if strings.TrimSpace(step.Model) == "" {
			return fmt.Errorf("step %d model is required", i+1)
		}
	}
	path, err := m.persistPipelinesProjectConfig(name, m.providers.pipelineDraftSteps)
	if err != nil {
		return err
	}
	m.providers.pipelineEditMode = false
	m.providers.pipelineDraftName = ""
	m.providers.pipelineDraftSteps = nil
	m.providers.pipelineDraftBuf = ""
	m.providers.pipelineNames = m.pipelineNamesFromEngine()
	m.notice = "saved pipeline to " + path
	return nil
}

func (m *Model) deletePipeline(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("pipeline name is empty")
	}
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return err
	}
	cfg := m.eng.Config
	if cfg.Pipelines == nil {
		return fmt.Errorf("pipeline not found")
	}
	delete(cfg.Pipelines, name)
	if err := cfg.Save(path); err != nil {
		return err
	}
	if err := m.reloadEngineConfig(); err != nil {
		return err
	}
	if m.providers.activePipeline == name {
		m.providers.activePipeline = ""
	}
	return nil
}

func (m Model) pipelineNamesFromEngine() []string {
	if m.eng == nil {
		return nil
	}
	return m.eng.PipelineNames()
}

// handleStatsPanelProviderKey processes keystrokes when the F2 stats panel
// is locked to the providers sub-mode (alt+p while on chat tab). It supports:
// j/k — move cursor through provider list
// enter — switch to the selected provider
// m — cycle preferred model for the selected profile
// f — cycle fallback model index
// s — save provider config to project .dfmc/config.yaml
// g/G — jump to first/last provider
func (m Model) handleStatsPanelProviderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ensure rows are populated
	panelRows := m.providerPanelRows()
	if len(panelRows) == 0 {
		return m, nil
	}

	total := len(panelRows)
	step := 1

	switch msg.String() {
	case "j", "down":
		if m.providers.editMode == "" {
			if m.providers.selectedIndex+step < total {
				m.providers.selectedIndex += step
			}
		}
	case "k", "up":
		if m.providers.editMode == "" {
			if m.providers.selectedIndex >= step {
				m.providers.selectedIndex -= step
			} else {
				m.providers.selectedIndex = 0
			}
		}
	case "g":
		if m.providers.editMode == "" {
			m.providers.selectedIndex = 0
		}
	case "G":
		if m.providers.editMode == "" && total > 0 {
			m.providers.selectedIndex = total - 1
		}
	case "enter":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			if m.providers.editMode == "model" {
				// commit model edit
				if len(row.Models) > 0 {
					model := row.Models[m.providers.modelEditIdx]
					m = m.applyProviderModelSelection(row.Name, model)
					m.notice = formatProviderSwitchNotice(m.providerProfile(row.Name))
				}
				m.providers.editMode = ""
			} else if m.providers.editMode == "fallback" {
				// commit fallback edit
				m.providers.editMode = ""
				m.notice = "fallback profile for " + row.Name + " updated"
			} else {
				// switch to this provider (use best model)
				model := ""
				if len(row.Models) > 0 {
					model = row.Models[0]
				}
				m = m.applyProviderModelSelection(row.Name, model)
				m.notice = formatProviderSwitchNotice(m.providerProfile(row.Name))
			}
		}
	case "m":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			if len(row.Models) > 1 {
				if m.providers.editMode == "model" {
					m.providers.modelEditIdx = (m.providers.modelEditIdx + 1) % len(row.Models)
				} else {
					m.providers.editMode = "model"
					m.providers.modelEditIdx = 0
				}
			} else {
				m.notice = row.Name + " has no additional models to cycle"
			}
		}
	case "f":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			if len(row.FallbackModels) > 0 {
				if m.providers.editMode == "fallback" {
					m.providers.fallbackIdx = (m.providers.fallbackIdx + 1) % len(row.FallbackModels)
				} else {
					m.providers.editMode = "fallback"
					m.providers.fallbackIdx = 0
				}
			} else {
				m.notice = row.Name + " has no fallback models configured"
			}
		}
	case "s":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			model := ""
			if len(row.Models) > 0 {
				model = row.Models[0]
			}
			path, err := m.persistProviderModelProjectConfig(row.Name, model)
			if err != nil {
				m.notice = "save failed: " + err.Error()
			} else {
				m.notice = "saved " + path
			}
		}
	}
	return m, nil
}

type syncModelsDevMsg struct {
	path    string
	changes []string
	err     error
}

func syncModelsDevCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Config == nil {
			return syncModelsDevMsg{err: fmt.Errorf("engine not ready")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		catalog, err := config.FetchModelsDevCatalog(ctx, config.DefaultModelsDevAPIURL)
		if err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("fetch: %w", err)}
		}
		if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("cache: %w", err)}
		}

		cwd := eng.ProjectRoot
		if cwd == "" {
			cwd = "."
		}
		path := filepath.Join(cwd, config.DefaultDirName, "config.yaml")

		cloned, err := config.LoadWithOptions(config.LoadOptions{CWD: cwd})
		if err != nil {
			cloned = config.DefaultConfig()
		}
		before := map[string]config.ModelConfig{}
		for n, p := range cloned.Providers.Profiles {
			before[n] = p
		}
		cloned.Providers.Profiles = config.MergeProviderProfilesFromModelsDev(cloned.Providers.Profiles, catalog, config.ModelsDevMergeOptions{RewriteBaseURL: true})
		if err := cloned.Save(path); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("save: %w", err)}
		}
		if err := eng.ReloadConfig(cwd); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("reload: %w", err)}
		}

		var changes []string
		for n, after := range cloned.Providers.Profiles {
			beforeProf, hadBefore := before[n]
			if !hadBefore {
				changes = append(changes, fmt.Sprintf("+%s (new)", n))
				continue
			}
			if beforeProf.Model != after.Model {
				changes = append(changes, fmt.Sprintf("~%s model %s → %s", n, beforeProf.Model, after.Model))
			}
			if beforeProf.MaxContext != after.MaxContext {
				changes = append(changes, fmt.Sprintf("~%s context %d → %d", n, beforeProf.MaxContext, after.MaxContext))
			}
		}
		return syncModelsDevMsg{path: path, changes: changes}
	}
}

func (m Model) renderNewProviderView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "New Provider")
	hint := subtleStyle.Render("type name · enter create · esc cancel")
	lines := []string{header, hint, renderDivider(width - 2), ""}
	lines = append(lines, "  name: "+accentStyle.Render(m.providers.newProviderDraft))
	return strings.Join(lines, "\n")
}

func (m Model) handleNewProviderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.providers.viewMode = "list"
		m.providers.newProviderDraft = ""
		m.notice = "new provider cancelled"
	case "enter":
		name := strings.TrimSpace(m.providers.newProviderDraft)
		if name == "" {
			m.notice = "provider name is required"
			return m, nil
		}
		if err := m.createProvider(name); err != nil {
			m.notice = "create failed: " + err.Error()
		} else {
			m.providers.newProviderDraft = ""
			m.providers.viewMode = "detail"
			m.providers.detailProvider = name
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(name)
			m.notice = "created provider: " + name
		}
	case "backspace":
		if len(m.providers.newProviderDraft) > 0 {
			m.providers.newProviderDraft = m.providers.newProviderDraft[:len(m.providers.newProviderDraft)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.providers.newProviderDraft += string(msg.Runes)
		}
	}
	return m, nil
}

func (m *Model) createProvider(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is empty")
	}
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	if _, exists := m.eng.Config.Providers.Profiles[name]; exists {
		return fmt.Errorf("provider %s already exists", name)
	}

	prof := config.ModelConfig{
		Protocol: "openai-compatible",
		Models:   []string{},
	}
	m.eng.Config.Providers.Profiles[name] = prof

	path, err := m.projectConfigPath()
	if err != nil {
		return err
	}
	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}
	profilesNode := ensureStringAnyMap(ensureStringAnyMap(doc, "providers"), "profiles")
	profileNode := ensureStringAnyMap(profilesNode, name)
	profileNode["protocol"] = prof.Protocol

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return m.reloadEngineConfig()
}

func (m *Model) deleteProvider(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is empty")
	}
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	delete(m.eng.Config.Providers.Profiles, name)

	path, err := m.projectConfigPath()
	if err != nil {
		return err
	}
	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	profilesNode := ensureStringAnyMap(ensureStringAnyMap(doc, "providers"), "profiles")
	delete(profilesNode, name)

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	if err := m.reloadEngineConfig(); err != nil {
		return err
	}
	if m.eng != nil {
		if strings.EqualFold(m.eng.Config.Providers.Primary, name) {
			m.eng.Config.Providers.Primary = ""
		}
		var newFallback []string
		for _, fb := range m.eng.Config.Providers.Fallback {
			if !strings.EqualFold(fb, name) {
				newFallback = append(newFallback, fb)
			}
		}
		m.eng.Config.Providers.Fallback = newFallback
	}
	return nil
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
	for i, label := range labels {
		num := fmt.Sprintf("%d. ", i+1)
		prefix := "   "
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
		lines = append(lines, prefix+l)
	}

	hint := "j/k scroll · 1-9 jump · enter select · esc cancel"
	lines = append(lines, subtleStyle.Render("  "+hint))
	return lines
}

func (m Model) buildListMenu() ([]string, []string, []bool) {
	var labels, actions []string
	var disabled []bool

	scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
	selectedName := ""
	if scroll >= 0 && scroll < len(m.providers.rows) {
		selectedName = m.providers.rows[scroll].Name
	}

	// --- Provider-specific actions ---
	if selectedName != "" {
		labels = append(labels, "View Detail")
		actions = append(actions, "detail")
		disabled = append(disabled, false)

		// Set Primary — context-aware label
		isPrimary := false
		if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, selectedName) {
			isPrimary = true
		}
		if isPrimary {
			labels = append(labels, "Already Primary")
		} else {
			labels = append(labels, "Set as Primary")
		}
		actions = append(actions, "set_primary")
		disabled = append(disabled, isPrimary)

		// Toggle Fallback — context-aware label
		inFallback := false
		if m.eng != nil {
			for _, fb := range m.eng.Config.Providers.Fallback {
				if strings.EqualFold(fb, selectedName) {
					inFallback = true
					break
				}
			}
		}
		if inFallback {
			labels = append(labels, "Remove from Fallback")
		} else {
			labels = append(labels, "Add to Fallback")
		}
		actions = append(actions, "toggle_fallback")
		disabled = append(disabled, false)

		labels = append(labels, "Cycle Model")
		actions = append(actions, "cycle_model")
		disabled = append(disabled, false)
		labels = append(labels, "Save Config")
		actions = append(actions, "save_config")
		disabled = append(disabled, false)
		labels = append(labels, "Delete Provider")
		actions = append(actions, "delete_provider")
		disabled = append(disabled, false)
	}

	// --- Global actions ---
	labels = append(labels, "Sync Models")
	actions = append(actions, "sync_models")
	disabled = append(disabled, false)
	labels = append(labels, "Pipelines")
	actions = append(actions, "pipelines")
	disabled = append(disabled, false)
	labels = append(labels, "New Provider")
	actions = append(actions, "new_provider")
	disabled = append(disabled, false)
	labels = append(labels, "Refresh")
	actions = append(actions, "refresh")
	disabled = append(disabled, false)

	return labels, actions, disabled
}

func (m Model) buildDetailMenu() ([]string, []string, []bool) {
	var labels, actions []string
	var disabled []bool
	name := m.providers.detailProvider

	// --- Profile ---
	labels = append(labels, "Edit Profile")
	actions = append(actions, "edit_profile")
	disabled = append(disabled, false)

	// --- Models ---
	labels = append(labels, "Add Model")
	actions = append(actions, "add_model")
	disabled = append(disabled, false)
	labels = append(labels, "Delete Active Model")
	actions = append(actions, "delete_model")
	disabled = append(disabled, false)

	// --- Routing ---
	isPrimary := false
	if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		isPrimary = true
	}
	if isPrimary {
		labels = append(labels, "Already Primary")
	} else {
		labels = append(labels, "Set as Primary")
	}
	actions = append(actions, "set_primary")
	disabled = append(disabled, isPrimary)

	inFallback := false
	if m.eng != nil {
		for _, fb := range m.eng.Config.Providers.Fallback {
			if strings.EqualFold(fb, name) {
				inFallback = true
				break
			}
		}
	}
	if inFallback {
		labels = append(labels, "Remove from Fallback")
	} else {
		labels = append(labels, "Add to Fallback")
	}
	actions = append(actions, "toggle_fallback")
	disabled = append(disabled, false)

	// --- Persistence ---
	labels = append(labels, "Save Config")
	actions = append(actions, "save_config")
	disabled = append(disabled, false)

	// --- Navigation ---
	labels = append(labels, "Back to List")
	actions = append(actions, "back")
	disabled = append(disabled, false)

	return labels, actions, disabled
}

func (m Model) buildPipelineMenu() ([]string, []string, []bool) {
	var labels, actions []string
	var disabled []bool

	scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
	if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
		name := m.providers.pipelineNames[scroll]
		if name == m.providers.activePipeline {
			labels = append(labels, "Already Active")
		} else {
			labels = append(labels, "Activate Pipeline")
		}
		actions = append(actions, "activate")
		disabled = append(disabled, name == m.providers.activePipeline)
		labels = append(labels, "Edit Pipeline")
		actions = append(actions, "edit")
		disabled = append(disabled, false)
		labels = append(labels, "Delete Pipeline")
		actions = append(actions, "delete")
		disabled = append(disabled, false)
	}

	labels = append(labels, "New Pipeline")
	actions = append(actions, "new")
	disabled = append(disabled, false)
	labels = append(labels, "Back to List")
	actions = append(actions, "back")
	disabled = append(disabled, false)

	return labels, actions, disabled
}

func (m Model) handleProvidersMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.providers.menuLabels)
	if total == 0 {
		m.providers.menuActive = false
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.providers.menuActive = false
	case "j", "down":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, m.providers.menuIndex, total, 1)
	case "k", "up":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, m.providers.menuIndex, total, -1)
	case "g":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, -1, total, 1)
	case "G":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, total, total, -1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n, _ := strconv.Atoi(msg.String())
		if n > 0 && n <= total {
			idx := n - 1
			if idx < len(m.providers.menuDisabled) && m.providers.menuDisabled[idx] {
				m.notice = "that action is not available"
			} else {
				m.providers.menuIndex = idx
			}
		}
	case "enter":
		if m.providers.menuIndex < len(m.providers.menuDisabled) && m.providers.menuDisabled[m.providers.menuIndex] {
			m.notice = "that action is not available"
			return m, nil
		}
		action := m.providers.menuActions[m.providers.menuIndex]
		m.providers.menuActive = false
		return m.executeMenuAction(action)
	}
	return m, nil
}

// nextEnabledMenuIndex searches for the nearest enabled menu item in the
// given direction (dir=1 forward, dir=-1 backward). If start is out of
// bounds it clamps to the edge before searching.
func nextEnabledMenuIndex(disabled []bool, start, total, dir int) int {
	if total == 0 {
		return 0
	}
	idx := start + dir
	for idx >= 0 && idx < total {
		if idx < len(disabled) && disabled[idx] {
			idx += dir
			continue
		}
		return idx
	}
	// All items in this direction are disabled — stay where we are.
	return start
}

func (m Model) executeMenuAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "detail":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			m.providers.viewMode = "detail"
			m.providers.detailProvider = m.providers.rows[scroll].Name
			m.providers.modelEditIdx = 0
		}
	case "back":
		m.providers.viewMode = "list"
		m.providers.detailProvider = ""
	case "set_primary":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			if m.eng != nil {
				m.eng.SetPrimaryProvider(name)
			}
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(name)
			m.notice = "set primary: " + name
		}
	case "toggle_fallback":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.toggleFallbackProvider(name)
		}
	case "cycle_model":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.cycleProviderModel(name)
		}
	case "save_config":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			model := m.providers.rows[scroll].Model
			path, err := m.persistProviderModelProjectConfig(name, model)
			if err != nil {
				m.notice = "save failed: " + err.Error()
			} else {
				m.notice = "saved " + path
			}
		}
	case "sync_models":
		return m, syncModelsDevCmd(m.eng)
	case "pipelines":
		m.providers.viewMode = "pipelines"
		m.providers.pipelineNames = m.pipelineNamesFromEngine()
		m.providers.pipelineScroll = 0
	case "new_provider":
		m.providers.viewMode = "new_provider"
		m.providers.newProviderDraft = ""
	case "delete_provider":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			if err := m.deleteProvider(name); err != nil {
				m.notice = "delete failed: " + err.Error()
			} else {
				m = m.refreshProvidersRows()
				m.notice = "deleted provider: " + name
			}
		}
	case "refresh":
		m = m.refreshProvidersRows()
	case "edit_profile":
		m.providers.profileEditMode = true
		m.providers.profileEditField = 0
		m.providers.profileEditDraft = ""
	case "add_model":
		m.providers.modelPickerActive = true
		m.providers.modelPickerManual = false
		m.providers.modelPickerIndex = 0
		m.providers.modelPickerDraft = ""
		if m.eng != nil {
			m.providers.modelPickerItems = m.loadModelsDevForProvider(m.providers.detailProvider)
		}
	case "delete_model":
		m = m.deleteActiveModel()
	case "activate":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			name := m.providers.pipelineNames[scroll]
			if m.eng != nil {
				if err := m.eng.ActivatePipeline(name); err != nil {
					m.notice = "pipeline failed: " + err.Error()
				} else {
					m.providers.activePipeline = name
					m.status = m.eng.Status()
					m.notice = "activated pipeline: " + name
				}
			}
		}
	case "edit":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			name := m.providers.pipelineNames[scroll]
			if m.eng != nil {
				if pipe, ok := m.eng.Pipeline(name); ok {
					m.providers.pipelineEditMode = true
					m.providers.pipelineDraftName = name
					m.providers.pipelineDraftSteps = append([]config.PipelineStep(nil), pipe.Steps...)
					m.providers.pipelineEditStep = -1
					m.providers.pipelineEditField = 0
					m.providers.pipelineDraftBuf = ""
				}
			}
		}
	case "delete":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			name := m.providers.pipelineNames[scroll]
			if err := m.deletePipeline(name); err != nil {
				m.notice = "delete failed: " + err.Error()
			} else {
				m.providers.pipelineNames = m.pipelineNamesFromEngine()
				m.providers.pipelineScroll = clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
				m.notice = "deleted pipeline: " + name
			}
		}
	case "new":
		m.providers.pipelineEditMode = true
		m.providers.pipelineDraftName = ""
		m.providers.pipelineDraftSteps = nil
		m.providers.pipelineEditStep = -1
		m.providers.pipelineEditField = 0
		m.providers.pipelineDraftBuf = ""
	}
	return m, nil
}

func (m Model) cycleProviderModel(name string) Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[name]
	if !ok {
		return m
	}
	models := prof.AllModels()
	if len(models) == 0 {
		m.notice = name + " has no models to cycle"
		return m
	}
	current := strings.TrimSpace(prof.Model)
	idx := 0
	for i, model := range models {
		if strings.EqualFold(model, current) {
			idx = i
			break
		}
	}
	idx = (idx + 1) % len(models)
	prof.Model = models[idx]
	m.eng.Config.Providers.Profiles[name] = prof
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(name)
	m.notice = fmt.Sprintf("%s model → %s", name, prof.Model)
	return m
}

func (m Model) toggleFallbackProvider(name string) Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	var newFallback []string
	found := false
	for _, fb := range m.eng.Config.Providers.Fallback {
		if strings.EqualFold(fb, name) {
			found = true
			continue
		}
		newFallback = append(newFallback, fb)
	}
	if !found {
		newFallback = append(newFallback, name)
	}
	m.eng.Config.Providers.Fallback = newFallback
	if m.eng != nil && m.eng.Providers != nil {
		m.eng.Providers.SetFallback(newFallback)
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(name)
	if found {
		m.notice = "removed " + name + " from fallback"
	} else {
		m.notice = "added " + name + " to fallback"
	}
	return m
}

func (m Model) deleteActiveModel() Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		return m
	}
	models := prof.AllModels()
	if len(models) == 0 {
		m.notice = "no models to delete"
		return m
	}
	idx := m.providers.modelEditIdx
	if idx < 0 || idx >= len(models) {
		idx = 0
	}
	deleted := models[idx]
	models = append(models[:idx], models[idx+1:]...)
	prof.Models = models
	if len(models) > 0 {
		prof.Model = models[0]
	} else {
		prof.Model = ""
	}
	m.eng.Config.Providers.Profiles[m.providers.detailProvider] = prof
	if m.providers.modelEditIdx >= len(models) {
		m.providers.modelEditIdx = max(0, len(models)-1)
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(m.providers.detailProvider)
	m.notice = fmt.Sprintf("deleted model %s from %s", deleted, m.providers.detailProvider)
	return m
}

func (m Model) handleProfileEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.providers.profileEditMode = false
		m.providers.profileEditDraft = ""
		m.notice = "profile edit cancelled"
	case "enter":
		m.commitProfileEditField()
		if err := m.persistProfileEdits(); err != nil {
			m.notice = "save failed: " + err.Error()
		} else {
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(m.providers.detailProvider)
			m.notice = "saved profile for " + m.providers.detailProvider
		}
		m.providers.profileEditMode = false
		m.providers.profileEditDraft = ""
	case "tab":
		m.commitProfileEditField()
		m.providers.profileEditField = (m.providers.profileEditField + 1) % 4
		m.providers.profileEditDraft = ""
	case "backspace":
		if len(m.providers.profileEditDraft) > 0 {
			m.providers.profileEditDraft = m.providers.profileEditDraft[:len(m.providers.profileEditDraft)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.providers.profileEditDraft += string(msg.Runes)
		}
	}
	return m, nil
}

func (m *Model) commitProfileEditField() {
	if m.eng == nil || m.eng.Config == nil {
		return
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		return
	}
	draft := strings.TrimSpace(m.providers.profileEditDraft)
	if draft == "" {
		return
	}
	switch m.providers.profileEditField {
	case 0:
		prof.Protocol = draft
	case 1:
		prof.BaseURL = draft
	case 2:
		if v, err := strconv.Atoi(draft); err == nil {
			prof.MaxContext = v
		}
	case 3:
		if v, err := strconv.Atoi(draft); err == nil {
			prof.MaxTokens = v
		}
	}
	m.eng.Config.Providers.Profiles[m.providers.detailProvider] = prof
	m.providers.profileEditDraft = ""
}

func (m *Model) persistProfileEdits() error {
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		return fmt.Errorf("provider not found")
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return err
	}

	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}

	profilesNode := ensureStringAnyMap(ensureStringAnyMap(doc, "providers"), "profiles")
	profileNode := ensureStringAnyMap(profilesNode, m.providers.detailProvider)
	if strings.TrimSpace(prof.Protocol) != "" {
		profileNode["protocol"] = prof.Protocol
	}
	if strings.TrimSpace(prof.BaseURL) != "" {
		profileNode["base_url"] = prof.BaseURL
	}
	if prof.MaxTokens > 0 {
		profileNode["max_tokens"] = prof.MaxTokens
	}
	if prof.MaxContext > 0 {
		profileNode["max_context"] = prof.MaxContext
	}
	if strings.TrimSpace(prof.Model) != "" {
		profileNode["model"] = prof.Model
	}
	if len(prof.Models) > 0 {
		profileNode["models"] = prof.Models
	}

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return m.reloadEngineConfig()
}
