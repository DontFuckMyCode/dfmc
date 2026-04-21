package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

type failingProvider struct {
	name     string
	model    string
	hints    provider.ProviderHints
	requests []provider.CompletionRequest
	err      error
}

func (p *failingProvider) Name() string                { return p.name }
func (p *failingProvider) Model() string               { return p.model }
func (p *failingProvider) Models() []string            { return []string{p.model} }
func (p *failingProvider) CountTokens(text string) int { return len(text) }
func (p *failingProvider) MaxContext() int             { return 128000 }
func (p *failingProvider) Hints() provider.ProviderHints {
	return p.hints
}
func (p *failingProvider) Complete(_ context.Context, req provider.CompletionRequest) (*provider.CompletionResponse, error) {
	p.requests = append(p.requests, req)
	if p.err == nil {
		p.err = fmt.Errorf("simulated failure")
	}
	return nil, p.err
}
func (p *failingProvider) Stream(_ context.Context, _ provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	return nil, fmt.Errorf("unexpected stream")
}

func TestRunSubagent_ModelOverrideUsesSelectedProfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{Model: "stub-model", MaxContext: 128000}
	cfg.Providers.Profiles["alt"] = config.ModelConfig{Model: "alt-model", MaxContext: 128000}

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{name: "stub", model: "stub-model", hints: newNativeHints()}
	alt := &scriptedProvider{
		name:      "alt",
		model:     "alt-model",
		hints:     newNativeHints(),
		responses: []scriptedResponse{{Text: "override worked"}},
	}
	router.Register(stub)
	router.Register(alt)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: t.TempDir(),
		Providers:   router,
		Tools:       tools.New(*cfg),
	}

	res, err := eng.RunSubagent(context.Background(), tools.SubagentRequest{
		Task:  "inspect the renderer",
		Role:  "researcher",
		Model: "alt",
	})
	if err != nil {
		t.Fatalf("RunSubagent: %v", err)
	}
	if got, _ := res.Data["provider"].(string); got != "alt" {
		t.Fatalf("expected final provider alt, got %q", got)
	}
	if len(alt.requests) != 1 {
		t.Fatalf("expected alt provider to receive 1 request, got %d", len(alt.requests))
	}
	if len(stub.requests) != 0 {
		t.Fatalf("primary stub provider should not have been used, got %d request(s)", len(stub.requests))
	}
}

func TestRunSubagentProfiles_FallbackPreservesSeedContext(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "fail"
	cfg.Providers.Profiles["fail"] = config.ModelConfig{Model: "fail-model", MaxContext: 128000}
	cfg.Providers.Profiles["ok"] = config.ModelConfig{Model: "ok-model", MaxContext: 128000}

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	fail := &failingProvider{
		name:  "fail",
		model: "fail-model",
		hints: newNativeHints(),
		err:   fmt.Errorf("provider exploded"),
	}
	ok := &scriptedProvider{
		name:      "ok",
		model:     "ok-model",
		hints:     newNativeHints(),
		responses: []scriptedResponse{{Text: "fallback completed"}},
	}
	router.Register(fail)
	router.Register(ok)

	bus := NewEventBus()
	evCh := bus.Subscribe("*")
	defer bus.Unsubscribe("*", evCh)

	eng := &Engine{
		Config:      cfg,
		EventBus:    bus,
		ProjectRoot: t.TempDir(),
		Providers:   router,
		Tools:       tools.New(*cfg),
	}

	res, err := eng.runSubagentProfiles(context.Background(), tools.SubagentRequest{
		Task: "inspect internal/engine and summarize the risks",
		Role: "researcher",
	}, []string{"fail", "ok"})
	if err != nil {
		t.Fatalf("runSubagentProfiles: %v", err)
	}
	if len(fail.requests) != 1 || len(ok.requests) != 1 {
		t.Fatalf("expected one request per provider, got fail=%d ok=%d", len(fail.requests), len(ok.requests))
	}
	if !reflect.DeepEqual(fail.requests[0].Messages, ok.requests[0].Messages) {
		t.Fatalf("fallback attempt should preserve the exact subagent messages seed")
	}
	if fail.requests[0].System != ok.requests[0].System {
		t.Fatalf("fallback attempt should preserve system prompt")
	}
	if !reflect.DeepEqual(fail.requests[0].Context, ok.requests[0].Context) {
		t.Fatalf("fallback attempt should preserve context chunks")
	}
	if got, _ := res.Data["fallback_used"].(bool); !got {
		t.Fatalf("expected fallback_used=true, got %+v", res.Data)
	}
	if got, _ := res.Data["attempts"].(int); got != 2 {
		t.Fatalf("expected attempts=2, got %+v", res.Data)
	}
	if got, _ := res.Data["provider"].(string); got != "ok" {
		t.Fatalf("expected final provider ok, got %+v", res.Data)
	}
	if got, _ := res.Data["profiles_tried"].([]string); !reflect.DeepEqual(got, []string{"fail", "ok"}) {
		t.Fatalf("expected tried chain [fail ok], got %+v", res.Data["profiles_tried"])
	}
	gotReasons, _ := res.Data["fallback_reasons"].([]string)
	if len(gotReasons) != 1 || !strings.Contains(gotReasons[0], "provider exploded") {
		t.Fatalf("expected fallback reason containing provider exploded, got %+v", res.Data["fallback_reasons"])
	}

	events := collectRecentEvents(evCh, 32, 150*time.Millisecond)
	if !containsEventType(events, "agent:subagent:fallback") {
		t.Fatalf("expected fallback event, got %v", eventTypes(events))
	}
	var ev Event
	found := false
	for _, candidate := range events {
		if candidate.Type == "agent:subagent:fallback" {
			ev = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected fallback event payload, got %v", eventTypes(events))
	}
	payload, _ := ev.Payload.(map[string]any)
	gotEventReasons, _ := payload["fallback_reasons"].([]string)
	if len(gotEventReasons) != 1 || !strings.Contains(gotEventReasons[0], "provider exploded") {
		t.Fatalf("expected fallback event reasons, got %+v", payload["fallback_reasons"])
	}
}

func TestRunSubagentProfiles_PropagatesDriveToolSource(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 4, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c1")}},
		{Text: "done"},
	})
	eng.Config.Tools.RequireApproval = []string{"tool_call"}

	var seen []string
	eng.SetApprover(ApproverFunc(func(_ context.Context, req ApprovalRequest) ApprovalDecision {
		seen = append(seen, req.Source)
		if req.Source != "drive" {
			return ApprovalDecision{Approved: false, Reason: "wrong source"}
		}
		return ApprovalDecision{Approved: true, Reason: "drive scoped"}
	}))

	res, err := eng.runSubagentProfiles(context.Background(), tools.SubagentRequest{
		Task:       "read note.txt and summarize it",
		Role:       "researcher",
		ToolSource: "drive",
	}, []string{"stub"})
	if err != nil {
		t.Fatalf("runSubagentProfiles: %v", err)
	}
	if strings.TrimSpace(res.Summary) != "done" {
		t.Fatalf("expected successful completion, got %+v", res)
	}
	if len(seen) == 0 || seen[0] != "drive" {
		t.Fatalf("expected approval request source drive, got %v", seen)
	}
}

func TestRunSubagentProfiles_DefaultToolSourceRemainsAgent(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 4, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c1")}},
		{Text: "done"},
	})
	eng.Config.Tools.RequireApproval = []string{"tool_call"}

	var seen []string
	eng.SetApprover(ApproverFunc(func(_ context.Context, req ApprovalRequest) ApprovalDecision {
		seen = append(seen, req.Source)
		return ApprovalDecision{Approved: true, Reason: "ok"}
	}))

	if _, err := eng.runSubagentProfiles(context.Background(), tools.SubagentRequest{
		Task: "read note.txt and summarize it",
		Role: "researcher",
	}, []string{"stub"}); err != nil {
		t.Fatalf("runSubagentProfiles: %v", err)
	}
	if len(seen) == 0 || seen[0] != "agent" {
		t.Fatalf("expected default approval source agent, got %v", seen)
	}
}
