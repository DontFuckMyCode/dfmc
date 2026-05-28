package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func newTestEngineForPassthrough(t *testing.T) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	mgr := ctxmgr.New(codemap.New(ast.New(), nil))
	return &Engine{
		Config:      cfg,
		Context:     mgr,
		ProjectRoot: t.TempDir(),
		Tools:       tools.NewFromConfig(cfg),
	}
}

// ParkedAgentSummary tests

func TestParkedAgentSummary_NilEngine(t *testing.T) {
	var e *Engine
	if got := e.ParkedAgentSummary(); got != "" {
		t.Errorf("nil engine: got %q", got)
	}
}

func TestParkedAgentSummary_NoParked(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	if got := e.ParkedAgentSummary(); got != "" {
		t.Errorf("no parked: got %q", got)
	}
}

func TestParkedAgentSummary_WithParked(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.agentMu.Lock()
	e.agentParked = &parkedAgentState{
		Question: "refactor the auth layer to support SSO",
		Step:     7,
	}
	e.agentMu.Unlock()
	got := e.ParkedAgentSummary()
	if got == "" {
		t.Fatal("expected non-empty summary")
	}
	if !contains(got, "parked at step 7") {
		t.Errorf("summary missing step info: %q", got)
	}
}

func TestParkedAgentSummary_TruncatesLongQuestion(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	longQ := "this is a very long question that exceeds eighty characters and should be truncated in the summary"
	e.agentMu.Lock()
	e.agentParked = &parkedAgentState{
		Question: longQ,
		Step:     3,
	}
	e.agentMu.Unlock()
	got := e.ParkedAgentSummary()
	// The question part is truncated to 80 chars (77 + "...")
	if len(got) < 80 {
		t.Errorf("expected truncated question, got %q", got)
	}
	if !contains(got, "...") {
		t.Errorf("expected ... truncation marker, got %q", got)
	}
}

// ParkedAgentDetails tests

func TestParkedAgentDetails_NilEngine(t *testing.T) {
	var e *Engine
	details, ok := e.ParkedAgentDetails()
	if ok {
		t.Error("nil engine should return ok=false")
	}
	if details != nil {
		t.Errorf("nil engine: got %+v", details)
	}
}

func TestParkedAgentDetails_NoParked(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	details, ok := e.ParkedAgentDetails()
	if ok {
		t.Error("no parked should return ok=false")
	}
	if details != nil {
		t.Errorf("no parked: got %+v", details)
	}
}

func TestParkedAgentDetails_WithParked(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.agentMu.Lock()
	e.agentParked = &parkedAgentState{
		Question:         "fix the parser",
		Step:             5,
		CumulativeSteps:  12,
		TotalTokens:      50000,
		CumulativeTokens: 45000,
		ContextTokens:    8000,
		LastProvider:     "anthropic",
		LastModel:        "claude-opus-4-7",
		ToolSource:       "edit_file",
		ParkedAt:         time.Now(),
	}
	e.agentMu.Unlock()
	details, ok := e.ParkedAgentDetails()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if details.Step != 5 {
		t.Errorf("Step: got %d", details.Step)
	}
	if details.CumulativeSteps != 12 {
		t.Errorf("CumulativeSteps: got %d", details.CumulativeSteps)
	}
	if details.Question != "fix the parser" {
		t.Errorf("Question: got %q", details.Question)
	}
}

// QueueAgentNote tests

func TestQueueAgentNote_NilEngine(t *testing.T) {
	var e *Engine
	e.QueueAgentNote("test note")
}

func TestQueueAgentNote_EmptyNote(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.QueueAgentNote("   ")
	e.agentMu.Lock()
	if len(e.agentNotesQueue) != 0 {
		t.Errorf("empty note should not be queued, got %d", len(e.agentNotesQueue))
	}
	e.agentMu.Unlock()
}

func TestQueueAgentNote_WithEngine(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.QueueAgentNote("test note one")
	e.QueueAgentNote("test note two")
	e.agentMu.Lock()
	if len(e.agentNotesQueue) != 2 {
		t.Errorf("Notes count: got %d", len(e.agentNotesQueue))
	}
	if e.agentNotesQueue[0] != "test note one" {
		t.Errorf("first note: got %q", e.agentNotesQueue[0])
	}
	e.agentMu.Unlock()
}

// Passthrough function tests

func TestSetProviderModel(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.SetProviderModel("openai", "gpt-4o")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.providerOverride != "openai" {
		t.Errorf("providerOverride: got %q", e.providerOverride)
	}
	if e.modelOverride != "gpt-4o" {
		t.Errorf("modelOverride: got %q", e.modelOverride)
	}
}

func TestProviderModelUsesFrontierTierPrimary(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.Config.Providers.Primary = "zai"
	e.Config.Providers.Profiles = map[string]config.ModelConfig{
		"zai": {
			Model:    "first-catalog-model",
			Models:   []string{"first-catalog-model", "glm-5.1"},
			Protocol: "openai-compatible",
			BaseURL:  "https://api.z.ai/api/coding/paas/v4",
			APIKey:   "test-key",
		},
	}
	e.Config.Routing.Tiers = map[string]config.TierRouting{
		"frontier": {Primary: "zai:glm-5.1"},
	}

	if got := e.provider(); got != "zai" {
		t.Fatalf("provider(): got %q", got)
	}
	if got := e.model(); got != "glm-5.1" {
		t.Fatalf("model(): got %q", got)
	}
	status := e.Status()
	if status.Provider != "zai" || status.Model != "glm-5.1" {
		t.Fatalf("Status(): got %s:%s", status.Provider, status.Model)
	}
}

func TestBuildNativeLoopRequestUsesFrontierTierPrimaryModel(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.Config.Providers.Primary = "zai"
	e.Config.Providers.Profiles = map[string]config.ModelConfig{
		"zai": {
			Model:    "first-catalog-model",
			Models:   []string{"first-catalog-model", "glm-5.1"},
			Protocol: "openai-compatible",
			BaseURL:  "https://api.z.ai/api/coding/paas/v4",
			APIKey:   "test-key",
		},
	}
	e.Config.Routing.Tiers = map[string]config.TierRouting{
		"frontier": {Primary: "zai:glm-5.1"},
	}

	req := e.buildNativeLoopRequest(&loopRunState{}, "auto")
	if req.Provider != "zai" || req.Model != "glm-5.1" {
		t.Fatalf("buildNativeLoopRequest(): got %s:%s", req.Provider, req.Model)
	}
}

func TestProviderModelOverrideBeatsFrontierTierPrimary(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.Config.Providers.Profiles = map[string]config.ModelConfig{
		"zai": {
			Model:    "first-catalog-model",
			Models:   []string{"first-catalog-model", "glm-5.1", "glm-4.7"},
			Protocol: "openai-compatible",
			BaseURL:  "https://api.z.ai/api/coding/paas/v4",
			APIKey:   "test-key",
		},
	}
	e.Config.Routing.Tiers = map[string]config.TierRouting{
		"frontier": {Primary: "zai:glm-5.1"},
	}

	e.SetProviderModel("zai", "glm-4.7")

	if got := e.provider(); got != "zai" {
		t.Fatalf("provider(): got %q", got)
	}
	if got := e.model(); got != "glm-4.7" {
		t.Fatalf("model(): got %q", got)
	}
}

func TestSetPrimaryProvider(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.SetPrimaryProvider("anthropic")
}

func TestSetFallbackProviders(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.SetFallbackProviders([]string{"openai", "local"})
}

func TestFallbackProviders(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.SetFallbackProviders([]string{"openai", "local"})
	got := e.FallbackProviders()
	if len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
}

func TestSetVerbose(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.SetVerbose(true)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.verbose {
		t.Error("verbose should be true")
	}
}

// IsUser and String for Source type

func TestSource_IsUser(t *testing.T) {
	if !SourceUser.IsUser() {
		t.Error("SourceUser should be user")
	}
	if SourceWeb.IsUser() {
		t.Error("SourceWeb should not be user")
	}
	if SourceWS.IsUser() {
		t.Error("SourceWS should not be user")
	}
	if SourceMCP.IsUser() {
		t.Error("SourceMCP should not be user")
	}
	if SourceCLI.IsUser() {
		t.Error("SourceCLI should not be user")
	}
}

func TestSource_String(t *testing.T) {
	if SourceUser.String() != "user" {
		t.Errorf("SourceUser.String(): got %q", SourceUser.String())
	}
	if SourceWeb.String() != "web" {
		t.Errorf("SourceWeb.String(): got %q", SourceWeb.String())
	}
	if SourceWS.String() != "ws" {
		t.Errorf("SourceWS.String(): got %q", SourceWS.String())
	}
	if SourceMCP.String() != "mcp" {
		t.Errorf("SourceMCP.String(): got %q", SourceMCP.String())
	}
	if SourceCLI.String() != "cli" {
		t.Errorf("SourceCLI.String(): got %q", SourceCLI.String())
	}
}

// itoaInt tests
func TestItoaInt(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "10"},
		{99, "99"},
		{100, "100"},
		{-1, "-1"},
		{-10, "-10"},
	}
	for _, tt := range tests {
		if got := itoaInt(tt.n); got != tt.want {
			t.Errorf("itoaInt(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Nil-Tools tool passthrough coverage: these functions all have nil-guards
// that only execute when e.Tools is nil.

func TestListTools_NilTools(t *testing.T) {
	e := &Engine{}
	if got := e.ListTools(); got != nil {
		t.Errorf("nil Tools: got %v", got)
	}
}

func TestSetToolEnabled_NilTools(t *testing.T) {
	e := &Engine{}
	if err := e.SetToolEnabled("mytool", false); err != nil {
		t.Errorf("nil Tools: %v", err)
	}
}

func TestIsToolDisabled_NilTools(t *testing.T) {
	e := &Engine{}
	if e.IsToolDisabled("mytool") {
		t.Error("nil Tools: should not be disabled")
	}
}

func TestListDisabledTools_NilTools(t *testing.T) {
	e := &Engine{}
	if got := e.ListDisabledTools(); got != nil {
		t.Errorf("nil Tools: got %v", got)
	}
}

func TestToolIsProtected(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	// edit_file is protected by default
	if !e.ToolIsProtected("edit_file") {
		t.Error("edit_file should be protected")
	}
	// non-protected tool
	if e.ToolIsProtected("nonexistent") {
		t.Error("nonexistent tool should not be protected")
	}
}

// ListAllTasks nil engine
func TestListAllTasks_NilEngine(t *testing.T) {
	var e *Engine
	got, err := e.ListAllTasks()
	if err != nil {
		t.Errorf("nil engine: %v", err)
	}
	if got != nil {
		t.Errorf("nil engine: got %v", got)
	}
}

// Engine core function tests

func TestNewWithVersion_NilConfig(t *testing.T) {
	_, err := NewWithVersion(nil, "test")
	if err == nil {
		t.Error("nil config should return error")
	}
	if err.Error() != "config is nil" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewWithVersion_ValidConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	e, err := NewWithVersion(cfg, "1.2.3")
	if err != nil {
		t.Fatalf("NewWithVersion: %v", err)
	}
	if e.Version != "1.2.3" {
		t.Errorf("version: got %q", e.Version)
	}
	if e.state != StateCreated {
		t.Errorf("state: got %v", e.state)
	}
	if e.EventBus == nil {
		t.Error("EventBus should be created")
	}
}

func TestNew_DefaultVersion(t *testing.T) {
	cfg := config.DefaultConfig()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.Version != "dev" {
		t.Errorf("default version: got %q", e.Version)
	}
}

func TestSetTelegramBot(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	if e.TelegramBot != nil {
		t.Error("bot should be nil initially")
	}
	// SetTelegramBot with nil is a no-op
	e.SetTelegramBot(nil, "test", nil)
	if e.TelegramBot != nil {
		t.Error("nil bot should stay nil")
	}
}

func TestEngineState_Transition(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	if e.State() != StateCreated {
		t.Fatalf("initial state: got %v", e.State())
	}

	e.setState(StateInitializing)
	if e.State() != StateInitializing {
		t.Errorf("State: got %v", e.State())
	}

	e.setState(StateReady)
	if e.State() != StateReady {
		t.Errorf("State: got %v", e.State())
	}
}

func TestRequireReady_NilEngine(t *testing.T) {
	var e *Engine
	err := e.requireReady("test op")
	if err != ErrEngineNil {
		t.Errorf("nil engine: got %v", err)
	}
}

func TestRequireReady_WrongState(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.setState(StateCreated)
	err := e.requireReady("test op")
	if err == nil {
		t.Error("StateCreated should fail requireReady")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("error should mention not initialized: %v", err)
	}
}

func TestRequireReady_CorrectStates(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	for _, state := range []EngineState{StateReady, StateServing, StateShuttingDown} {
		e.setState(state)
		if err := e.requireReady("test"); err != nil {
			t.Errorf("state %v should pass: %v", state, err)
		}
	}
}

func TestRequireReady_EmptyOpUsesDefault(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	e.setState(StateCreated)
	err := e.requireReady("")
	if err == nil {
		t.Error("empty op should still return an error")
	}
	if !strings.Contains(err.Error(), "operation") {
		t.Errorf("empty op should default to 'operation': %v", err)
	}
}

func TestBackgroundContext_NilBackgroundCtx(t *testing.T) {
	e := &Engine{}
	ctx := e.BackgroundContext()
	if ctx == nil {
		t.Error("BackgroundContext should return background context, not nil")
	}
}

func TestStartBackgroundTask_NilFn(t *testing.T) {
	e := newTestEngineForPassthrough(t)
	// nil fn is a no-op — just verify no panic
	e.StartBackgroundTask("test", nil)
}
