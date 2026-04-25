package intent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// stubProvider is a minimal provider.Provider used by these tests. It
// returns a fixed CompletionResponse and records the request it saw so
// assertions can verify the prompt the router built. We deliberately
// don't depend on real provider implementations: the unit under test is
// the router's prompt construction + JSON parsing + fail-open behavior,
// not Anthropic/OpenAI/etc. wire formats.
type stubProvider struct {
	response  string
	err       error
	delay     time.Duration
	supports  bool
	gotReq    provider.CompletionRequest
	calledN   int
	modelName string
}

func (s *stubProvider) Name() string  { return "stub" }
func (s *stubProvider) Model() string { return s.modelName }
func (s *stubProvider) Models() []string { return []string{s.modelName} }
func (s *stubProvider) Complete(ctx context.Context, req provider.CompletionRequest) (*provider.CompletionResponse, error) {
	s.calledN++
	s.gotReq = req
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return &provider.CompletionResponse{Text: s.response}, nil
}
func (s *stubProvider) Stream(ctx context.Context, req provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("stream not supported in stub")
}
func (s *stubProvider) CountTokens(text string) int { return len(text) / 4 }
func (s *stubProvider) MaxContext() int             { return 200000 }
func (s *stubProvider) Hints() provider.ProviderHints {
	return provider.ProviderHints{SupportsTools: s.supports}
}

func newRouter(t *testing.T, prov provider.Provider) *Router {
	t.Helper()
	return NewRouter(config.IntentConfig{
		Enabled:   true,
		TimeoutMs: 1000,
		FailOpen:  true,
	}, func(name string) (provider.Provider, bool) {
		if prov == nil {
			return nil, false
		}
		return prov, true
	})
}

func TestEvaluate_FallbackWhenDisabled(t *testing.T) {
	r := NewRouter(config.IntentConfig{Enabled: false}, func(string) (provider.Provider, bool) {
		return nil, false
	})
	dec, err := r.Evaluate(context.Background(), "hello", Snapshot{})
	if err != nil {
		t.Fatalf("disabled router should never error, got %v", err)
	}
	if dec.Source != "fallback" {
		t.Fatalf("disabled router should produce fallback, got source=%q", dec.Source)
	}
	if dec.EnrichedRequest != "hello" {
		t.Fatalf("fallback should pass raw through, got %q", dec.EnrichedRequest)
	}
	if dec.Intent != IntentNew {
		t.Fatalf("fallback intent should be new, got %q", dec.Intent)
	}
}

func TestEvaluate_FallbackWhenLookupNil(t *testing.T) {
	r := NewRouter(config.IntentConfig{Enabled: true, FailOpen: true}, nil)
	dec, err := r.Evaluate(context.Background(), "hello", Snapshot{})
	if err != nil {
		t.Fatalf("nil lookup should fall back without error, got %v", err)
	}
	if dec.Source != "fallback" {
		t.Fatalf("expected fallback source, got %q", dec.Source)
	}
}

func TestEvaluate_FallbackWhenProviderMissing(t *testing.T) {
	r := NewRouter(config.IntentConfig{
		Enabled:  true,
		Provider: "nonexistent",
		FailOpen: true,
	}, func(string) (provider.Provider, bool) { return nil, false })
	dec, _ := r.Evaluate(context.Background(), "hi", Snapshot{})
	if dec.Source != "fallback" {
		t.Fatalf("missing provider should fall back, got %q", dec.Source)
	}
}

func TestEvaluate_ParsesResumeIntent(t *testing.T) {
	prov := &stubProvider{
		supports: true,
		response: `{
			"intent": "resume",
			"enriched_request": "Continue the C1 refactor: extract chatState",
			"reasoning": "user said devam et and a parked agent exists",
			"follow_up_question": ""
		}`,
	}
	r := newRouter(t, prov)
	snap := Snapshot{Parked: true, ParkedSummary: "parked at step 7"}
	dec, err := r.Evaluate(context.Background(), "devam et", snap)
	if err != nil {
		t.Fatalf("Evaluate err: %v", err)
	}
	if dec.Intent != IntentResume {
		t.Fatalf("want resume, got %q", dec.Intent)
	}
	if !strings.Contains(dec.EnrichedRequest, "C1 refactor") {
		t.Fatalf("enriched should carry rewrite, got %q", dec.EnrichedRequest)
	}
	if dec.Source != "llm" {
		t.Fatalf("real LLM call should mark source=llm, got %q", dec.Source)
	}
	if prov.calledN != 1 {
		t.Fatalf("expected 1 provider call, got %d", prov.calledN)
	}
	// Snapshot rendering should show up in the user message we sent.
	if !strings.Contains(prov.gotReq.Messages[0].Content, "PARKED_AGENT: yes") {
		t.Fatalf("user message should embed snapshot, got %q", prov.gotReq.Messages[0].Content)
	}
}

func TestEvaluate_ParsesNewIntent(t *testing.T) {
	prov := &stubProvider{
		supports: true,
		response: `{"intent":"new","enriched_request":"List all .go files in the project root","reasoning":"fresh question, no parked work","follow_up_question":""}`,
	}
	r := newRouter(t, prov)
	dec, _ := r.Evaluate(context.Background(), "list go files", Snapshot{})
	if dec.Intent != IntentNew {
		t.Fatalf("want new, got %q", dec.Intent)
	}
	if dec.EnrichedRequest != "List all .go files in the project root" {
		t.Fatalf("enriched mismatch, got %q", dec.EnrichedRequest)
	}
}

func TestEvaluate_ParsesClarifyIntent(t *testing.T) {
	prov := &stubProvider{
		supports: true,
		response: `{"intent":"clarify","enriched_request":"","reasoning":"too vague","follow_up_question":"What would you like fixed?"}`,
	}
	r := newRouter(t, prov)
	dec, _ := r.Evaluate(context.Background(), "fix it", Snapshot{})
	if dec.Intent != IntentClarify {
		t.Fatalf("want clarify, got %q", dec.Intent)
	}
	if dec.FollowUpQuestion != "What would you like fixed?" {
		t.Fatalf("follow-up missing, got %q", dec.FollowUpQuestion)
	}
}

func TestEvaluate_StripsCodeFences(t *testing.T) {
	prov := &stubProvider{
		supports: true,
		response: "```json\n{\"intent\":\"new\",\"enriched_request\":\"Run tests\",\"reasoning\":\"ok\",\"follow_up_question\":\"\"}\n```",
	}
	r := newRouter(t, prov)
	dec, err := r.Evaluate(context.Background(), "run tests", Snapshot{})
	if err != nil {
		t.Fatalf("fenced JSON should parse, got %v", err)
	}
	if dec.Intent != IntentNew {
		t.Fatalf("expected new, got %q", dec.Intent)
	}
}

func TestEvaluate_FailOpenOnInvalidJSON(t *testing.T) {
	prov := &stubProvider{supports: true, response: "not json at all"}
	r := newRouter(t, prov)
	dec, err := r.Evaluate(context.Background(), "hi", Snapshot{})
	if err != nil {
		t.Fatalf("FailOpen should swallow parse errors, got %v", err)
	}
	if dec.Source != "fallback" {
		t.Fatalf("expected fallback on bad JSON, got source=%q", dec.Source)
	}
	if dec.EnrichedRequest != "hi" {
		t.Fatalf("fallback should pass raw, got %q", dec.EnrichedRequest)
	}
}

func TestEvaluate_FailClosedOnInvalidJSON(t *testing.T) {
	prov := &stubProvider{supports: true, response: "garbage"}
	r := NewRouter(config.IntentConfig{
		Enabled:  true,
		FailOpen: false,
	}, func(string) (provider.Provider, bool) { return prov, true })
	dec, err := r.Evaluate(context.Background(), "hi", Snapshot{})
	if err == nil {
		t.Fatalf("FailOpen=false should surface JSON errors")
	}
	if dec.EnrichedRequest != "hi" {
		t.Fatalf("decision should still carry raw on fail-closed, got %q", dec.EnrichedRequest)
	}
}

func TestEvaluate_RespectsTimeout(t *testing.T) {
	prov := &stubProvider{
		supports: true,
		delay:    200 * time.Millisecond,
		response: `{"intent":"new","enriched_request":"x","reasoning":"y","follow_up_question":""}`,
	}
	r := NewRouter(config.IntentConfig{
		Enabled:   true,
		TimeoutMs: 30,
		FailOpen:  true,
	}, func(string) (provider.Provider, bool) { return prov, true })
	start := time.Now()
	dec, _ := r.Evaluate(context.Background(), "hello", Snapshot{})
	elapsed := time.Since(start)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("router should have given up at ~30ms, ran for %v", elapsed)
	}
	if dec.Source != "fallback" {
		t.Fatalf("timeout should fall back, got source=%q", dec.Source)
	}
}

func TestEvaluate_EmptyRawShortCircuits(t *testing.T) {
	prov := &stubProvider{supports: true}
	r := newRouter(t, prov)
	dec, _ := r.Evaluate(context.Background(), "   ", Snapshot{})
	if prov.calledN != 0 {
		t.Fatalf("empty input should not call provider, got %d calls", prov.calledN)
	}
	if dec.Source != "fallback" {
		t.Fatalf("empty input should fall back, got %q", dec.Source)
	}
}

func TestEvaluate_RejectsUnknownIntent(t *testing.T) {
	prov := &stubProvider{
		supports: true,
		response: `{"intent":"escalate","enriched_request":"foo","reasoning":"y","follow_up_question":""}`,
	}
	r := newRouter(t, prov)
	dec, _ := r.Evaluate(context.Background(), "hi", Snapshot{})
	if dec.Source != "fallback" {
		t.Fatalf("unknown intent value should fall back, got %q", dec.Source)
	}
}

func TestSnapshotRender_IncludesAllSignals(t *testing.T) {
	snap := Snapshot{
		Parked:           true,
		ParkedSummary:    "parked at step 5 — refactor x",
		ParkedStep:       5,
		ParkedToolName:   "edit_file",
		ParkedAt:         time.Now().Add(-2 * time.Minute),
		CumulativeSteps:  12,
		CumulativeTokens: 45000,
		Provider:         "anthropic",
		Model:            "claude-opus-4-7",
		LastAssistant:    "Here is the refactored file...",
		RecentToolNames:  []string{"read_file", "edit_file", "grep_codebase"},
		UserTurnCount:    7,
	}
	out := snap.Render(2000)
	for _, want := range []string{
		"PARKED_AGENT: yes",
		"step: 5",
		"last_tool: edit_file",
		"ACTIVE_MODEL: anthropic/claude-opus-4-7",
		"USER_TURNS: 7",
		"RECENT_TOOLS: read_file, edit_file, grep_codebase",
		"LAST_ASSISTANT:",
		"Here is the refactored file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot render missing %q:\n%s", want, out)
		}
	}
}

func TestSnapshotRender_TruncatesPastBudget(t *testing.T) {
	snap := Snapshot{
		LastAssistant: strings.Repeat("x", 5000),
	}
	out := snap.Render(200)
	if r := []rune(out); len(r) > 220 { // 200 + "(truncated)" tail
		t.Fatalf("snapshot should be truncated to ~200 runes, got %d", len(r))
	}
}

func TestSnapshotRender_EmptyStateProducesPlaceholder(t *testing.T) {
	out := Snapshot{}.Render(200)
	if !strings.Contains(out, "PARKED_AGENT: no") {
		t.Fatalf("empty snapshot should still spell out PARKED_AGENT: no, got %q", out)
	}
}

func TestStripCodeFences(t *testing.T) {
	cases := map[string]string{
		"plain":                    "plain",
		"```\nfoo\n```":            "foo",
		"```json\n{\"a\":1}\n```":  `{"a":1}`,
		"```yaml\nkey: value\n```": "key: value",
		"   ```\nbar\n```\n":       "bar",
	}
	for in, want := range cases {
		got := stripCodeFences(strings.TrimSpace(in))
		if got != want {
			t.Errorf("stripCodeFences(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRouter_Enabled_NilRouter(t *testing.T) {
	var r *Router
	if r.Enabled() {
		t.Error("nil router should not be enabled")
	}
}

func TestRouter_Enabled_DisabledConfig(t *testing.T) {
	prov := &stubProvider{supports: true, modelName: "test"}
	r := NewRouter(config.IntentConfig{
		Enabled: false,
	}, func(string) (provider.Provider, bool) { return prov, true })
	if r.Enabled() {
		t.Error("disabled config should not be enabled")
	}
}

func TestRouter_Enabled_NoProvider(t *testing.T) {
	r := NewRouter(config.IntentConfig{
		Enabled: true,
	}, func(string) (provider.Provider, bool) { return nil, false })
	if r.Enabled() {
		t.Error("no provider should not be enabled")
	}
}

func TestRouter_Enabled_AllGood(t *testing.T) {
	prov := &stubProvider{supports: true, modelName: "test"}
	r := NewRouter(config.IntentConfig{
		Enabled: true,
	}, func(string) (provider.Provider, bool) { return prov, true })
	if !r.Enabled() {
		t.Error("all conditions met should be enabled")
	}
}

func TestInvalidJSONError_Unwrap(t *testing.T) {
	innerErr := errors.New("json: unexpected end of input")
	e := &invalidJSONError{raw: "bad", err: innerErr}
	got := e.Unwrap()
	if got != innerErr {
		t.Errorf("Unwrap() = %v, want %v", got, innerErr)
	}
}
