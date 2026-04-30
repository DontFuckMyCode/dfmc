package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestProvidersWrapTransient5xxInStatusError pins the structured-error
// contract for every first-party provider: anthropic, openai-compatible,
// and google must wrap non-throttle 5xx responses in *StatusError so the
// router's isTransient classifier can branch via errors.As. Without
// this test, a future regression to fmt.Errorf("status %d: %s", ...)
// would still satisfy isTransient (via the substring fallback) and
// compile cleanly — silently dropping the structured contract. 503 is
// covered separately because isThrottleStatus routes it through
// ThrottledError; the non-throttle 5xx branch is what we lock in here.
func TestProvidersWrapTransient5xxInStatusError(t *testing.T) {
	cases := []struct {
		name    string
		mkProv  func(url string) Provider
		wantErr int
	}{
		{
			name: "anthropic",
			mkProv: func(url string) Provider {
				return NewAnthropicProvider("claude-test", "k", url, 64000, 1000000)
			},
			wantErr: http.StatusInternalServerError,
		},
		{
			name: "openai-compatible",
			mkProv: func(url string) Provider {
				return NewOpenAICompatibleProvider("openai-compat", "test-model", "k", url, 64000, 1000000, 0)
			},
			wantErr: http.StatusBadGateway,
		},
		{
			name: "google",
			mkProv: func(url string) Provider {
				return NewGoogleProvider("gemini-test", "k", url, 64000, 1000000, 5*time.Second)
			},
			// 502 instead of 503 — isThrottleStatus maps 503 onto the
			// ThrottledError path, so it's never a *StatusError.
			wantErr: http.StatusBadGateway,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.wantErr)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream blip"}}`))
			}))
			defer srv.Close()

			p := tc.mkProv(srv.URL)
			_, err := p.Complete(context.Background(), CompletionRequest{
				Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
			})
			if err == nil {
				t.Fatalf("%s: expected error from %d response, got nil", tc.name, tc.wantErr)
			}
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("%s: expected *StatusError via errors.As, got %T: %v", tc.name, err, err)
			}
			if se.StatusCode != tc.wantErr {
				t.Fatalf("%s: StatusCode: got %d, want %d", tc.name, se.StatusCode, tc.wantErr)
			}
			if !se.IsTransient() {
				t.Fatalf("%s: %d should classify as transient", tc.name, tc.wantErr)
			}
			if !isTransient(err) {
				t.Fatalf("%s: router-level isTransient must agree with StatusError.IsTransient", tc.name)
			}
		})
	}
}

// TestProvidersWrap401InStatusErrorNotTransient covers the inverse:
// auth failures must surface as *StatusError but classify as NOT
// transient so the model fallback chain doesn't burn through every
// configured fallback model on the same misconfigured key.
func TestProvidersWrap401InStatusErrorNotTransient(t *testing.T) {
	cases := []struct {
		name   string
		mkProv func(url string) Provider
	}{
		{"anthropic", func(url string) Provider {
			return NewAnthropicProvider("claude-test", "k", url, 64000, 1000000)
		}},
		{"openai-compatible", func(url string) Provider {
			return NewOpenAICompatibleProvider("openai-compat", "test-model", "k", url, 64000, 1000000, 0)
		}},
		{"google", func(url string) Provider {
			return NewGoogleProvider("gemini-test", "k", url, 64000, 1000000, 5*time.Second)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			}))
			defer srv.Close()

			p := tc.mkProv(srv.URL)
			_, err := p.Complete(context.Background(), CompletionRequest{
				Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
			})
			if err == nil {
				t.Fatalf("%s: expected error from 401 response", tc.name)
			}
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("%s: expected *StatusError, got %T: %v", tc.name, err, err)
			}
			if se.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s: StatusCode: got %d, want 401", tc.name, se.StatusCode)
			}
			if se.IsTransient() {
				t.Fatalf("%s: 401 must not be transient", tc.name)
			}
			if isTransient(err) {
				t.Fatalf("%s: router-level isTransient must agree", tc.name)
			}
		})
	}
}
