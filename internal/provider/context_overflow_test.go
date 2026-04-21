package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// flakyProvider fails the first N Complete calls with the configured error,
// then succeeds. Used to verify the router's compact-and-retry behavior.
type flakyProvider struct {
	name             string
	failErr          error
	failsLeft        int
	seenCalls        int
	lastCompleteMsgs []Message
}

func (p *flakyProvider) Name() string  { return p.name }
func (p *flakyProvider) Model() string { return "test-model" }
func (p *flakyProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	p.seenCalls++
	p.lastCompleteMsgs = append([]Message(nil), req.Messages...)
	if p.failsLeft > 0 {
		p.failsLeft--
		return nil, p.failErr
	}
	return &CompletionResponse{Text: "ok", Model: "test-model"}, nil
}
func (p *flakyProvider) Stream(_ context.Context, _ CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamDone}
	close(ch)
	return ch, nil
}
func (p *flakyProvider) CountTokens(s string) int { return len(s) }
func (p *flakyProvider) MaxContext() int          { return 100000 }
func (p *flakyProvider) Hints() ProviderHints     { return ProviderHints{} }

func TestIsContextOverflowDetectsCommonPhrases(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("connection reset"), false},
		{"explicit sentinel", fmt.Errorf("%w: prompt too big", ErrContextOverflow), true},
		{"openai phrase", errors.New("This model's maximum context length is 128000 tokens"), true},
		{"anthropic phrase", errors.New("prompt is too long: 210000 tokens > 200000 limit"), true},
		{"lowercase pattern", errors.New("context_length_exceeded: prompt exceeds model max context"), true},
		{"input is too long", errors.New("input is too long for this model"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isContextOverflow(tc.err); got != tc.want {
				t.Fatalf("isContextOverflow(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCompactMessagesForRetryKeepsTailAndAddsNotice(t *testing.T) {
	msgs := []Message{
		{Role: types.RoleUser, Content: "turn 1"},
		{Role: types.RoleAssistant, Content: "reply 1"},
		{Role: types.RoleUser, Content: "turn 2"},
		{Role: types.RoleAssistant, Content: "reply 2"},
		{Role: types.RoleUser, Content: "final turn"},
	}
	compacted, trimmed := compactMessagesForRetry(msgs)
	if trimmed != 4 {
		t.Fatalf("expected 4 trimmed, got %d", trimmed)
	}
	if len(compacted) != 2 {
		t.Fatalf("expected 2 messages (notice + tail), got %d", len(compacted))
	}
	if string(compacted[0].Role) != "user" {
		t.Fatalf("notice role should be user, got %q", compacted[0].Role)
	}
	if compacted[len(compacted)-1].Content != "final turn" {
		t.Fatalf("tail lost: %+v", compacted)
	}
}

func TestCompactMessagesForRetrySkipsShortHistories(t *testing.T) {
	// A 2-message history has nothing to trim.
	msgs := []Message{
		{Role: types.RoleUser, Content: "hello"},
		{Role: types.RoleAssistant, Content: "hi"},
	}
	_, trimmed := compactMessagesForRetry(msgs)
	if trimmed != 0 {
		t.Fatalf("expected 0 trimmed on short history, got %d", trimmed)
	}
}

func TestRouterCompactsAndRetriesOnContextOverflow(t *testing.T) {
	router := &Router{
		primary:   "flaky",
		providers: map[string]Provider{},
	}
	flaky := &flakyProvider{
		name:      "flaky",
		failErr:   fmt.Errorf("%w: prompt too long", ErrContextOverflow),
		failsLeft: 1, // fail once, succeed on retry
	}
	router.Register(flaky)
	router.Register(NewOfflineProvider())

	// Long-enough history to trigger actual compaction.
	msgs := []Message{
		{Role: types.RoleUser, Content: "first"},
		{Role: types.RoleAssistant, Content: "first reply"},
		{Role: types.RoleUser, Content: "second"},
		{Role: types.RoleAssistant, Content: "second reply"},
		{Role: types.RoleUser, Content: "final ask"},
	}
	resp, name, err := router.Complete(context.Background(), CompletionRequest{
		Provider: "flaky",
		Messages: msgs,
	})
	if err != nil {
		t.Fatalf("router should have retried successfully: %v", err)
	}
	if name != "flaky" {
		t.Fatalf("retry should hit the same provider, got %s", name)
	}
	if resp == nil || resp.Text != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if flaky.seenCalls != 2 {
		t.Fatalf("expected exactly 2 calls (fail + retry), got %d", flaky.seenCalls)
	}
	// The retry should have fewer messages than the original.
	if len(flaky.lastCompleteMsgs) >= len(msgs) {
		t.Fatalf("retry did not compact messages; still %d", len(flaky.lastCompleteMsgs))
	}
}
