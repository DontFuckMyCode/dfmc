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
	m := NewModel(context.TODO(), eng)
	m = m.cycleProviderModel("single")
	if m.notice != "cycle single model → only-model" {
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
	if m.eng.Config.Providers.Profiles["test"].Model != "opus" {
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

func TestCommitProfileEditField_IntValues(t *testing.T) {
	m := newProvidersTestModel()
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{MaxContext: 1000, MaxTokens: 5000}
	m.eng.Config = cfg
	m.providers.detailProvider = "anthropic"
	m.providers.profileEditField = 2
	m.providers.profileEditDraft = "200000"
	m.commitProfileEditField()
	if m.eng.Config.Providers.Profiles["anthropic"].MaxContext != 200000 {
		t.Errorf("MaxContext: got %d", m.eng.Config.Providers.Profiles["anthropic"].MaxContext)
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
	m2, _ := m.handleProvidersConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	nm := m2.(Model)
	if nm.notice != "cancelled" {
		t.Errorf("notice: %s", nm.notice)
	}
	if nm.providers.confirmAction != "" {
		t.Error("confirm should be cleared")
	}
}

func TestHandleProvidersMenuKey_NilMenu(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.menuLabels = nil
	m2, _ := m.handleProvidersMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	nm := m2.(Model)
	if nm.providers.menuActive {
		t.Error("menu should be deactivated when labels are nil")
	}
}

func TestHandleProvidersMenuKey_EscClosesMenu(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.menuActive = true
	m2, _ := m.handleProvidersMenuKey(tea.KeyMsg{Type: tea.KeyEsc})
	nm := m2.(Model)
	if nm.providers.menuActive {
		t.Error("Esc should close menu")
	}
}

func TestHandleProvidersMenuKey_NumberKeySelects(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.menuActive = true
	m.providers.menuLabels = []string{"Item 1", "Item 2", "Item 3"}
	m.providers.menuDisabled = []bool{false, true, false}
	m.providers.menuDisabledReasons = []string{"", "reason", ""}
	m.providers.menuIndex = 0
	m2, _ := m.handleProvidersMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	nm := m2.(Model)
	if nm.providers.menuIndex != 1 {
		t.Errorf("key 2 should select index 1: got %d", nm.providers.menuIndex)
	}
}

func TestHandleProvidersMenuKey_NumberKeyDisabledItem(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.menuActive = true
	m.providers.menuLabels = []string{"Item 1", "Item 2", "Item 3"}
	m.providers.menuDisabled = []bool{false, true, false}
	m.providers.menuDisabledReasons = []string{"", "reason", ""}
	m.providers.menuIndex = 0
	m2, _ := m.handleProvidersMenuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	nm := m2.(Model)
	if nm.notice != "that action is not available" {
		t.Errorf("notice: %s", nm.notice)
	}
}

// --- nextEnabledMenuIndex ---

func TestNextEnabledMenuIndex_AllDisabledForward(t *testing.T) {
	disabled := []bool{true, true, true}
	got := nextEnabledMenuIndex(disabled, 0, 3, 1)
	if got != 0 {
		t.Errorf("all disabled forward: got %d want 0", got)
	}
}

func TestNextEnabledMenuIndex_AllDisabledBackward(t *testing.T) {
	disabled := []bool{true, true, true}
	got := nextEnabledMenuIndex(disabled, 2, 3, -1)
	if got != 2 {
		t.Errorf("all disabled backward: got %d want 2", got)
	}
}

func TestNextEnabledMenuIndex_SkipsDisabled(t *testing.T) {
	disabled := []bool{false, true, false, true}
	got := nextEnabledMenuIndex(disabled, 0, 4, 1)
	if got != 2 {
		t.Errorf("skip disabled: got %d want 2", got)
	}
}

func TestNextEnabledMenuIndex_EmptyTotal(t *testing.T) {
	got := nextEnabledMenuIndex(nil, 5, 0, 1)
	if got != 0 {
		t.Errorf("empty total: got %d want 0", got)
	}
}

func TestNextEnabledMenuIndex_WrapsAround(t *testing.T) {
	disabled := []bool{false, true, false}
	got := nextEnabledMenuIndex(disabled, 0, 3, 1)
	if got != 0 {
		t.Errorf("at end with wrap-around: got %d", got)
	}
}

// --- executeMenuAction ---

func TestExecuteMenuAction_Detail(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	m2, _ := m.executeMenuAction("detail")
	nm := m2.(Model)
	if nm.providers.viewMode != "detail" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
	if nm.providers.detailProvider != "anthropic" {
		t.Errorf("detailProvider: got %s", nm.providers.detailProvider)
	}
}

func TestExecuteMenuAction_Back(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	m2, _ := m.executeMenuAction("back")
	nm := m2.(Model)
	if nm.providers.viewMode != "list" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
	if nm.providers.detailProvider != "" {
		t.Errorf("detailProvider: got %s", nm.providers.detailProvider)
	}
}

func TestExecuteMenuAction_Pipelines(t *testing.T) {
	m := newProvidersTestModel()
	m2, _ := m.executeMenuAction("pipelines")
	nm := m2.(Model)
	if nm.providers.viewMode != "pipelines" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
}

func TestExecuteMenuAction_NewProvider(t *testing.T) {
	m := newProvidersTestModel()
	m2, _ := m.executeMenuAction("new_provider")
	nm := m2.(Model)
	if nm.providers.viewMode != "new_provider" {
		t.Errorf("viewMode: got %s", nm.providers.viewMode)
	}
}

func TestExecuteMenuAction_Refresh(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.err = "stale error"
	m2, _ := m.executeMenuAction("refresh")
	nm := m2.(Model)
	if nm.providers.err == "stale error" {
		t.Errorf("refresh should re-derive error: %s", nm.providers.err)
	}
}

func TestExecuteMenuAction_SetPrimary(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 1 // deepseek
	m2, _ := m.executeMenuAction("set_primary")
	nm := m2.(Model)
	if nm.eng.Config.Providers.Primary != "deepseek" {
		t.Errorf("primary: got %s", nm.eng.Config.Providers.Primary)
	}
}

func TestExecuteMenuAction_DeleteProviderWithSelection(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	m2, _ := m.executeMenuAction("delete_provider")
	nm := m2.(Model)
	if nm.providers.confirmAction != "delete_provider" {
		t.Errorf("confirmAction: got %s", nm.providers.confirmAction)
	}
	if nm.providers.confirmTarget != "anthropic" {
		t.Errorf("confirmTarget: got %s", nm.providers.confirmTarget)
	}
}

func TestExecuteMenuAction_DeleteProviderNoSelection(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = nil
	m.providers.scroll = 0
	m2, _ := m.executeMenuAction("delete_provider")
	nm := m2.(Model)
	if nm.providers.confirmAction != "" {
		t.Errorf("confirmAction should be empty: %s", nm.providers.confirmAction)
	}
}

func TestExecuteMenuAction_CycleModel(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	m2, _ := m.executeMenuAction("cycle_model")
	nm := m2.(Model)
	if nm.notice == "" || !strings.Contains(nm.notice, "cycle") {
		t.Errorf("notice: %s", nm.notice)
	}
}

func TestExecuteMenuAction_ToggleFallback(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 2 // offline
	m2, _ := m.executeMenuAction("toggle_fallback")
	nm := m2.(Model)
	if len(nm.eng.Config.Providers.Fallback) != 1 || nm.eng.Config.Providers.Fallback[0] != "offline" {
		t.Errorf("fallback: got %v", nm.eng.Config.Providers.Fallback)
	}
}

func TestExecuteMenuAction_SyncModels(t *testing.T) {
	m := newProvidersTestModel()
	_, cmd := m.executeMenuAction("sync_models")
	if cmd == nil {
		t.Error("sync_models should return a cmd")
	}
}

// --- executeConfirmedAction ---

func TestExecuteConfirmedAction_DeleteProvider(t *testing.T) {
	m := newProvidersTestModel()
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["delprov"] = config.ModelConfig{Protocol: "openai-compatible"}
	eng := &engine.Engine{Config: cfg, ProjectRoot: t.TempDir()}
	m.eng = eng
	m2, _ := m.executeConfirmedAction("delete_provider", "delprov")
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
	m2, _ := m.executeConfirmedAction("delete_model", "sonnet")
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
	m2, _ := m.executeConfirmedAction("delete_pipeline", "testpipe")
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
	if nm.providers.menuActive {
		t.Error("enter with no rows should not open menu")
	}
}

func TestHandleProvidersListKey_SearchClear(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.query = "anthropic"
	m.providers.scroll = 5
	m2, _ := m.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	nm := m2.(Model)
	if nm.providers.query != "" {
		t.Errorf("c should clear query: got %s", nm.providers.query)
	}
	if nm.providers.scroll != 0 {
		t.Errorf("c should reset scroll: got %d", nm.providers.scroll)
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

func TestHandleProvidersListKey_JNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m2, _ := m.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	nm := m2.(Model)
	if nm.providers.scroll != 1 {
		t.Errorf("j: got scroll %d", nm.providers.scroll)
	}
	m3, _ := nm.handleProvidersListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	nm2 := m3.(Model)
	if nm2.providers.scroll != 2 {
		t.Errorf("G: got scroll %d", nm2.providers.scroll)
	}
}

func TestHandleNewProviderKey_EmptyName(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m.providers.newProviderDraft = ""
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := m2.(Model)
	if nm.notice != "provider name is required" {
		t.Errorf("notice: %s", nm.notice)
	}
}

func TestHandleNewProviderKey_Typing(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("newprov")})
	nm := m2.(Model)
	if nm.providers.newProviderDraft != "newprov" {
		t.Errorf("newProviderDraft: got %s", nm.providers.newProviderDraft)
	}
}

func TestHandleNewProviderKey_Backspace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "new_provider"
	m.providers.newProviderDraft = "abc"
	m2, _ := m.handleNewProviderKey(tea.KeyMsg{Type: tea.KeyBackspace})
	nm := m2.(Model)
	if nm.providers.newProviderDraft != "ab" {
		t.Errorf("backspace: got %s", nm.providers.newProviderDraft)
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
	if nm3.providers.profileEditField != 3 {
		t.Errorf("tab should cycle to field 3, got %d", nm3.providers.profileEditField)
	}
	m5, _ := nm3.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyTab})
	nm4 := m5.(Model)
	if nm4.providers.profileEditField != 0 {
		t.Errorf("tab should wrap to field 0, got %d", nm4.providers.profileEditField)
	}
}

func TestHandleProfileEditKey_Backspace(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m.providers.profileEditDraft = "abc"
	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyBackspace})
	nm := m2.(Model)
	if nm.providers.profileEditDraft != "ab" {
		t.Errorf("backspace: got %s", nm.providers.profileEditDraft)
	}
}

func TestHandleProfileEditKey_Typing(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.profileEditMode = true
	m2, _ := m.handleProfileEditKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("typed")})
	nm := m2.(Model)
	if nm.providers.profileEditDraft != "typed" {
		t.Errorf("typing: got %s", nm.providers.profileEditDraft)
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

func TestHandleProvidersDetailKey_JNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	nm := m2.(Model)
	if nm.providers.modelEditIdx != 1 {
		t.Errorf("j should advance model idx: got %d", nm.providers.modelEditIdx)
	}
}

func TestHandleProvidersDetailKey_KNavigation(t *testing.T) {
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
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	nm := m2.(Model)
	if nm.providers.modelEditIdx != 1 {
		t.Errorf("k should move back: got %d", nm.providers.modelEditIdx)
	}
}

func TestHandleProvidersDetailKey_GJump(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "detail"
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{
		Model:  "sonnet",
		Models: []string{"sonnet", "opus", "haiku"},
	}
	m.eng.Config = cfg
	m2, _ := m.handleProvidersDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	nm := m2.(Model)
	if nm.providers.modelEditIdx != 2 {
		t.Errorf("G should jump to last: got %d", nm.providers.modelEditIdx)
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

func TestHandleProvidersPipelineKey_JNavigation(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "pipelines"
	m.providers.pipelineNames = []string{"pipe1", "pipe2", "pipe3"}
	m2, _ := m.handleProvidersPipelineKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	nm := m2.(Model)
	if nm.providers.pipelineScroll != 1 {
		t.Errorf("j: got scroll %d", nm.providers.pipelineScroll)
	}
}

func TestHandleProvidersPipelineKey_GJumpToEnd(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.viewMode = "pipelines"
	m.providers.pipelineNames = []string{"pipe1", "pipe2", "pipe3"}
	m2, _ := m.handleProvidersPipelineKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	nm := m2.(Model)
	if nm.providers.pipelineScroll != 2 {
		t.Errorf("G: got scroll %d", nm.providers.pipelineScroll)
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

func TestHandleModelPickerKey_MSwitch(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.modelPickerActive = true
	m.providers.modelPickerManual = false
	m2, _ := m.handleModelPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	nm := m2.(Model)
	if !nm.providers.modelPickerManual {
		t.Error("m should switch to manual mode")
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
	if nm.eng.Config.Providers.Profiles["anthropic"].Model != "custom-model" {
		t.Errorf("model not set: %s", nm.eng.Config.Providers.Profiles["anthropic"].Model)
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

// --- build*Menu tests ---

func TestBuildListMenu_SetsMenuState(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 0
	labels, actions, disabled, _ := m.buildListMenu()
	if len(labels) == 0 {
		t.Fatal("expected menu labels")
	}
	if len(labels) != len(actions) || len(actions) != len(disabled) {
		t.Fatal("menu slices length mismatch")
	}
	found := false
	for i, l := range labels {
		if strings.Contains(l, "Already Primary") && disabled[i] {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected disabled 'Already Primary' for primary provider")
	}
}

func TestBuildDetailMenu_SetsMenuState(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.detailProvider = "anthropic"
	cfg := config.DefaultConfig()
	cfg.Providers.Profiles["anthropic"] = config.ModelConfig{Model: "sonnet", Models: []string{"sonnet"}}
	m.eng.Config = cfg
	labels, actions, disabled, _ := m.buildDetailMenu()
	if len(labels) == 0 {
		t.Fatal("expected detail menu labels")
	}
	foundNav := false
	for i, a := range actions {
		if a == "back" && !disabled[i] {
			foundNav = true
		}
	}
	if !foundNav {
		t.Error("expected enabled back navigation")
	}
}

func TestBuildPipelineMenu_EmptyList(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.pipelineNames = nil
	m.providers.pipelineScroll = 0
	labels, _, _, _ := m.buildPipelineMenu()
	if len(labels) != 2 {
		t.Errorf("empty pipeline list: got %d labels", len(labels))
	}
}

func TestBuildPipelineMenu_WithInactivePipeline(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.pipelineNames = []string{"my-pipe"}
	m.providers.activePipeline = "other"
	m.providers.pipelineScroll = 0
	labels, _, disabled, _ := m.buildPipelineMenu()
	if labels[0] != "Activate Pipeline" {
		t.Errorf("inactive pipeline label: %s", labels[0])
	}
	if disabled[0] {
		t.Error("Activate Pipeline should not be disabled")
	}
}

func TestBuildPipelineMenu_ActivePipeline(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.pipelineNames = []string{"my-pipe"}
	m.providers.activePipeline = "my-pipe"
	m.providers.pipelineScroll = 0
	labels, _, disabled, _ := m.buildPipelineMenu()
	if labels[0] != "Already Active" {
		t.Errorf("active pipeline label: %s", labels[0])
	}
	if !disabled[0] {
		t.Error("Already Active should be disabled")
	}
}

func TestBuildListMenu_RemainingItemsEnabled(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 1 // deepseek (not primary)
	labels, _, disabled, _ := m.buildListMenu()
	for i, l := range labels {
		if strings.Contains(l, "Set as Primary") && disabled[i] {
			t.Errorf("Set as Primary should be enabled for non-primary: %s", l)
		}
	}
}
