package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"

	tea "github.com/charmbracelet/bubbletea"
)

// --- helpers ---

func newProvidersTestModel() Model {
	cfg := config.DefaultConfig()
	eng := &engine.Engine{
		Config:      cfg,
		ProjectRoot: "",
		EventBus:    engine.NewEventBus(),
	}
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context", "Providers"},
		activeTab:             14,
		diagnosticPanelsState: newDiagnosticPanelsState(),
		eng:                   eng,
	}
}

func sampleProviderRows() []providerRow {
	return []providerRow{
		{Name: "anthropic", Model: "claude-opus-4", MaxContext: 200000, ToolStyle: "provider-native", SupportsTools: true, BestFor: []string{"reasoning", "long-context"}, IsPrimary: true, Status: "ready"},
		{Name: "deepseek", Model: "deepseek-chat", MaxContext: 128000, ToolStyle: "provider-native", SupportsTools: false, BestFor: []string{"code"}, Status: "no-key"},
		{Name: "offline", Model: "deterministic", MaxContext: 12000, ToolStyle: "none", SupportsTools: false, BestFor: []string{"offline-analysis", "fallback"}, IsOffline: true, Status: "offline"},
	}
}

func TestStatusLoadedHydratesPrimaryProviderFromConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "minimax"
	cfg.Providers.Profiles["minimax"] = config.ModelConfig{
		Model:      "MiniMax-M2.7",
		Protocol:   "anthropic",
		BaseURL:    "https://api.minimax.io/anthropic/v1",
		MaxContext: 204800,
		MaxTokens:  131072,
	}
	eng := &engine.Engine{Config: cfg, EventBus: engine.NewEventBus()}
	m := NewModel(context.Background(), nil)
	m.eng = eng

	nextModel, _ := m.Update(statusLoadedMsg{})
	next := nextModel.(Model)
	if next.status.Provider != "minimax" {
		t.Fatalf("expected provider hydrated from config primary, got %q", next.status.Provider)
	}
	if next.status.Model != "MiniMax-M2.7" {
		t.Fatalf("expected model hydrated from primary profile, got %q", next.status.Model)
	}
	if next.status.ProviderProfile.MaxContext != 204800 {
		t.Fatalf("expected provider profile context hydrated, got %d", next.status.ProviderProfile.MaxContext)
	}
}

func TestProviderStatusPanelRowsShowsRuntimeRelevantProvidersOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "primary-ready"
	cfg.Providers.Fallback = []string{"fallback-ready", "openai", "deepseek"}
	cfg.Providers.Profiles = map[string]config.ModelConfig{
		"active-ready": {
			Model:    "active-model",
			Protocol: "openai-compatible",
			BaseURL:  "http://active.example/v1",
		},
		"primary-ready": {
			Model:    "primary-model",
			Protocol: "openai-compatible",
			BaseURL:  "http://primary.example/v1",
		},
		"fallback-ready": {
			Model:    "fallback-model",
			Protocol: "openai-compatible",
			BaseURL:  "http://fallback.example/v1",
		},
		"catalog-owned": {
			CatalogID: "zai",
			Model:     "glm-5.1",
			BaseURL:   "http://catalog.example/v1",
		},
		"my-provider": {
			Model:    "my-model",
			Protocol: "openai-compatible",
			BaseURL:  "http://mine.example/v1",
			Tags:     []string{"my-provider"},
		},
		"seed-no-key": {
			Model:    "unrelated-model",
			Protocol: "anthropic",
		},
		"openai":   config.ModelsDevSeedProfiles()["openai"],
		"deepseek": config.ModelsDevSeedProfiles()["deepseek"],
	}
	eng := &engine.Engine{Config: cfg, EventBus: engine.NewEventBus()}
	eng.SetProviderModel("active-ready", "active-model")
	m := NewModel(context.Background(), eng)
	m.status = eng.Status()

	rows := m.providerStatusPanelRows()
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"active-ready", "fallback-ready"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status rows missing %s: %v", want, names)
		}
	}
	if strings.Contains(got, "primary-ready") || strings.Contains(got, "my-provider") {
		t.Fatalf("status rows should omit primary/non-runtime owned profiles: %v", names)
	}
	if strings.Contains(got, "catalog-owned") {
		t.Fatalf("status rows should omit catalog-only seed profiles: %v", names)
	}
	if strings.Contains(got, "seed-no-key") {
		t.Fatalf("status rows should omit unrelated no-key profiles: %v", names)
	}
	if strings.Contains(got, "openai") || strings.Contains(got, "deepseek") {
		t.Fatalf("status rows should omit no-key default seed fallbacks: %v", names)
	}
}

// --- provider CRUD ---

func TestCreateProvider_EmptyNameError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m.eng = &engine.Engine{Config: config.DefaultConfig()}
	err := m.createProvider("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if err.Error() != "provider name is empty" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateProvider_NilEngineError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	err := m.createProvider("anthropic")
	if err == nil {
		t.Fatal("expected error when engine is nil")
	}
	if err.Error() != "engine not ready" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateProvider_AlreadyExistsError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{Protocol: "anthropic"}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m := NewModel(context.TODO(), eng)
	err := m.createProvider("anthropic")
	if err == nil {
		t.Fatal("expected error for duplicate provider")
	}
}

func TestDeleteProvider_EmptyNameError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	err := m.deleteProvider("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestDeleteProvider_NilEngineError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	err := m.deleteProvider("anthropic")
	if err == nil {
		t.Fatal("expected error when engine is nil")
	}
}

func TestDeleteProvider_Success(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["testprov"] = config.ModelConfig{Protocol: "openai-compatible"}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m := NewModel(context.TODO(), eng)
	err := m.deleteProvider("testprov")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, exists := m.eng.Config.Providers.Profiles["testprov"]; exists {
		t.Error("provider should be deleted")
	}
}

func TestCycleProviderModel_NilEngine(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m = m.cycleProviderModel("anthropic")
	if m.notice != "" {
		t.Errorf("nil engine notice: %s", m.notice)
	}
}

func TestCycleProviderModel_UnknownProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["known"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus"},
	}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m := NewModel(context.TODO(), eng)
	m = m.cycleProviderModel("unknown")
	if m.notice != "" && !strings.Contains(m.notice, "unknown") {
		t.Errorf("unexpected notice: %s", m.notice)
	}
}

func TestCycleProviderModel_SingleModel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["single"] = config.ModelConfig{
		Model:  "only-model",
		Models: []string{"only-model"},
	}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := NewModel(context.TODO(), eng)
	m = m.cycleProviderModel("single")
	// Notice now includes a "saved → <path>" suffix so the user has
	// visible confirmation that their choice was persisted to user
	// home — that was the load-bearing UX gap behind "save ediyorsam
	// save olsun, sürekli kasıyoruz". Just check the action prefix.
	if !strings.HasPrefix(m.notice, "cycle single model → only-model") {
		t.Errorf("notice: %s", m.notice)
	}
}

func TestToggleFallbackProvider_NilEngine(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m = m.toggleFallbackProvider("offline")
	if m.notice != "" {
		t.Errorf("nil engine notice: %s", m.notice)
	}
}

func TestToggleFallbackProvider_AddToFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Fallback = nil
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	// Isolate HOME so the persist side-effect lands in a sandbox
	// instead of polluting the developer's real ~/.dfmc/config.yaml.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := NewModel(context.TODO(), eng)
	m = m.toggleFallbackProvider("offline")
	if m.eng.Config.Providers.Fallback == nil {
		t.Fatal("fallback list should not be nil")
	}
	if len(m.eng.Config.Providers.Fallback) != 1 {
		t.Fatalf("expected 1 fallback, got %d", len(m.eng.Config.Providers.Fallback))
	}
}

func TestToggleFallbackProvider_RemoveFromFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Fallback = []string{"offline", "deepseek"}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := NewModel(context.TODO(), eng)
	m = m.toggleFallbackProvider("offline")
	if len(m.eng.Config.Providers.Fallback) != 1 {
		t.Fatalf("expected 1 fallback after removal, got %d", len(m.eng.Config.Providers.Fallback))
	}
}

func TestSavePipelineDraft_EmptyNameError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m.providers.pipelineDraftName = ""
	err := m.savePipelineDraft()
	if err == nil {
		t.Fatal("expected error for empty pipeline name")
	}
	if err.Error() != "pipeline name is required" {
		t.Errorf("unexpected: %v", err)
	}
}

func TestSavePipelineDraft_NoStepsError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m.providers.pipelineDraftName = "my-pipeline"
	m.providers.pipelineDraftSteps = nil
	err := m.savePipelineDraft()
	if err == nil {
		t.Fatal("expected error for empty steps")
	}
	if err.Error() != "pipeline needs at least one step" {
		t.Errorf("unexpected: %v", err)
	}
}

func TestSavePipelineDraft_MissingProviderError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m.providers.pipelineDraftName = "my-pipeline"
	m.providers.pipelineDraftSteps = []config.PipelineStep{{Provider: "", Model: "sonnet"}}
	err := m.savePipelineDraft()
	if err == nil {
		t.Fatal("expected error for empty step provider")
	}
}

func TestDeletePipeline_EmptyNameError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	err := m.deletePipeline("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if err.Error() != "pipeline name is empty" {
		t.Errorf("unexpected: %v", err)
	}
}

func TestDeletePipeline_NilEngineError(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m.eng = &engine.Engine{Config: config.DefaultConfig()}
	err := m.deletePipeline("some-pipeline")
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestDeleteActiveModel_NilEngine(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m = m.deleteActiveModel()
	if m.notice != "engine not ready" {
		t.Errorf("notice: %s", m.notice)
	}
}

func TestDeleteActiveModel_UnknownProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["known"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet"},
	}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m := NewModel(context.TODO(), eng)
	m.providers.detailProvider = "unknown"
	m = m.deleteActiveModel()
	if m.notice != "provider not found" {
		t.Errorf("notice: %s", m.notice)
	}
}

func TestAddModelToProvider_NilEngine(t *testing.T) {
	m := NewModel(context.TODO(), nil)
	m.addModelToProvider("anthropic", "sonnet")
}

func TestAddModelToProvider_UnknownProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["known"] = config.ModelConfig{Model: "sonnet", Models: []string{"sonnet"}}
	eng := &engine.Engine{Config: cfg}
	m := NewModel(context.TODO(), eng)
	m.addModelToProvider("unknown", "model")
	if len(m.eng.Config.Providers.Profiles["known"].Models) != 1 {
		t.Errorf("unknown provider should not modify models: %v", m.eng.Config.Providers.Profiles["known"].Models)
	}
}

func TestAddModelToProvider_Success(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["test"] = config.ModelConfig{Model: "sonnet", Models: []string{"sonnet"}}
	eng := &engine.Engine{Config: cfg}
	m := NewModel(context.TODO(), eng)
	m.addModelToProvider("test", "opus")
	if len(m.eng.Config.Providers.Profiles["test"].Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(m.eng.Config.Providers.Profiles["test"].Models))
	}
	if m.eng.Config.Providers.Profiles["test"].Model != "sonnet" {
		t.Errorf("active model: got %s", m.eng.Config.Providers.Profiles["test"].Model)
	}
}

func TestCommitProfileEditField_NilEngine(t *testing.T) {
	m := newProvidersTestModel()
	m.eng = nil
	m.commitProfileEditField()
}

func TestCommitProfileEditField_EmptyDraft(t *testing.T) {
	m := newProvidersTestModel()
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{BaseURL: "http://old"}
	m.eng.Config = cfg
	m.providers.detailProvider = "anthropic"
	m.providers.profileEditDraft = ""
	m.providers.profileEditField = 1
	m.commitProfileEditField()
	if m.eng.Config.Providers.Profiles["anthropic"].BaseURL != "http://old" {
		t.Errorf("empty draft should not change value: %s", m.eng.Config.Providers.Profiles["anthropic"].BaseURL)
	}
}

func TestCommitProfileEditField_DoesNotEditModelLimits(t *testing.T) {
	m := newProvidersTestModel()
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{MaxContext: 1000, MaxTokens: 5000}
	m.eng.Config = cfg
	m.providers.detailProvider = "anthropic"
	m.providers.profileEditField = 99
	m.providers.profileEditDraft = "200000"
	m.commitProfileEditField()
	if got := m.eng.Config.Providers.Profiles["anthropic"].MaxContext; got != 1000 {
		t.Errorf("MaxContext should not be editable from provider profile, got %d", got)
	}
}

// --- confirm / menu key handlers ---

func TestHandleProvidersConfirmKey_NoAction(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.confirmAction = ""
	m2, _ := m.handleProvidersConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	nm := m2.(Model)
	if nm.notice != "" {
		t.Errorf("notice should be empty: %s", nm.notice)
	}
}

func TestHandleProvidersConfirmKey_NKey(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.confirmAction = "delete_provider"
	m.providers.confirmTarget = "anthropic"
	m2, _ := m.handleProvidersConfirmKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.notice != "Cancelled." {
		t.Errorf("notice: %s", nm.notice)
	}
	if nm.providers.confirmAction != "" {
		t.Error("confirm should be cleared")
	}
}

// --- executeConfirmedProviderAction ---

func TestExecuteConfirmedAction_DeleteProvider(t *testing.T) {
	m := newProvidersTestModel()
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["delprov"] = config.ModelConfig{Protocol: "openai-compatible"}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m.eng = eng
	m2, _ := m.executeConfirmedProviderAction("delete_provider", "delprov")
	nm := m2.(Model)
	if _, exists := nm.eng.Config.Providers.Profiles["delprov"]; exists {
		t.Error("delprov should be deleted")
	}
}

func TestExecuteConfirmedAction_DeleteModel(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.detailProvider = "anthropic"
	m.providers.modelEditIdx = 0
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus"},
	}
	m.eng.Config = cfg
	m2, _ := m.executeConfirmedProviderAction("delete_model", "sonnet")
	nm := m2.(Model)
	if len(nm.eng.Config.Providers.Profiles["anthropic"].Models) != 1 {
		t.Errorf("expected 1 model after delete, got %d", len(nm.eng.Config.Providers.Profiles["anthropic"].Models))
	}
}

func TestExecuteConfirmedAction_DeletePipeline(t *testing.T) {
	m := newProvidersTestModel()
	cfg := config.DefaultConfig()
	cfg.Pipelines = map[string]config.PipelineConfig{
		"testpipe": {Steps: []config.PipelineStep{{Provider: "anthropic", Model: "sonnet"}}},
	}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m.eng = eng
	m2, _ := m.executeConfirmedProviderAction("delete_pipeline", "testpipe")
	nm := m2.(Model)
	if _, exists := nm.eng.Config.Pipelines["testpipe"]; exists {
		t.Error("testpipe should be deleted from config")
	}
}

// --- key handlers ---

func TestHandleProvidersListKey_EnterWithNoRows(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = nil
	m.providers.scroll = 0
	m2, _ := m.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := m2.(Model)
	if !nm.actionMenu.open || nm.actionMenu.owner != "Providers" {
		t.Error("enter should open the provider actions menu")
	}
}

func TestHandleProvidersListKey_CtrlFActivatesSearch(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.query = "anthropic"
	m.providers.scroll = 5
	m2, _ := m.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	nm := m2.(Model)
	if !nm.providers.searchActive {
		t.Error("ctrl+f should activate search")
	}
}

func TestHandleProvidersListKey_SlashActivatesSearch(t *testing.T) {
	m := newProvidersTestModel()
	m2, _ := m.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	nm := m2.(Model)
	if !nm.providers.searchActive {
		t.Error("/ should activate search")
	}
}

func TestHandleProvidersListKey_ArrowNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m2, _ := m.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyDown})
	nm := m2.(Model)
	if nm.providers.scroll != 1 {
		t.Errorf("down: got scroll %d", nm.providers.scroll)
	}
	m3, _ := nm.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyEnd})
	nm2 := m3.(Model)
	if nm2.providers.scroll != 2 {
		t.Errorf("end: got scroll %d", nm2.providers.scroll)
	}
}

func TestHandleNewProviderKey_EmptyName(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m.providers.newProviderDraft = ""
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := m2.(Model)
	if !nm.providers.textEditActive {
		t.Error("empty new provider enter should open input box")
	}
}

func TestHandleNewProviderKey_TypingRequiresEnter(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("newprov")})
	nm := m2.(Model)
	if nm.providers.newProviderDraft != "" {
		t.Errorf("newProviderDraft: got %s", nm.providers.newProviderDraft)
	}
	if !strings.Contains(nm.notice, "press Enter") {
		t.Errorf("notice: %s", nm.notice)
	}
}

func TestProvidersInputModeAltRunesDoNotTriggerGlobalShortcuts(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewCatalogForm
	m.providers.catalogFormField = 3
	m.providers.catalogFormKey = "sk-"

	nextModel, _ := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("8"), Alt: true})
	next := nextModel.(Model)

	if next.activeTab != m.activeTab {
		t.Fatalf("paste-like alt+rune must not switch tabs: got %d want %d", next.activeTab, m.activeTab)
	}
	if got := next.providers.catalogFormKey; got != "sk-" {
		t.Fatalf("paste-like alt+rune should not edit a closed field, got %q", got)
	}
	if !strings.Contains(next.notice, "press Enter") {
		t.Fatalf("expected notice to enter field editor, got %q", next.notice)
	}
}

func TestProvidersProfileEditPasteDoesNotTriggerAltShortcuts(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewDetail
	m.providers.profileEditMode = true
	m.providers.profileEditField = 2
	m.providers.profileEditDraft = "key-"

	nextModel, _ := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true})
	next := nextModel.(Model)

	if next.activeTab != m.activeTab {
		t.Fatalf("profile edit paste-like alt+rune must stay on Providers: got %d want %d", next.activeTab, m.activeTab)
	}
	if got := next.providers.profileEditDraft; got != "key-" {
		t.Fatalf("profile edit should not append into a closed field, got %q", got)
	}
	if !strings.Contains(next.notice, "press Enter") {
		t.Fatalf("expected notice to enter field editor, got %q", next.notice)
	}
}

func TestProviderInputTextKeyDoesNotConsumeEnterAfterPaste(t *testing.T) {
	m := newProvidersTestModel()
	m = m.beginProviderTextEdit(providerViewCatalogForm, 3, "API key", "", true)

	next, handled := m.handleProviderInputTextKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-part"), Paste: true})
	if !handled {
		t.Fatal("paste text should be handled as input")
	}
	next, handled = next.handleProviderInputTextKey(tea.KeyMsg{Type: tea.KeyEnter})
	if handled {
		t.Fatal("enter after paste must remain a form action, not secret text")
	}
	if got := next.providers.textEditDraft; got != "sk-part" {
		t.Fatalf("paste text should remain intact, got %q", got)
	}
}

func TestProviderSecretInputDropsControlCharsWithNotice(t *testing.T) {
	m := newProvidersTestModel()
	m = m.beginProviderTextEdit(providerViewCatalogForm, 3, "API key", "", true)

	next, handled := m.handleProviderInputTextKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-a\nb\tc"), Paste: true})
	if !handled {
		t.Fatal("paste text should be handled")
	}
	if got := next.providers.textEditDraft; got != "sk-abc" {
		t.Fatalf("secret input should ignore control chars, got %q", got)
	}
	if !strings.Contains(next.notice, "control chars ignored") {
		t.Fatalf("expected notice about ignored control chars, got %q", next.notice)
	}
}

func TestCatalogFormEnterOpensFieldEditor(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewCatalogForm
	m.providers.catalogFormField = 3

	nextModel, _ := m.handleCatalogProviderFormKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)

	if !next.providers.textEditActive {
		t.Fatal("enter on a field should open the text edit box")
	}
	if !next.providers.textEditSecret {
		t.Fatal("API key edit box should be secret-cleaned")
	}
}

func TestProvidersEmptyNewProviderInputBypassesGlobalShortcuts(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m.providers.newProviderDraft = ""

	nextModel, _ := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true})
	next := nextModel.(Model)

	if next.activeTab != m.activeTab {
		t.Fatalf("empty new-provider input must not switch tabs: got %d want %d", next.activeTab, m.activeTab)
	}
	if got := next.providers.newProviderDraft; got != "" {
		t.Fatalf("new provider input should stay closed until Enter, got %q", got)
	}
	if !strings.Contains(next.notice, "press Enter") {
		t.Fatalf("expected notice to enter field editor, got %q", next.notice)
	}
}

func TestConfigProtocolFromCatalogOpenAICompatible(t *testing.T) {
	got := configProtocolFromCatalog(config.ModelsDevProvider{NPM: "@ai-sdk/openai-compatible"})
	if got != "openai-compatible" {
		t.Fatalf("protocol: got %q", got)
	}
}

func TestCatalogCompatibleFieldCyclesWithSpace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewCatalogForm
	m.providers.catalogFormField = 2
	m.providers.catalogFormCompat = "openai-compatible"

	nextModel, _ := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeySpace})
	next := nextModel.(Model)

	if next.providers.catalogFormCompat != "openai" {
		t.Fatalf("space should cycle compatible value, got %q", next.providers.catalogFormCompat)
	}
}

func TestCatalogCompatibleFieldEnterCyclesWithoutTextEditor(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewCatalogForm
	m.providers.catalogFormField = 2
	m.providers.catalogFormCompat = "openai-compatible"

	nextModel, _ := m.handleCatalogProviderFormKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)

	if next.providers.textEditActive {
		t.Fatal("compatible must not open the text edit box")
	}
	if next.providers.catalogFormCompat != "openai" {
		t.Fatalf("enter should cycle compatible value, got %q", next.providers.catalogFormCompat)
	}
}

func TestCatalogFormRendersFieldTitles(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewCatalogForm
	m.providers.catalogFormName = "zai"
	m.providers.catalogFormURL = "https://api.z.ai/api/paas/v4"
	m.providers.catalogFormCompat = "openai-compatible"
	m.providers.catalogFormKey = "sk-test"

	view := m.renderProviderCatalogFormView(100)
	for _, want := range []string{"Provider name:", "Endpoint:", "Compatible:", "API key:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("catalog form missing %q:\n%s", want, view)
		}
	}
}

func TestProviderProfileEditRendersFieldTitles(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewDetail
	m.providers.detailProvider = "anthropic"
	m.providers.profileEditMode = true

	view := m.renderProviderDetailView(100)
	for _, want := range []string{"Provider name:", "Endpoint:", "Compatible:", "API key:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("profile edit missing %q:\n%s", want, view)
		}
	}
}

func TestProviderDetailModelTagsUseTierPrimary(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = providerViewDetail
	m.providers.detailProvider = "zai"
	m.eng.Config.Providers.Primary = "zai"
	m.eng.Config.Providers.Profiles = map[string]config.ModelConfig{
		"zai": {
			Model:  "glm-old",
			Models: []string{"glm-old", "glm-new"},
		},
	}
	m.eng.Config.Routing.Tiers = map[string]config.TierRouting{
		"frontier": {Primary: "zai:glm-new"},
	}

	view := m.renderProviderDetailView(100)
	if !strings.Contains(view, "glm-new") || !strings.Contains(view, "frontier primary") {
		t.Fatalf("tier primary tag missing:\n%s", view)
	}
	if strings.Contains(view, "glm-old primary") {
		t.Fatalf("old profile model should not render as primary:\n%s", view)
	}
}

func TestProviderOpenAIRequestURL(t *testing.T) {
	got := providerOpenAIRequestURL("openai-compatible", "https://api.z.ai/api/coding/paas/v4")
	if got != "https://api.z.ai/api/coding/paas/v4/chat/completions" {
		t.Fatalf("request url: %q", got)
	}
	got = providerOpenAIRequestURL("openai-compatible", "https://api.z.ai/api/coding/paas/v4/chat/completions")
	if got != "https://api.z.ai/api/coding/paas/v4/chat/completions" {
		t.Fatalf("full request url should not double append, got %q", got)
	}
}

func TestCatalogProviderPersistenceStoresReferenceNotModelList(t *testing.T) {
	node := map[string]any{
		"model":           "old",
		"models":          []string{"old"},
		"fallback_models": []string{"older"},
		"max_context":     123,
		"max_tokens":      456,
	}
	writeProviderProfileProjectConfig(node, config.ModelConfig{
		CatalogID: "zai",
		Model:     "glm-5.1",
		Models:    []string{"glm-5.1", "glm-5"},
		BaseURL:   "https://api.z.ai/api/paas/v4",
		Protocol:  "openai-compatible",
	})
	for _, key := range []string{"models", "fallback_models", "max_context", "max_tokens"} {
		if _, ok := node[key]; ok {
			t.Fatalf("catalog provider must not persist %s: %#v", key, node)
		}
	}
	if got := node["catalog_id"]; got != "zai" {
		t.Fatalf("catalog_id not persisted: %#v", node)
	}
	if got := node["model"]; got != "glm-5.1" {
		t.Fatalf("catalog model pin not persisted: %#v", node)
	}
}

func TestApplyProviderModelSelectionHydratesCatalogLimits(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	catalog := config.ModelsDevCatalog{
		"zai": {
			ID:  "zai",
			NPM: "@ai-sdk/openai-compatible",
			Models: map[string]config.ModelsDevModel{
				"glm-old": {ID: "glm-old", Limit: config.ModelsDevLimits{Context: 1000, Output: 100}},
				"glm-new": {ID: "glm-new", Limit: config.ModelsDevLimits{Context: 200000, Output: 131072}},
			},
		},
	}
	if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
		t.Fatalf("save catalog: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "my-zai"
	cfg.Providers.Profiles = map[string]config.ModelConfig{
		"my-zai": {
			BaseURL:    "https://api.z.ai/api/paas/v4",
			CatalogID:  "zai",
			Model:      "glm-old",
			Protocol:   "openai-compatible",
			MaxContext: 1000,
			MaxTokens:  100,
		},
	}
	eng := &engine.Engine{Config: cfg, EventBus: engine.NewEventBus(), ProjectRoot: t.TempDir()}
	m := NewModel(context.Background(), eng)

	next := m.applyProviderModelSelection("my-zai", "glm-new")

	prof := next.eng.Config.Providers.Profiles["my-zai"]
	if prof.Model != "glm-new" {
		t.Fatalf("model not selected: %#v", prof)
	}
	if prof.MaxContext != 200000 || prof.MaxTokens != 131072 {
		t.Fatalf("catalog limits not applied: %#v", prof)
	}
	if next.status.ProviderProfile.MaxContext != 200000 {
		t.Fatalf("runtime status max_context stale: %#v", next.status.ProviderProfile)
	}
}

func TestHandleNewProviderKey_Backspace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m.providers.newProviderDraft = "abc"
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyBackspace})
	nm := m2.(Model)
	if nm.providers.newProviderDraft != "abc" {
		t.Errorf("backspace should not edit closed field: got %s", nm.providers.newProviderDraft)
	}
	if !strings.Contains(nm.notice, "press Enter") {
		t.Errorf("notice: %s", nm.notice)
	}
}

func TestHandleNewProviderKey_Esc(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.viewMode != "list" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
}

func TestHandleProfileEditKey_EscCancels(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m.providers.profileEditDraft = "modified"
	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.profileEditMode {
		t.Error("esc should exit profile edit mode")
	}
	if nm.providers.profileEditDraft != "" {
		t.Error("draft should be cleared on esc")
	}
}

func TestHandleProfileEditKey_TabCyclesFields(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m.providers.profileEditField = 0
	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyTab})
	nm := m2.(Model)
	if nm.providers.profileEditField != 1 {
		t.Errorf("tab should cycle to field 1, got %d", nm.providers.profileEditField)
	}
	m3, _ := nm.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyTab})
	nm2 := m3.(Model)
	if nm2.providers.profileEditField != 2 {
		t.Errorf("tab should cycle to field 2, got %d", nm2.providers.profileEditField)
	}
	m4, _ := nm2.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyTab})
	nm3 := m4.(Model)
	if nm3.providers.profileEditField != 0 {
		t.Errorf("tab should wrap to field 0, got %d", nm3.providers.profileEditField)
	}
}

func TestHandleProfileEditKey_Backspace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m.providers.profileEditDraft = "abc"
	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyBackspace})
	nm := m2.(Model)
	if nm.providers.profileEditDraft != "abc" {
		t.Errorf("backspace should not edit closed profile field, got %s", nm.providers.profileEditDraft)
	}
	if !strings.Contains(nm.notice, "press Enter") {
		t.Errorf("notice: %s", nm.notice)
	}
}

func TestHandleProfileEditKey_TypingRequiresEnter(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("typed")})
	nm := m2.(Model)
	if nm.providers.profileEditDraft != "" {
		t.Errorf("typing should not edit closed profile field, got %s", nm.providers.profileEditDraft)
	}
	if !strings.Contains(nm.notice, "press Enter") {
		t.Errorf("notice: %s", nm.notice)
	}
}

func TestHandleProfileEditKey_EnterOpensFieldEditor(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m.providers.profileEditField = 2

	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := m2.(Model)

	if !nm.providers.textEditActive {
		t.Fatal("enter should open profile field editor")
	}
	if !nm.providers.textEditSecret {
		t.Fatal("api_key field should use secret input cleanup")
	}
}

func TestHandleProvidersSearchKey_Typing(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.searchActive = true
	m2, _ := m.handleProvidersSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("an")})
	nm := m2.(Model)
	if nm.providers.query != "an" {
		t.Errorf("query: got %s", nm.providers.query)
	}
}

func TestHandleProvidersSearchKey_Space(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.searchActive = true
	m.providers.query = "anthropic"
	m2, _ := m.handleProvidersSearchKey(tea.KeyMsg{Type: tea.KeySpace})
	nm := m2.(Model)
	if nm.providers.query != "anthropic " {
		t.Errorf("space: got %s", nm.providers.query)
	}
}

func TestHandleProvidersSearchKey_Backspace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.searchActive = true
	m.providers.query = "an"
	m2, _ := m.handleProvidersSearchKey(tea.KeyMsg{Type: tea.KeyBackspace})
	nm := m2.(Model)
	if nm.providers.query != "a" {
		t.Errorf("backspace: got %s", nm.providers.query)
	}
}

func TestHandleProvidersSearchKey_EnterCommits(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.searchActive = true
	m.providers.query = "anthropic"
	m2, _ := m.handleProvidersSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := m2.(Model)
	if nm.providers.searchActive {
		t.Error("enter should close search")
	}
	if nm.providers.query != "anthropic" {
		t.Errorf("query should be preserved: got %s", nm.providers.query)
	}
}

func TestHandleProvidersSearchKey_EscCancels(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.searchActive = true
	m.providers.query = "anthropic"
	m2, _ := m.handleProvidersSearchKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.searchActive {
		t.Error("esc should close search")
	}
	if nm.providers.query != "" {
		t.Error("esc should clear query")
	}
}

func TestHandleProvidersDetailKey_Esc(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.viewMode != "list" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
	if nm.providers.detailProvider != "" {
		t.Errorf("detailProvider: got %s", nm.providers.detailProvider)
	}
}

func TestHandleProvidersDetailKey_DownNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyDown})
	nm := m2.(Model)
	if nm.providers.modelEditIdx != 1 {
		t.Errorf("down should advance model idx: got %d", nm.providers.modelEditIdx)
	}
}

func TestHandleProvidersDetailKey_UpNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	m.providers.modelEditIdx = 2
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyUp})
	nm := m2.(Model)
	if nm.providers.modelEditIdx != 1 {
		t.Errorf("up should move back: got %d", nm.providers.modelEditIdx)
	}
}

func TestHandleProvidersDetailKey_EndJump(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyEnd})
	nm := m2.(Model)
	if nm.providers.modelEditIdx != 2 {
		t.Errorf("end should jump to last: got %d", nm.providers.modelEditIdx)
	}
}

func TestHandleProvidersDetailKey_ModelSearchFiltersList(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg

	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	nm := m2.(Model)
	if !nm.providers.modelSearchActive {
		t.Fatal("slash should activate model search")
	}
	m3, _ := nm.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("op")})
	nm2 := m3.(Model)
	got := nm2.detailProviderVisibleModels()
	if len(got) != 1 || got[0] != "opus" {
		t.Fatalf("filtered models: got %v", got)
	}
	selected, ok := nm2.selectedDetailModel()
	if !ok || selected != "opus" {
		t.Fatalf("selected filtered model: got %q ok=%v", selected, ok)
	}
}

func TestRenderProviderDetailShowsModelSearchQuery(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	m.providers.modelSearchActive = true
	m.providers.modelQuery = "op"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg

	view := m.renderProviderDetailView(100)
	if !strings.Contains(view, "Models (1/3)") || !strings.Contains(view, "search: ") || !strings.Contains(view, "opus") {
		t.Fatalf("detail view should show filtered model search:\n%s", view)
	}
	if strings.Contains(view, "haiku") {
		t.Fatalf("detail view should hide non-matching model:\n%s", view)
	}
}

func TestRenderProviderDetailSizedWindowsAroundSelectedModel(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	m.providers.modelEditIdx = 18
	models := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		models = append(models, "model-"+string(rune('a'+i)))
	}
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "model-a",
		Models: models,
	}
	m.eng.Config = cfg

	view := m.renderProviderDetailViewSized(100, 16)
	if !strings.Contains(view, "model-s") {
		t.Fatalf("selected model should stay visible in short detail view:\n%s", view)
	}
	if strings.Contains(view, "model-b") {
		t.Fatalf("short detail view should window model rows instead of starting at top:\n%s", view)
	}
}

func TestRenderProviderCatalogSizedWindowsAroundCursor(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.catalogLoaded = true
	m.providers.catalogScroll = 18
	for i := 0; i < 24; i++ {
		suffix := string(rune('a' + i))
		m.providers.catalogItems = append(m.providers.catalogItems, catalogProviderItem{
			ID:         "provider-" + suffix,
			Name:       "Provider " + suffix,
			Endpoint:   "https://example.test/" + suffix,
			Compatible: "openai-compatible",
			ModelCount: 2,
		})
	}

	view := m.renderProviderCatalogViewSized(100, 9)
	if !strings.Contains(view, "Provider s") {
		t.Fatalf("selected catalog provider should stay visible in short catalog view:\n%s", view)
	}
	if strings.Contains(view, "Provider a") {
		t.Fatalf("short catalog view should window provider rows instead of starting at top:\n%s", view)
	}
}

func TestRenderProviderModelPickerSizedWindowsAroundCursor(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerIndex = 18
	for i := 0; i < 24; i++ {
		m.providers.modelPickerItems = append(m.providers.modelPickerItems, "pick-"+string(rune('a'+i)))
	}

	view := strings.Join(m.renderProviderModelPickerSectionSized(3), "\n")
	if !strings.Contains(view, "pick-s") {
		t.Fatalf("selected picker item should stay visible in short picker:\n%s", view)
	}
	if strings.Contains(view, "pick-a") {
		t.Fatalf("short picker should window rows instead of starting at top:\n%s", view)
	}
}

func TestRenderPipelinesSizedWindowsAroundCursor(t *testing.T) {
	m := newProvidersTestModel()
	for i := 0; i < 24; i++ {
		m.providers.pipelineNames = append(m.providers.pipelineNames, "pipe-"+string(rune('a'+i)))
	}
	m.providers.pipelineScroll = 18

	view := m.renderPipelinesViewSized(100, 8)
	if !strings.Contains(view, "pipe-s") {
		t.Fatalf("selected pipeline should stay visible in short pipeline view:\n%s", view)
	}
	if strings.Contains(view, "pipe-a") {
		t.Fatalf("short pipeline view should window rows instead of starting at top:\n%s", view)
	}
}

func TestHandleProvidersPipelineKey_Esc(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "pipelines"
	m2, _ := m.handleProvidersPipelineKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.viewMode != "list" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
}

func TestHandleProvidersPipelineKey_DownNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "pipelines"
	m.providers.pipelineNames = []string{"pipe1", "pipe2", "pipe3"}
	m2, _ := m.handleProvidersPipelineKey(tea.KeyMsg{Type: tea.KeyDown})
	nm := m2.(Model)
	if nm.providers.pipelineScroll != 1 {
		t.Errorf("down: got scroll %d", nm.providers.pipelineScroll)
	}
}

func TestHandleProvidersPipelineKey_EndJump(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "pipelines"
	m.providers.pipelineNames = []string{"pipe1", "pipe2", "pipe3"}
	m2, _ := m.handleProvidersPipelineKey(tea.KeyMsg{Type: tea.KeyEnd})
	nm := m2.(Model)
	if nm.providers.pipelineScroll != 2 {
		t.Errorf("end: got scroll %d", nm.providers.pipelineScroll)
	}
}

func TestHandleModelPickerKey_Esc(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerManual = false
	m.providers.modelPickerDraft = "typed"
	m2, _ := m.handleModelPickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.modelPickerActive {
		t.Error("esc should close picker")
	}
}

func TestHandleModelPickerKey_SpaceSwitchesToManual(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerManual = false
	m2, _ := m.handleModelPickerKey(tea.KeyMsg{Type: tea.KeySpace})
	nm := m2.(Model)
	if !nm.providers.modelPickerManual {
		t.Error("space should switch to manual mode")
	}
}

func TestHandleModelPickerKey_ManualTyping(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerManual = true
	m2, _ := m.handleModelPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("custom-model")})
	nm := m2.(Model)
	if nm.providers.modelPickerDraft != "custom-model" {
		t.Errorf("manual draft: got %s", nm.providers.modelPickerDraft)
	}
}

func TestHandleModelPickerKey_ManualBackspace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerManual = true
	m.providers.modelPickerDraft = "abc"
	m2, _ := m.handleModelPickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	nm := m2.(Model)
	if nm.providers.modelPickerDraft != "ab" {
		t.Errorf("backspace: got %s", nm.providers.modelPickerDraft)
	}
}

func TestHandleModelPickerKey_ManualEnterAddsModel(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerManual = true
	m.providers.modelPickerDraft = "custom-model"
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet"},
	}
	m.eng.Config = cfg
	m2, _ := m.handleModelPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := m2.(Model)
	if nm.providers.modelPickerActive {
		t.Error("enter should close manual picker")
	}
	prof := nm.eng.Config.Providers.Profiles["anthropic"]
	if prof.Model != "sonnet" {
		t.Errorf("primary model should stay explicit: %s", prof.Model)
	}
	found := false
	for _, model := range prof.Models {
		if model == "custom-model" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("custom model not added to model list: %v", prof.Models)
	}
}

// --- helper / accessor tests ---

func TestDetailProviderModels_NilEngine(t *testing.T) {
	m := newProvidersTestModel()
	m.eng = nil
	got := m.detailProviderModels()
	if got != nil {
		t.Errorf("nil engine: got %v", got)
	}
}

func TestLoadModelsDevForProviderUsesCatalogIDNotAliasName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), config.ModelsDevCatalog{
		"zai-coding-plan": {
			ID: "zai-coding-plan",
			Models: map[string]config.ModelsDevModel{
				"glm-5.1": {ID: "glm-5.1"},
				"glm-5v":  {ID: "glm-5v"},
			},
		},
	}); err != nil {
		t.Fatalf("SaveModelsDevCatalog: %v", err)
	}
	m := newProvidersTestModel()
	m.eng.Config.Providers.Profiles["my-zai-work"] = config.ModelConfig{
		CatalogID: "zai-coding-plan",
	}

	got := m.loadModelsDevForProvider("my-zai-work")
	if strings.Join(got, ",") != "glm-5.1,glm-5v" {
		t.Fatalf("models should come from catalog_id ref, got %v", got)
	}
}

func TestStartCatalogProviderFormSuggestsUniqueAliasForSameReference(t *testing.T) {
	m := newProvidersTestModel()
	m.eng.Config.Providers.Profiles["zai-coding-plan"] = config.ModelConfig{CatalogID: "zai-coding-plan"}
	m.eng.Config.Providers.Profiles["zai-coding-plan-2"] = config.ModelConfig{CatalogID: "zai-coding-plan"}

	m = m.startCatalogProviderForm(catalogProviderItem{ID: "zai-coding-plan", Name: "ZAI Coding Plan"})

	if got := m.providers.catalogFormName; got != "zai-coding-plan-3" {
		t.Fatalf("form should suggest unique alias, got %q", got)
	}
	if got := m.providers.catalogRefID; got != "zai-coding-plan" {
		t.Fatalf("catalog ref must stay original models.dev id, got %q", got)
	}
}

func TestFocusProviderRow_Empty(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	m = m.focusProviderRow("")
	if m.providers.scroll != 0 {
		t.Errorf("empty name: scroll should stay 0, got %d", m.providers.scroll)
	}
}

func TestFocusProviderRow_CaseInsensitive(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	m = m.focusProviderRow("DEEPSEEK")
	if m.providers.scroll != 1 {
		t.Errorf("case insensitive match: scroll=%d want 1", m.providers.scroll)
	}
}

func TestFocusProviderRow_NotFound(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	before := m.providers.scroll
	m = m.focusProviderRow("nonexistent")
	if m.providers.scroll != before {
		t.Errorf("not found: scroll should stay %d, got %d", before, m.providers.scroll)
	}
}

func TestRefreshProvidersRows_NilEngineSetsErr(t *testing.T) {
	m := newProvidersTestModel()
	m.eng = nil
	m2 := m.refreshProvidersRows()
	nm := m2
	if nm.providers.err == "" {
		t.Fatal("nil engine should set err")
	}
}

func TestCollectProviderRows_NilEngine(t *testing.T) {
	got := collectProviderRows(nil)
	if got != nil {
		t.Errorf("nil engine: got %v", got)
	}
}

func TestCollectProviderRows_NilProviders(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := &engine.Engine{Config: cfg, Providers: nil}
	got := collectProviderRows(eng)
	if got != nil {
		t.Errorf("nil providers: got %v", got)
	}
}

func TestFilteredProviderRows_SearchByModel(t *testing.T) {
	rows := sampleProviderRows()
	got := filteredProviderRows(rows, "deepseek-chat")
	if len(got) != 1 || got[0].Name != "deepseek" {
		t.Errorf("search by model: got %v", got)
	}
}

func TestFilteredProviderRows_NoMatch(t *testing.T) {
	rows := sampleProviderRows()
	got := filteredProviderRows(rows, "xyznotamatch")
	if len(got) != 0 {
		t.Errorf("no match: got %d rows", len(got))
	}
}

func TestFilteredProviderRows_EmptyRows(t *testing.T) {
	got := filteredProviderRows(nil, "anthropic")
	if len(got) != 0 {
		t.Errorf("nil rows: got %d", len(got))
	}
}

func TestProviderStatusTag_OfflineByName(t *testing.T) {
	status, offline := providerStatusTag("Offline", true)
	if status != "offline" || !offline {
		t.Errorf("Offline name wins: status=%s offline=%v", status, offline)
	}
}

func TestProviderStatusTag_NoKeyNonOffline(t *testing.T) {
	status, offline := providerStatusTag("openai", false)
	if status != "no-key" || offline {
		t.Errorf("openai no-key: status=%s offline=%v", status, offline)
	}
}

func TestStatusPriority_Default(t *testing.T) {
	if statusPriority("something") != 3 {
		t.Errorf("default priority: got %d", statusPriority("something"))
	}
}

// --- action menu tests ---
