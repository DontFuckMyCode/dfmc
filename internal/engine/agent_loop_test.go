package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type scriptedProvider struct {
	name       string
	model      string
	hints      provider.ProviderHints
	mu         sync.Mutex
	responses  []string
	requests   []provider.CompletionRequest
	streamUsed bool
}

func (p *scriptedProvider) Name() string                  { return p.name }
func (p *scriptedProvider) Model() string                 { return p.model }
func (p *scriptedProvider) CountTokens(text string) int   { return len(strings.Fields(text)) }
func (p *scriptedProvider) MaxContext() int               { return 128000 }
func (p *scriptedProvider) Hints() provider.ProviderHints { return p.hints }

func (p *scriptedProvider) Complete(_ context.Context, req provider.CompletionRequest) (*provider.CompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	text := p.responses[0]
	p.responses = p.responses[1:]
	return &provider.CompletionResponse{
		Text:  text,
		Model: p.model,
		Usage: provider.Usage{InputTokens: 12, OutputTokens: 18, TotalTokens: 30},
	}, nil
}

func (p *scriptedProvider) Stream(_ context.Context, _ provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.streamUsed = true
	p.mu.Unlock()
	return nil, fmt.Errorf("unexpected stream call")
}

func TestAskWithMetadata_ExecutesLocalToolLoop(t *testing.T) {
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
		hints: provider.ProviderHints{
			ToolStyle:   "function-calling",
			MaxContext:  128000,
			DefaultMode: "balanced",
		},
		responses: []string{
			"```dfmc-tool\n{\"tool\":\"read_file\",\"params\":{\"path\":\"note.txt\",\"line_start\":1,\"line_end\":1}}\n```",
			"The first line in note.txt is alpha.",
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

	answer, err := eng.AskWithMetadata(context.Background(), "note.txt dosyasının ilk satırı ne?")
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
	second := stub.requests[1]
	var joined []string
	for _, msg := range second.Messages {
		joined = append(joined, msg.Content)
	}
	combined := strings.Join(joined, "\n---\n")
	if !strings.Contains(combined, "[DFMC tool result]") {
		t.Fatalf("expected tool result prompt in second request, got %q", combined)
	}
	if !strings.Contains(combined, "alpha") {
		t.Fatalf("expected second request to include tool output, got %q", combined)
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
	if len(assistant.Results) != 1 {
		t.Fatalf("expected one recorded tool result, got %#v", assistant.Results)
	}
	if !assistant.Results[0].Success {
		t.Fatalf("expected successful tool result, got %#v", assistant.Results[0])
	}
}

func TestAskWithMetadata_LocalToolLoopParsesPreambleToolBlock(t *testing.T) {
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
		hints: provider.ProviderHints{
			ToolStyle:   "function-calling",
			MaxContext:  128000,
			DefaultMode: "balanced",
		},
		responses: []string{
			"I will inspect the file now.\n```dfmc-tool\n{\"tool\":\"read_file\",\"params\":{\"path\":\"note.txt\",\"line_start\":1,\"line_end\":1}}\n```\nThen I will continue.",
			"The first line in note.txt is alpha.",
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

	answer, err := eng.AskWithMetadata(context.Background(), "note.txt first line?")
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
}

func TestParseLocalToolCall_AllowsMixedText(t *testing.T) {
	raw := "Let me inspect first.\n```dfmc-tool\n{\"tool\":\"read_file\",\"params\":{\"path\":\"README.md\"}}\n```\nContinuing..."
	call, ok := parseLocalToolCall(raw)
	if !ok {
		t.Fatal("expected parser to detect tool call inside mixed response")
	}
	if call.Tool != "read_file" {
		t.Fatalf("expected read_file tool, got %q", call.Tool)
	}
	if got := fmt.Sprint(call.Params["path"]); got != "README.md" {
		t.Fatalf("expected path README.md, got %q", got)
	}
}

func TestBuildLocalToolBridgeInstructionsIncludesContextSnapshot(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "internal/engine/engine.go", LineStart: 10, LineEnd: 80, TokenCount: 220},
		{Path: "internal/tools/engine.go", LineStart: 1, LineEnd: 60, TokenCount: 140},
	}
	text := buildLocalToolBridgeInstructions(
		[]string{"read_file", "grep_codebase"},
		ctxmgr.PromptRuntime{ToolStyle: "function-calling"},
		chunks,
	)
	if !strings.Contains(text, "Pre-tool instruction (context enter)") {
		t.Fatalf("expected pre-tool contract in bridge instructions, got:\n%s", text)
	}
	if !strings.Contains(text, "Post-tool instruction (context exit)") {
		t.Fatalf("expected post-tool contract in bridge instructions, got:\n%s", text)
	}
	if !strings.Contains(text, "Context snapshot:") || !strings.Contains(text, "files=2 tokens=360") {
		t.Fatalf("expected context snapshot summary in bridge instructions, got:\n%s", text)
	}
}

func TestFormatLocalToolResultPromptIncludesPostToolContract(t *testing.T) {
	trace := localToolTrace{
		Call: localToolCall{
			Tool: "read_file",
			Params: map[string]any{
				"path":       "README.md",
				"line_start": 1,
				"line_end":   10,
			},
		},
		Result: tools.Result{
			Success:    true,
			Output:     "line1\nline2",
			DurationMs: 12,
		},
		Step: 1,
	}
	text := formatLocalToolResultPrompt(trace)
	if !strings.Contains(text, "[DFMC post-tool context contract]") {
		t.Fatalf("expected post-tool contract heading, got:\n%s", text)
	}
	if !strings.Contains(text, "Enter context:") || !strings.Contains(text, "Exit context:") {
		t.Fatalf("expected enter/exit context instructions, got:\n%s", text)
	}
	if !strings.Contains(text, "exactly ONE dfmc-tool block OR the final answer") {
		t.Fatalf("expected next action instruction, got:\n%s", text)
	}
}

func TestAskWithMetadata_PublishesLoopEvents(t *testing.T) {
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
		hints: provider.ProviderHints{
			ToolStyle:   "function-calling",
			MaxContext:  128000,
			DefaultMode: "balanced",
		},
		responses: []string{
			"```dfmc-tool\n{\"tool\":\"read_file\",\"params\":{\"path\":\"note.txt\",\"line_start\":1,\"line_end\":1}}\n```",
			"alpha first line",
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
	if !containsEventType(events, "agent:loop:start") {
		t.Fatalf("expected agent:loop:start event, got %#v", eventTypes(events))
	}
	if !containsEventType(events, "agent:loop:contract") {
		t.Fatalf("expected agent:loop:contract event, got %#v", eventTypes(events))
	}
	if !containsEventType(events, "agent:loop:thinking") {
		t.Fatalf("expected agent:loop:thinking event, got %#v", eventTypes(events))
	}
	if !containsEventType(events, "agent:loop:context_enter") {
		t.Fatalf("expected agent:loop:context_enter event, got %#v", eventTypes(events))
	}
	if !containsEventType(events, "tool:call") || !containsEventType(events, "tool:result") {
		t.Fatalf("expected tool call/result events, got %#v", eventTypes(events))
	}
	if !containsEventType(events, "agent:loop:context_exit") {
		t.Fatalf("expected agent:loop:context_exit event, got %#v", eventTypes(events))
	}
	if !containsEventType(events, "agent:loop:final") {
		t.Fatalf("expected agent:loop:final event, got %#v", eventTypes(events))
	}

	contract, ok := findEventByType(events, "agent:loop:contract")
	if !ok {
		t.Fatalf("expected contract event in feed, got %#v", eventTypes(events))
	}
	payload, _ := contract.Payload.(map[string]any)
	if !strings.Contains(fmt.Sprint(payload["pre_tool"]), "context enter") {
		t.Fatalf("expected contract payload pre_tool instruction, got %#v", payload)
	}
	if !strings.Contains(fmt.Sprint(payload["post_tool"]), "context exit") {
		t.Fatalf("expected contract payload post_tool instruction, got %#v", payload)
	}
}

func TestStreamAsk_UsesLocalToolLoopForToolCapableProvider(t *testing.T) {
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
		hints: provider.ProviderHints{
			ToolStyle:   "function-calling",
			MaxContext:  128000,
			DefaultMode: "balanced",
		},
		responses: []string{
			"```dfmc-tool\n{\"tool\":\"read_file\",\"params\":{\"path\":\"main.go\",\"line_start\":1,\"line_end\":1}}\n```",
			"Dosya package main ile başlıyor.",
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

	stream, err := eng.StreamAsk(context.Background(), "main.go dosyası neyle başlıyor?")
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
		t.Fatal("expected StreamAsk local tool path to avoid provider Stream")
	}
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
