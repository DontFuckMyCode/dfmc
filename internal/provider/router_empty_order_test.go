package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestCompleteReportsEmptyOrderInsteadOfNilNilNil pins the fix for a
// silent-nil bug in Complete: when filterToolCapable strips every
// provider out (no tool-capable primary, no tool-capable fallbacks,
// and offline - which is always appended - doesn't support tools),
// the old code ran a zero-iteration loop over `order` and returned
// (nil, "", errors.Join(empty)) == (nil, "", nil). Any caller then
// hit a nil-pointer deref on resp.Content / resp.Usage because the
// "no error" contract was violated.
//
// The fix is an explicit empty-order guard that names the cause so
// the operator knows what broke (no tool-capable provider available),
// rather than a mystery nil that shows up as a SIGSEGV in the agent
// loop three frames up.
func TestCompleteReportsEmptyOrderInsteadOfNilNilNil(t *testing.T) {
	// Only offline is registered. Offline's Hints.SupportsTools is
	// false, so filterToolCapable strips it out when req.Tools is
	// non-empty. No primary is set (zero value), so there's nothing
	// to preserve via the `name == req` exception either.
	offline := &toolCapableProvider{name: "offline", text: "canned", supportsTools: false}
	r := newRouterWith(offline)
	// Deliberately leave r.primary = "" to exercise the empty-request path.

	req := CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
		Tools:    []ToolDescriptor{{Name: "read_file"}},
	}
	resp, used, err := r.Complete(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error when no tool-capable provider is available, got resp=%v used=%q err=nil", resp, used)
	}
	if resp != nil {
		t.Fatalf("expected nil response on error, got %+v", resp)
	}
	// The error must name the missing-capability cause so the operator
	// can fix their config; a bare ErrProviderNotFound would be wrong
	// because all registered providers were FOUND, they were just
	// filtered for lacking SupportsTools.
	if !errors.Is(err, ErrNoCapableProvider) {
		t.Fatalf("expected ErrNoCapableProvider, got %v", err)
	}
}

// TestStreamReportsEmptyOrderInsteadOfNilNilNil is the streaming twin:
// Stream shared the same zero-iteration fallthrough as Complete. The
// SSE handler in ui/web feeds a nil stream-channel pointer back to the
// client if the router returns (nil, "", nil), which blocks forever.
// Make Stream surface the same named sentinel.
func TestStreamReportsEmptyOrderInsteadOfNilNilNil(t *testing.T) {
	offline := &toolCapableProvider{name: "offline", text: "canned", supportsTools: false}
	r := newRouterWith(offline)

	req := CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
		Tools:    []ToolDescriptor{{Name: "read_file"}},
	}
	stream, used, err := r.Stream(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error, got stream=%v used=%q err=nil", stream, used)
	}
	if stream != nil {
		t.Fatalf("expected nil stream on error, got %v", stream)
	}
	if !errors.Is(err, ErrNoCapableProvider) {
		t.Fatalf("expected ErrNoCapableProvider, got %v", err)
	}
}
