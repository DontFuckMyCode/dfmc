package tui

// provider_panel.go — read-side surface for the Providers panel:
// derives provider rows from the engine, filters on the panel query,
// renders status badges + key-source badges + relative-time strings,
// and exposes a few small Model accessors used by the panel renderer.
// All write paths (cycle/setPrimary/toggleFallback/profile edits +
// pipelines) live in provider_panel_actions.go; provider create/delete
// + models.dev sync command live in provider_panel_crud.go; the
// keyboard router lives in provider_panel_key*.go.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type providerRow struct {
	Name          string
	Model         string
	Models        []string // ordered model list for cycling
	Protocol      string
	MaxContext    int
	ToolStyle     string
	SupportsTools bool
	BestFor       []string
	IsOffline     bool
	IsPrimary     bool
	Status        string // "ready" | "offline" | "no-key"
}

// syncModelsDevMsg carries the result of the async models.dev refresh
// dispatched from provider_panel_crud.go's syncModelsDevCmd.
type syncModelsDevMsg struct {
	path    string
	changes []string
	err     error
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
		prof := eng.Config.Providers.Profiles[name]
		rows = append(rows, providerRow{
			Name:          name,
			Model:         p.Model(),
			Models:        p.Models(),
			Protocol:      prof.Protocol,
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

// filteredProviderRows returns only rows whose name, model, or status
// matches the query (case-insensitive). An empty query returns all rows.
func filteredProviderRows(rows []providerRow, query string) []providerRow {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return rows
	}
	var out []providerRow
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Name), q) ||
			strings.Contains(strings.ToLower(r.Model), q) ||
			strings.Contains(strings.ToLower(r.Status), q) {
			out = append(out, r)
		}
	}
	return out
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

// formatRelativeTime returns a human-friendly elapsed string like "2m ago"
// or "just now" for recent timestamps.
func formatRelativeTime(t time.Time) string {
	d := time.Since(t)
	if d < 10*time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// apiKeySourceBadge returns a styled badge showing where the provider's API
// key comes from: env var, config file, or missing. When the key is present
// and the canonical env var matches it, the badge names the env var.
func apiKeySourceBadge(name string, prof config.ModelConfig) string {
	envVar := config.EnvVarForProvider(name)
	key := strings.TrimSpace(prof.APIKey)
	if key == "" {
		if envVar != "" {
			return warnStyle.Render("[key:missing — set " + envVar + "]")
		}
		return warnStyle.Render("[key:missing]")
	}
	if envVar != "" {
		if v, _ := os.LookupEnv(envVar); strings.TrimSpace(v) == key {
			return okStyle.Render("[key:env " + envVar + "]")
		}
	}
	return subtleStyle.Render("[key:config]")
}

func (m Model) refreshProvidersRows() Model {
	rows := collectProviderRows(m.eng)
	m.providers.rows = rows
	if m.eng == nil {
		m.providers.err = "engine not ready — another dfmc process may hold the store lock (try `dfmc doctor`)"
	} else if len(rows) == 0 {
		m.providers.err = "router has no providers"
	} else {
		m.providers.err = ""
	}
	m.providers.scroll = clampScroll(m.providers.scroll, len(filteredProviderRows(rows, m.providers.query)))
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

func (m Model) detailProviderModels() []string {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		return nil
	}
	return prof.AllModels()
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

func (m Model) pipelineNamesFromEngine() []string {
	if m.eng == nil {
		return nil
	}
	return m.eng.PipelineNames()
}
