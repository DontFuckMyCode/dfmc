package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// scriptedResponse is one programmed reply from the fake provider. Each entry
// is either a plain text answer (finalize the loop) or a set of tool calls
// that the engine must dispatch to the backend registry.
type scriptedResponse struct {
	Text      string
	ToolCalls []provider.ToolCall
}

type scriptedProvider struct {
	name       string
	model      string
	hints      provider.ProviderHints
	maxContext int
	mu         sync.Mutex
	responses  []scriptedResponse
	requests   []provider.CompletionRequest
	streamUsed bool
}

func (p *scriptedProvider) Name() string                { return p.name }
func (p *scriptedProvider) Model() string               { return p.model }
func (p *scriptedProvider) CountTokens(text string) int { return len(strings.Fields(text)) }
func (p *scriptedProvider) MaxContext() int {
	if p.maxContext > 0 {
		return p.maxContext
	}
	return 128000
}
func (p *scriptedProvider) Hints() provider.ProviderHints { return p.hints }

func (p *scriptedProvider) Complete(_ context.Context, req provider.CompletionRequest) (*provider.CompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	next := p.responses[0]
	p.responses = p.responses[1:]
	stop := provider.StopEnd
	if len(next.ToolCalls) > 0 {
		stop = provider.StopTool
	}
	return &provider.CompletionResponse{
		Text:       next.Text,
		Model:      p.model,
		Usage:      provider.Usage{InputTokens: 12, OutputTokens: 18, TotalTokens: 30},
		ToolCalls:  next.ToolCalls,
		StopReason: stop,
	}, nil
}

func (p *scriptedProvider) Stream(_ context.Context, _ provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.streamUsed = true
	p.mu.Unlock()
	return nil, fmt.Errorf("unexpected stream call")
}

// toolCallInput marshals any Go value through JSON so we can pass arguments
// the way a real provider would.
func toolCallInput(v any) map[string]any {
	raw, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func newNativeHints() provider.ProviderHints {
	return provider.ProviderHints{
		ToolStyle:     "function-calling",
		MaxContext:    128000,
		DefaultMode:   "balanced",
		SupportsTools: true,
	}
}

func TestAskWithMetadata_NativeToolLoop_DiscoverAndCall(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{
				Text: "",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_1",
						Name: "tool_call",
						Input: toolCallInput(map[string]any{
							"name": "read_file",
							"args": map[string]any{"path": "note.txt", "line_start": 1, "line_end": 1},
						}),
					},
				},
			},
			{Text: "The first line in note.txt is alpha."},
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

	answer, err := eng.AskWithMetadata(context.Background(), "first line of note.txt?")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	if !strings.Contains(answer, "alpha") {
		t.Fatalf("expected final answer to mention alpha, got %q", answer)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(stub.requests))
	}

	first := stub.requests[0]
	if len(first.Tools) == 0 {
		t.Fatalf("expected first request to advertise meta tools, got none")
	}
	if !containsMetaTool(first.Tools, "tool_call") {
		t.Fatalf("expected tool_call meta tool in advertised set, got %v", metaToolNamesFromDescs(first.Tools))
	}

	second := stub.requests[1]
	var sawAssistantToolCall, sawToolResult bool
	for _, msg := range second.Messages {
		if len(msg.ToolCalls) > 0 {
			sawAssistantToolCall = true
		}
		if strings.TrimSpace(msg.ToolCallID) != "" {
			sawToolResult = true
			if !strings.Contains(msg.Content, "alpha") {
				t.Fatalf("expected tool result payload to include file output, got %q", msg.Content)
			}
		}
	}
	if !sawAssistantToolCall {
		t.Fatalf("expected second request to carry the assistant tool_calls turn")
	}
	if !sawToolResult {
		t.Fatalf("expected second request to carry the tool_result turn")
	}

	active := eng.Conversation.Active()
	if active == nil {
		t.Fatal("expected active conversation")
	}
	msgs := active.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected user+assistant messages, got %d", len(msgs))
	}
	assistant := msgs[1]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected one recorded tool call, got %#v", assistant.ToolCalls)
	}
	if len(assistant.Results) != 1 || !assistant.Results[0].Success {
		t.Fatalf("expected one successful tool result, got %#v", assistant.Results)
	}
}

func TestAskWithMetadata_NativeToolLoop_PublishesLifecycleEvents(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_1",
						Name: "tool_call",
						Input: toolCallInput(map[string]any{
							"name": "read_file",
							"args": map[string]any{"path": "note.txt", "line_start": 1, "line_end": 1},
						}),
					},
				},
			},
			{Text: "alpha"},
		},
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}
	evCh := eng.EventBus.Subscribe("*")
	defer eng.EventBus.Unsubscribe("*", evCh)

	if _, err := eng.AskWithMetadata(context.Background(), "read note"); err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}

	events := collectRecentEvents(evCh, 64, 150*time.Millisecond)
	for _, expected := range []string{
		"agent:loop:start",
		"agent:loop:thinking",
		"tool:call",
		"tool:result",
		"agent:loop:final",
	} {
		if !containsEventType(events, expected) {
			t.Fatalf("expected %s event, got %v", expected, eventTypes(events))
		}
	}

	start, ok := findEventByType(events, "agent:loop:start")
	if !ok {
		t.Fatalf("expected agent:loop:start event, got %v", eventTypes(events))
	}
	payload, _ := start.Payload.(map[string]any)
	if got, _ := payload["surface"].(string); got != "native" {
		t.Fatalf("expected surface=native, got %v (payload=%v)", got, payload)
	}
	if names, _ := payload["meta_tools"].([]string); !containsString(names, "tool_call") {
		t.Fatalf("expected meta_tools to include tool_call, got %v", names)
	}
}

func TestStreamAsk_NativeToolLoop_AvoidsProviderStream(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_1",
						Name: "tool_call",
						Input: toolCallInput(map[string]any{
							"name": "read_file",
							"args": map[string]any{"path": "main.go", "line_start": 1, "line_end": 1},
						}),
					},
				},
			},
			{Text: "File starts with package main."},
		},
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}

	stream, err := eng.StreamAsk(context.Background(), "what does main.go start with?")
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	var out strings.Builder
	for ev := range stream {
		switch ev.Type {
		case provider.StreamDelta:
			out.WriteString(ev.Delta)
		case provider.StreamError:
			t.Fatalf("stream error: %v", ev.Err)
		}
	}
	if !strings.Contains(out.String(), "package main") {
		t.Fatalf("expected streamed answer to mention package main, got %q", out.String())
	}
	if stub.streamUsed {
		t.Fatal("native tool loop must not invoke provider Stream")
	}
}

func TestAskWithMetadata_NativeToolLoop_BatchCall(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	calls := []map[string]any{
		{"name": "read_file", "args": map[string]any{"path": "a.txt", "line_start": 1, "line_end": 1}},
		{"name": "read_file", "args": map[string]any{"path": "b.txt", "line_start": 1, "line_end": 1}},
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{
				ToolCalls: []provider.ToolCall{
					{
						ID:    "batch_1",
						Name:  "tool_batch_call",
						Input: toolCallInput(map[string]any{"calls": calls}),
					},
				},
			},
			{Text: "Both files are one byte long."},
		},
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}

	answer, err := eng.AskWithMetadata(context.Background(), "compare sizes")
	if err != nil {
		t.Fatalf("AskWithMetadata: %v", err)
	}
	if !strings.Contains(answer, "one byte") {
		t.Fatalf("expected final answer, got %q", answer)
	}
}

// TestAskWithMetadata_NativeToolLoop_RespectsConfiguredMaxSteps checks that
// cfg.Agent.MaxToolSteps overrides the package default: with a script that
// keeps asking for tools indefinitely, the loop parks after exactly N rounds,
// publishes agent:loop:parked, and preserves a resumable snapshot so the
// user can /continue.
func TestAskWithMetadata_NativeToolLoop_RespectsConfiguredMaxSteps(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.MaxToolSteps = 2
	cfg.Agent.MaxToolTokens = 0 // disable token budget for this test

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	loopingCall := scriptedResponse{
		ToolCalls: []provider.ToolCall{
			{
				ID:   "call_loop",
				Name: "tool_call",
				Input: toolCallInput(map[string]any{
					"name": "read_file",
					"args": map[string]any{"path": "note.txt", "line_start": 1, "line_end": 1},
				}),
			},
		},
	}
	stub := &scriptedProvider{
		name:      "stub",
		model:     "stub-model",
		hints:     newNativeHints(),
		responses: []scriptedResponse{loopingCall, loopingCall, loopingCall, loopingCall},
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}
	evCh := eng.EventBus.Subscribe("*")
	defer eng.EventBus.Unsubscribe("*", evCh)

	answer, err := eng.AskWithMetadata(context.Background(), "loop forever")
	if err != nil {
		t.Fatalf("expected parked completion (no error), got %v", err)
	}
	if !strings.Contains(answer, "parked at step 2") {
		t.Fatalf("expected parked-at-step-2 notice, got %q", answer)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected parked state to be preserved for resume")
	}

	stub.mu.Lock()
	reqCount := len(stub.requests)
	stub.mu.Unlock()
	if reqCount != 2 {
		t.Fatalf("expected exactly 2 provider requests under MaxToolSteps=2, got %d", reqCount)
	}

	events := collectRecentEvents(evCh, 64, 150*time.Millisecond)
	ev, ok := findEventByType(events, "agent:loop:parked")
	if !ok {
		t.Fatalf("expected agent:loop:parked event, got %v", eventTypes(events))
	}
	payload, _ := ev.Payload.(map[string]any)
	if got, _ := payload["max_tool_steps"].(int); got != 2 {
		t.Fatalf("expected max_tool_steps=2 in payload, got %v", payload["max_tool_steps"])
	}
}

// TestAskWithMetadata_NativeToolLoop_RespectsTokenBudget verifies that the
// token-budget hard limit (cfg.Agent.MaxToolTokens) parks the loop (rather
// than erroring out and dropping work) when headroom is gone. It must emit
// both agent:loop:budget_exhausted (telemetry) and agent:loop:parked (UI
// resume prompt). The scripted provider reports 30 TotalTokens per call; a
// 25-token budget should trip after the first round.
func TestAskWithMetadata_NativeToolLoop_RespectsTokenBudget(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.MaxToolSteps = 8
	cfg.Agent.MaxToolTokens = 25 // below scripted per-call usage of 30

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		// Keep MaxContext small enough that the elastic budget scaler
		// (~60% of window) can't beat the 25-token cfg floor — otherwise
		// this test wouldn't trip the budget path it's designed to cover.
		maxContext: 40,
		responses: []scriptedResponse{
			{
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_1",
						Name: "tool_call",
						Input: toolCallInput(map[string]any{
							"name": "read_file",
							"args": map[string]any{"path": "note.txt", "line_start": 1, "line_end": 1},
						}),
					},
				},
			},
			// If the budget check didn't fire, the loop would consume this.
			{Text: "should never reach final answer"},
		},
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}
	evCh := eng.EventBus.Subscribe("*")
	defer eng.EventBus.Unsubscribe("*", evCh)

	answer, err := eng.AskWithMetadata(context.Background(), "budget trip")
	if err != nil {
		t.Fatalf("expected budget trip to park (no error), got %v", err)
	}
	if !strings.Contains(answer, "tool budget exhausted") {
		t.Fatalf("expected parked notice mentioning tool budget, got %q", answer)
	}
	if !strings.Contains(answer, "/continue") {
		t.Fatalf("expected parked notice to hint /continue, got %q", answer)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected engine to hold parked agent state after budget trip")
	}

	stub.mu.Lock()
	reqCount := len(stub.requests)
	stub.mu.Unlock()
	if reqCount != 1 {
		t.Fatalf("expected exactly 1 provider request before budget trip, got %d", reqCount)
	}

	events := collectRecentEvents(evCh, 64, 150*time.Millisecond)
	ev, ok := findEventByType(events, "agent:loop:budget_exhausted")
	if !ok {
		t.Fatalf("expected agent:loop:budget_exhausted event, got %v", eventTypes(events))
	}
	payload, _ := ev.Payload.(map[string]any)
	if got, _ := payload["max_tool_tokens"].(int); got != 25 {
		t.Fatalf("expected max_tool_tokens=25 in payload, got %v", payload["max_tool_tokens"])
	}
	if got, _ := payload["tokens_used"].(int); got < 25 {
		t.Fatalf("expected tokens_used >= 25, got %v", payload["tokens_used"])
	}

	parkedEv, ok := findEventByType(events, "agent:loop:parked")
	if !ok {
		t.Fatalf("expected agent:loop:parked event alongside budget_exhausted, got %v", eventTypes(events))
	}
	parkedPayload, _ := parkedEv.Payload.(map[string]any)
	if reason, _ := parkedPayload["reason"].(string); reason != "budget_exhausted" {
		t.Fatalf("expected parked reason=budget_exhausted, got %v", parkedPayload["reason"])
	}
}

func containsMetaTool(descs []provider.ToolDescriptor, name string) bool {
	for _, d := range descs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func metaToolNamesFromDescs(descs []provider.ToolDescriptor) []string {
	out := make([]string, 0, len(descs))
	for _, d := range descs {
		out = append(out, d.Name)
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func collectRecentEvents(ch <-chan Event, max int, wait time.Duration) []Event {
	if ch == nil || max <= 0 {
		return nil
	}
	out := make([]Event, 0, max)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for len(out) < max {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-timer.C:
			return out
		}
	}
	return out
}

func containsEventType(events []Event, want string) bool {
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" {
		return false
	}
	for _, ev := range events {
		if strings.TrimSpace(strings.ToLower(ev.Type)) == want {
			return true
		}
	}
	return false
}

func eventTypes(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if t := strings.TrimSpace(ev.Type); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func findEventByType(events []Event, want string) (Event, bool) {
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" {
		return Event{}, false
	}
	for _, ev := range events {
		if strings.TrimSpace(strings.ToLower(ev.Type)) == want {
			return ev, true
		}
	}
	return Event{}, false
}
