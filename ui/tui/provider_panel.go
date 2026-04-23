package tui

// provider_panel.go

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

	"gopkg.in/yaml.v3"

	tea "github.com/charmbracelet/bubbletea"
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

// formatProviderRow renders one line. Shape:
// `▶ READY  anthropic  claude-opus-4   max=200000  tools=on  tool-style`.
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
