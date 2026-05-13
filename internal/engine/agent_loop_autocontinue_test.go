package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// TestAutoContinue_ResumesUntilDone verifies the auto-continue wrapper
// re-enters askWithNativeTools when the assistant emits [done: false]
// (or omits the marker) and stops once [done: true] arrives. The
// concatenated answer must contain both turn outputs.
func TestAutoContinue_ResumesUntilDone(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.AutoContinue = "auto"
	cfg.Agent.MaxAutoContinueIterations = 3
	cfg.Intent.Enabled = false // skip intent layer for deterministic routing

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{Text: "Step one done.\n\n[next:\n- finish step two]\n[cleanup: ]\n[done: false]"},
			{Text: "Step two done.\n\n[next:\n- ship it]\n[cleanup: ]\n[done: true]"},
		},
	}
	router.Register(stub)

	bus := NewEventBus()
	evCh := bus.Subscribe("*")
	defer bus.Unsubscribe("*", evCh)

	eng := &Engine{
		Config:       cfg,
		EventBus:     bus,
		ProjectRoot:  tmp,
		Providers:    router,
		Tools:        tools.New(*cfg),
		Conversation: conversation.New(nil),
	}
	eng.setState(StateReady)

	answer, err := eng.AskWithMetadata(context.Background(), "do the two-step task")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	if !strings.Contains(answer, "Step one done") {
		t.Errorf("expected answer to contain step-one output, got %q", answer)
	}
	if !strings.Contains(answer, "Step two done") {
		t.Errorf("expected answer to contain step-two output (auto-continue), got %q", answer)
	}
	if strings.Contains(answer, "[done:") || strings.Contains(answer, "[next:") {
		t.Errorf("markers leaked into final answer: %q", answer)
	}
	if got := countEventsByType(evCh, "assistant:auto_continue"); got != 1 {
		t.Errorf("expected exactly 1 auto-continue event, got %d", got)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.requests) != 2 {
		t.Fatalf("expected 2 provider requests (one per iteration), got %d", len(stub.requests))
	}
}

// countEventsByType drains the bus channel non-blockingly and returns
// the count of matching event types. Used by the auto-continue tests
// to verify how many `assistant:auto_continue` events were emitted.
func countEventsByType(ch <-chan Event, want string) int {
	count := 0
	for {
		select {
		case ev := <-ch:
			if ev.Type == want {
				count++
			}
		default:
			return count
		}
	}
}

// TestAutoContinue_StopsOnDoneTrue verifies that when the very first
// answer carries [done: true], no auto-continue iteration fires and
// only one provider request is made.
func TestAutoContinue_StopsOnDoneTrue(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.AutoContinue = "auto"
	cfg.Agent.MaxAutoContinueIterations = 3
	cfg.Intent.Enabled = false

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{Text: "All done.\n\n[next:\n- nothing pending]\n[cleanup: ]\n[done: true]"},
		},
	}
	router.Register(stub)

	bus := NewEventBus()
	evCh := bus.Subscribe("*")
	defer bus.Unsubscribe("*", evCh)

	eng := &Engine{
		Config:       cfg,
		EventBus:     bus,
		ProjectRoot:  tmp,
		Providers:    router,
		Tools:        tools.New(*cfg),
		Conversation: conversation.New(nil),
	}
	eng.setState(StateReady)

	answer, err := eng.AskWithMetadata(context.Background(), "is it done?")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	if !strings.HasPrefix(answer, "All done.") {
		t.Errorf("expected answer to start with All done, got %q", answer)
	}
	if got := countEventsByType(evCh, "assistant:auto_continue"); got != 0 {
		t.Errorf("expected zero auto-continue events on done=true, got %d", got)
	}
}

func TestAutoContinue_SelfSelectsWhenNextMissing(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{Model: "stub-model", MaxTokens: 4096, MaxContext: 128000}
	cfg.Agent.AutoContinue = "auto"
	cfg.Agent.MaxAutoContinueIterations = 3
	cfg.Intent.Enabled = false
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{name: "stub", model: "stub-model", hints: newNativeHints(), responses: []scriptedResponse{
		{Text: "I can continue in a few ways:\n1. inspect the failing area\n2. write the fix\n3. run tests\nWhich option should I choose?"},
		{Text: "I chose the safest next step and completed it.\n\n[next:\n- nothing pending]\n[cleanup: ]\n[done: true]"},
	}}
	router.Register(stub)
	bus := NewEventBus()
	evCh := bus.Subscribe("*")
	defer bus.Unsubscribe("*", evCh)
	eng := &Engine{Config: cfg, EventBus: bus, ProjectRoot: tmp, Providers: router, Tools: tools.New(*cfg), Conversation: conversation.New(nil)}
	eng.setState(StateReady)
	answer, err := eng.AskWithMetadata(context.Background(), "finish the task autonomously")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	if !strings.Contains(answer, "I chose the safest next step") {
		t.Fatalf("expected self-selected continuation answer, got %q", answer)
	}
	stub.mu.Lock()
	requestCount := len(stub.requests)
	var secondReq provider.CompletionRequest
	if requestCount >= 2 {
		secondReq = stub.requests[1]
	}
	stub.mu.Unlock()
	if requestCount != 2 {
		t.Fatalf("expected 2 provider requests after self-select fallback, got %d", requestCount)
	}
	if !requestContainsUserText(secondReq, "Do not wait for the user") {
		t.Fatalf("second request did not include autonomous fallback prompt: %#v", secondReq.Messages)
	}
	if got := countEventsByType(evCh, "assistant:auto_continue"); got != 1 {
		t.Errorf("expected one auto-continue event, got %d", got)
	}
	if got := countEventsByType(evCh, "assistant:auto_continue:clarify"); got != 0 {
		t.Errorf("expected no clarify pause event, got %d", got)
	}
}

func TestAutoContinue_DoneTrueChoiceGateStillContinues(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{Model: "stub-model", MaxTokens: 4096, MaxContext: 128000}
	cfg.Agent.AutoContinue = "auto"
	cfg.Agent.MaxAutoContinueIterations = 3
	cfg.Intent.Enabled = false
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{name: "stub", model: "stub-model", hints: newNativeHints(), responses: []scriptedResponse{
		{Text: "Choose one option:\n1. inspect\n2. edit\n3. test\n\n[cleanup: ]\n[done: true]"},
		{Text: "Continued without waiting for a numbered choice.\n\n[next:\n- nothing pending]\n[cleanup: ]\n[done: true]"},
	}}
	router.Register(stub)
	eng := &Engine{Config: cfg, EventBus: NewEventBus(), ProjectRoot: tmp, Providers: router, Tools: tools.New(*cfg), Conversation: conversation.New(nil)}
	eng.setState(StateReady)
	answer, err := eng.AskWithMetadata(context.Background(), "finish without asking me")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	if !strings.Contains(answer, "Continued without waiting") {
		t.Fatalf("expected continuation despite done=true choice gate, got %q", answer)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(stub.requests))
	}
}

// TestAutoContinue_RespectsIterationCap verifies the wrapper stops at
// MaxAutoContinueIterations even if the model never emits [done: true].
func TestAutoContinue_RespectsIterationCap(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.AutoContinue = "auto"
	cfg.Agent.MaxAutoContinueIterations = 2
	cfg.Intent.Enabled = false

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	// Three responses, all [done: false] — wrapper must stop after the
	// initial call + 2 iterations = 3 total provider calls.
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{Text: "round 1.\n\n[next:\n- keep going]\n[cleanup: ]\n[done: false]"},
			{Text: "round 2.\n\n[next:\n- keep going]\n[cleanup: ]\n[done: false]"},
			{Text: "round 3.\n\n[next:\n- keep going]\n[cleanup: ]\n[done: false]"},
			// Extra response so the test fails loudly if the cap leaks.
			{Text: "round 4 — should NOT be called.\n[done: true]"},
		},
	}
	router.Register(stub)

	bus := NewEventBus()
	evCh := bus.Subscribe("*")
	defer bus.Unsubscribe("*", evCh)

	eng := &Engine{
		Config:       cfg,
		EventBus:     bus,
		ProjectRoot:  tmp,
		Providers:    router,
		Tools:        tools.New(*cfg),
		Conversation: conversation.New(nil),
	}
	eng.setState(StateReady)

	answer, err := eng.AskWithMetadata(context.Background(), "go forever")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	stub.mu.Lock()
	requestCount := len(stub.requests)
	stub.mu.Unlock()
	if requestCount != 3 {
		t.Errorf("expected 3 provider requests (1 initial + 2 auto-continues), got %d", requestCount)
	}
	if got := countEventsByType(evCh, "assistant:auto_continue"); got != 2 {
		t.Errorf("expected 2 auto-continue events, got %d", got)
	}
	if strings.Contains(answer, "round 4") {
		t.Errorf("cap leaked: round 4 was called: %q", answer)
	}
}

// TestAutoContinue_DisabledByConfig verifies that when AutoContinue is
// "off" the wrapper short-circuits to single-call behaviour even if
// the model omits [done: true].
func TestAutoContinue_DisabledByConfig(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.AutoContinue = "off"
	cfg.Agent.MaxAutoContinueIterations = 5
	cfg.Intent.Enabled = false

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{Text: "stopped here.\n\n[next:\n- but more pending]\n[cleanup: ]\n[done: false]"},
			{Text: "should NOT be called"},
		},
	}
	router.Register(stub)

	eng := &Engine{
		Config:       cfg,
		EventBus:     NewEventBus(),
		ProjectRoot:  tmp,
		Providers:    router,
		Tools:        tools.New(*cfg),
		Conversation: conversation.New(nil),
	}
	eng.setState(StateReady)

	if _, err := eng.AskWithMetadata(context.Background(), "do it"); err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.requests) != 1 {
		t.Errorf("expected exactly 1 provider request when AutoContinue=off, got %d", len(stub.requests))
	}
}

func TestAutoContinueConfig_Defaults(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		max         int
		wantEnabled bool
		wantMax     int
	}{
		{"default_auto", "auto", 0, true, 5},
		{"explicit_off", "off", 7, false, 7},
		{"explicit_false", "false", 0, false, 5},
		{"explicit_manual", "manual", 0, false, 5},
		{"empty_defaults_to_enabled", "", 0, true, 5},
		{"custom_max", "auto", 12, true, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agent.AutoContinue = tc.mode
			cfg.Agent.MaxAutoContinueIterations = tc.max
			eng := &Engine{Config: cfg}
			got := eng.autoContinueConfig()
			if got.Enabled != tc.wantEnabled {
				t.Errorf("Enabled: got %v want %v", got.Enabled, tc.wantEnabled)
			}
			if got.MaxIterations != tc.wantMax {
				t.Errorf("MaxIterations: got %d want %d", got.MaxIterations, tc.wantMax)
			}
		})
	}
}

func requestContainsUserText(req provider.CompletionRequest, needle string) bool {
	for _, msg := range req.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}
