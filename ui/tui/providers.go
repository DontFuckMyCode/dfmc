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
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// providerRow is one row in the panel — a shaped snapshot of what a
// registered Provider's Hints() + MaxContext() + Model() report, plus
// the derived "ready / no-key / offline" status string that distills
// the three classes (real, placeholder, offline) into one tag.
type providerRow struct {
	Name          string
	Model         string
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

// collectProviderRows walks the registered providers and shapes them
// into rows sorted by name. Pure — no goroutines, no engine state
// mutation; safe to call from handle* and render* paths.
func collectProviderRows(eng *engine.Engine) []providerRow {
	if eng == nil || eng.Providers == nil {
		return nil
	}
	names := eng.Providers.List()
	sort.Strings(names)
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
			MaxContext:    p.MaxContext(),
			ToolStyle:     hints.ToolStyle,
			SupportsTools: hints.SupportsTools,
			BestFor:       append([]string(nil), hints.BestFor...),
			IsOffline:     isOffline,
			IsPrimary:     strings.EqualFold(name, primary),
			Status:        status,
		})
	}
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

// formatProviderDetail renders the selected row's extended info
// (best_for tags, primary badge) beneath the list.
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
	width = clampInt(width, 24, 1000)
	hint := subtleStyle.Render("j/k scroll · r refresh · g/G top/bottom")
	header := sectionHeader("⚑", "Providers")

	rows := m.providersRows
	order := resolveProviderOrder(m.eng)

	lines := []string{header, hint}

	if len(order) > 0 {
		chain := subtleStyle.Render("resolve order: ") + strings.Join(order, subtleStyle.Render(" → "))
		lines = append(lines, chain)
	}
	lines = append(lines, renderDivider(width-2))

	if m.providersErr != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.providersErr))
		return strings.Join(lines, "\n")
	}

	if len(rows) == 0 {
		lines = append(lines,
			"",
			subtleStyle.Render("No providers registered — engine is in degraded startup."),
			subtleStyle.Render("Press r to re-read the router once the store is available."),
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

	scroll := clampScroll(m.providersScroll, len(rows))
	for i, row := range rows {
		selected := i == scroll
		lines = append(lines, formatProviderRow(row, selected, width-2))
	}

	if scroll >= 0 && scroll < len(rows) {
		lines = append(lines, "")
		lines = append(lines, formatProviderDetail(rows[scroll], width-2)...)
	}

	return strings.Join(lines, "\n")
}

// refreshProvidersRows re-reads the router and stamps the fresh rows
// into the Model. Pure — invoked from 'r' and from the tab-switch
// first-activation path.
func (m Model) refreshProvidersRows() Model {
	rows := collectProviderRows(m.eng)
	m.providersRows = rows
	if m.eng == nil {
		m.providersErr = "engine not ready (degraded startup)"
	} else if len(rows) == 0 {
		m.providersErr = "router has no providers"
	} else {
		m.providersErr = ""
	}
	m.providersScroll = clampScroll(m.providersScroll, len(rows))
	return m
}

func (m Model) handleProvidersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.providersRows)
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.providersScroll+step < total {
			m.providersScroll += step
		}
	case "k", "up":
		if m.providersScroll >= step {
			m.providersScroll -= step
		} else {
			m.providersScroll = 0
		}
	case "pgdown":
		if m.providersScroll+pageStep < total {
			m.providersScroll += pageStep
		} else if total > 0 {
			m.providersScroll = total - 1
		}
	case "pgup":
		if m.providersScroll >= pageStep {
			m.providersScroll -= pageStep
		} else {
			m.providersScroll = 0
		}
	case "g":
		m.providersScroll = 0
	case "G":
		if total > 0 {
			m.providersScroll = total - 1
		}
	case "r":
		m = m.refreshProvidersRows()
	}
	return m, nil
}
