package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestOpenAICompatibleProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got == "" {
			t.Fatal("missing authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "model":"gpt-5.4",
  "usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20},
  "choices":[{"message":{"content":"hello from openai"}}]
}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("openai", "gpt-5.4", "test-key", srv.URL+"/v1", 128000, 1050000, 0)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{
			{Role: types.RoleUser, Content: "say hello"},
		},
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if resp.Text != "hello from openai" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 20 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestOpenAICompatibleProviderStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"hello "}}]}`)
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"stream"}}]}`)
		_, _ = fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("openai", "gpt-5.4", "test-key", srv.URL+"/v1", 128000, 1050000, 0)
	stream, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{
			{Role: types.RoleUser, Content: "say hello"},
		},
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	var out strings.Builder
	done := false
	for ev := range stream {
		switch ev.Type {
		case StreamDelta:
			out.WriteString(ev.Delta)
		case StreamDone:
			done = true
		case StreamError:
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
	}
	if !done {
		t.Fatal("expected stream done")
	}
	if got := out.String(); got != "hello stream" {
		t.Fatalf("unexpected stream text: %q", got)
	}
}

func TestOpenAICompatibleProviderStream_ThrottleWraps429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, `{"error":{"code":"1302","message":"Rate limit reached for requests"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("zai", "glm-5.1", "test-key", srv.URL+"/v1", 128000, 1050000, 0)
	_, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "say hello"}},
	})
	if err == nil {
		t.Fatal("expected throttle error")
	}
	if !errors.Is(err, ErrProviderThrottled) {
		t.Fatalf("expected ErrProviderThrottled, got %v", err)
	}
	var te *ThrottledError
	if !errors.As(err, &te) {
		t.Fatalf("expected ThrottledError, got %T", err)
	}
	if te.RetryAfter != time.Second {
		t.Fatalf("expected Retry-After=1s, got %s", te.RetryAfter)
	}
}

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	cases := []struct {
		name, provider, in, want string
	}{
		{"bare deepseek gets /v1", "deepseek", "https://api.deepseek.com", "https://api.deepseek.com/v1"},
		{"deepseek with /v1 untouched", "deepseek", "https://api.deepseek.com/v1", "https://api.deepseek.com/v1"},
		{"trailing slash trimmed", "deepseek", "https://api.deepseek.com/v1/", "https://api.deepseek.com/v1"},
		{"kimi bare host gets /v1", "kimi", "https://api.moonshot.ai", "https://api.moonshot.ai/v1"},
		{"zai compatible path preserved", "zai", "https://api.z.ai/api/paas/v4", "https://api.z.ai/api/paas/v4"},
		{"alibaba compatible-mode preserved", "alibaba", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
		{"unknown host left alone", "custom", "https://my-proxy.internal", "https://my-proxy.internal"},
		{"empty stays empty", "deepseek", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeOpenAIBaseURL(tc.provider, tc.in); got != tc.want {
				t.Fatalf("normalizeOpenAIBaseURL(%q,%q) = %q; want %q", tc.provider, tc.in, got, tc.want)
			}
		})
	}
}
