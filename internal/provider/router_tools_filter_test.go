package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// toolCapableProvider mirrors fakeRaceProvider but lets the test dictate
// whether Hints.SupportsTools is true. A canned completion or a canned
// error is returned from Complete so the cascade can be observed.
type toolCapableProvider struct {
	name          string
	text          string
	err           error
	supportsTools bool
}

func (p *toolCapableProvider) Name() string  { return p.name }
func (p *toolCapableProvider) Model() string { return p.name + "-model" }
func (p *toolCapableProvider) Models() []string { return []string{p.name + "-model"} }
func (p *toolCapableProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &CompletionResponse{Text: p.text, Model: p.Model()}, nil
}
func (p *toolCapableProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	resp, err := p.Complete(ctx, req)
	if err != nil {
		ch <- StreamEvent{Type: StreamError, Err: err}
		close(ch)
		return ch, nil
	}
	ch <- StreamEvent{Type: StreamDone, Model: resp.Model}
	close(ch)
	return ch, nil
}
func (p *toolCapableProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *toolCapableProvider) MaxContext() int             { return 100_000 }
func (p *toolCapableProvider) Hints() ProviderHints {
	return ProviderHints{MaxContext: 100_000, SupportsTools: p.supportsTools}
}

// TestCompleteSkipsNonToolProviderOnFallback locks in the fix for the
// /continue-to-offline bug: when the caller asks for tools and the primary
// errors, the router must NOT fall through to a provider that can't honour
// tools (offline returns a canned analyzer reply with zero tool_calls,
// which the agent loop then treats as the final answer).
func TestCompleteSkipsNonToolProviderOnFallback(t *testing.T) {
	primary := &toolCapableProvider{name: "zai", err: errors.New("upstream 429"), supportsTools: true}
	offline := &toolCapableProvider{name: "offline", text: "canned offline", supportsTools: false}
	r := newRouterWith(primary, offline)
	r.primary = "zai"

	req := CompletionRequest{
		Provider: "zai",
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
		Tools:    []ToolDescriptor{{Name: "read_file"}},
	}
	_, used, err := r.Complete(context.Background(), req)
	if err == nil {
		t.Fatalf("want error when primary fails and only fallback is offline, got success used=%s", used)
	}
	if !strings.Contains(err.Error(), "upstream 429") {
		t.Fatalf("error should surface zai failure, got %v", err)
	}
}

// TestCompleteAllowsToolCapableFallback verifies the filter doesn't strip
// legitimate tool-capable fallback providers — only those that lack tool
// support (offline, placeholders).
func TestCompleteAllowsToolCapableFallback(t *testing.T) {
	primary := &toolCapableProvider{name: "anthropic", err: errors.New("upstream 500"), supportsTools: true}
	fallback := &toolCapableProvider{name: "openai", text: "tool-capable fallback win", supportsTools: true}
	offline := &toolCapableProvider{name: "offline", text: "canned offline", supportsTools: false}
	r := newRouterWith(primary, fallback, offline)
	r.primary = "anthropic"
	r.fallback = []string{"openai"}

	req := CompletionRequest{
		Provider: "anthropic",
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
		Tools:    []ToolDescriptor{{Name: "read_file"}},
	}
	resp, used, err := r.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("tool-capable fallback should win, got err=%v", err)
	}
	if used != "openai" {
		t.Fatalf("used=%q, want openai", used)
	}
	if resp.Text != "tool-capable fallback win" {
		t.Fatalf("wrong response body: %q", resp.Text)
	}
}

// TestCompleteKeepsExplicitNonToolProvider confirms `--provider offline`
// still works for users who actively opt in. The filter only closes off the
// silent-fallback path.
func TestCompleteKeepsExplicitNonToolProvider(t *testing.T) {
	offline := &toolCapableProvider{name: "offline", text: "canned offline", supportsTools: false}
	r := newRouterWith(offline)
	r.primary = "offline"

	req := CompletionRequest{
		Provider: "offline",
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
		Tools:    []ToolDescriptor{{Name: "read_file"}},
	}
	resp, used, err := r.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("explicit offline request must still resolve, got err=%v", err)
	}
	if used != "offline" {
		t.Fatalf("used=%q, want offline", used)
	}
	if resp.Text != "canned offline" {
		t.Fatalf("wrong response body: %q", resp.Text)
	}
}

// TestCompleteNoToolsUsesFullCascade sanity-checks that the filter is a
// no-op when the request carries no tools — regular text-only calls should
// still enjoy offline as the always-available backstop.
func TestCompleteNoToolsUsesFullCascade(t *testing.T) {
	primary := &toolCapableProvider{name: "zai", err: errors.New("upstream 429"), supportsTools: true}
	offline := &toolCapableProvider{name: "offline", text: "canned offline", supportsTools: false}
	r := newRouterWith(primary, offline)
	r.primary = "zai"

	req := CompletionRequest{
		Provider: "zai",
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
		// No Tools — full cascade including offline should be in play.
	}
	resp, used, err := r.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("text-only call should fall through to offline, got err=%v", err)
	}
	if used != "offline" {
		t.Fatalf("used=%q, want offline (full cascade)", used)
	}
	if resp.Text != "canned offline" {
		t.Fatalf("wrong response body: %q", resp.Text)
	}
}

func TestFilterToolCapableDirect(t *testing.T) {
	r := newRouterWith(
		&toolCapableProvider{name: "zai", supportsTools: true},
		&toolCapableProvider{name: "openai", supportsTools: true},
		&toolCapableProvider{name: "offline", supportsTools: false},
	)
	in := []string{"zai", "openai", "offline"}

	// Without an explicit request, offline is filtered out.
	got := r.filterToolCapable(in, "")
	want := []string{"zai", "openai"}
	if !equalStrings(got, want) {
		t.Fatalf("filter without request: got %v, want %v", got, want)
	}

	// Explicit request preserves the named provider even when non-capable.
	got = r.filterToolCapable(in, "offline")
	want = []string{"zai", "openai", "offline"}
	if !equalStrings(got, want) {
		t.Fatalf("filter with explicit offline: got %v, want %v", got, want)
	}

	// Unknown providers are passed through unchanged so the caller still
	// sees the existing ErrProviderNotFound from the main loop.
	in2 := []string{"zai", "ghost", "offline"}
	got = r.filterToolCapable(in2, "")
	want = []string{"zai", "ghost"}
	if !equalStrings(got, want) {
		t.Fatalf("filter with unknown name: got %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
