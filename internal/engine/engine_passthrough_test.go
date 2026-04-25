package engine

import (
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
	mgr := ctxmgr.New(codemap.New(ast.New()))
	return &Engine{
		Config:      cfg,
		Context:     mgr,
		ProjectRoot: t.TempDir(),
		Tools:       tools.New(*cfg),
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
		Question:          "fix the parser",
		Step:              5,
		CumulativeSteps:   12,
		TotalTokens:       50000,
		CumulativeTokens:  45000,
		ContextTokens:     8000,
		LastProvider:      "anthropic",
		LastModel:         "claude-opus-4-7",
		ToolSource:        "edit_file",
		ParkedAt:          time.Now(),
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
