package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// --- URL normalization for Alibaba/DashScope ---

func TestAlibabaDefaultBaseURL(t *testing.T) {
	got := defaultOpenAIBaseURL("alibaba")
	want := "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	if got != want {
		t.Fatalf("defaultOpenAIBaseURL(alibaba) = %q; want %q", got, want)
	}
}

func TestAlibabaNormalizeBaseURL_PreservesCompatibleMode(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"full dashscope URL preserved",
			"https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
			"https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		},
		{
			"trailing slash trimmed",
			"https://dashscope-intl.aliyuncs.com/compatible-mode/v1/",
			"https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		},
		{
			"bare dashscope host without /v1 stays as-is (not a known bare-host case)",
			"https://dashscope-intl.aliyuncs.com",
			"https://dashscope-intl.aliyuncs.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeOpenAIBaseURL("alibaba", tc.in); got != tc.want {
				t.Fatalf("normalizeOpenAIBaseURL(alibaba,%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAlibabaNewProvider_EmptyBaseURLUsesDefault(t *testing.T) {
	p := NewOpenAICompatibleProvider("alibaba", "qwen3.5-plus", "test-key", "", 65536, 1000000)
	if got := p.baseURL; got != "https://dashscope-intl.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("expected default dashscope URL, got %q", got)
	}
	if got := p.MaxContext(); got != 1000000 {
		t.Fatalf("expected max context 1000000, got %d", got)
	}
}

// --- Router integration: protocol resolution ---

func TestAlibabaNormalizedProtocol(t *testing.T) {
	got := normalizedProtocol("alibaba", "")
	if got != "openai-compatible" {
		t.Fatalf("normalizedProtocol(alibaba, empty) = %q; want openai-compatible", got)
	}
	// Explicit protocol should win
	got = normalizedProtocol("alibaba", "anthropic")
	if got != "anthropic" {
		t.Fatalf("normalizedProtocol(alibaba, anthropic) = %q; want anthropic", got)
	}
}

func TestAlibabaProviderFromProfile_WithAPIKey(t *testing.T) {
	p := providerFromProfile("alibaba", config.ModelConfig{
		Model:      "qwen3.5-plus",
		APIKey:     "sk-test-alibaba",
		BaseURL:    "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		MaxContext: 1000000,
		Protocol:   "openai-compatible",
	})
	oai, ok := p.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("expected OpenAICompatibleProvider for alibaba, got %T", p)
	}
	if got := oai.Name(); got != "alibaba" {
		t.Fatalf("expected provider name alibaba, got %q", got)
	}
	if got := oai.Model(); got != "qwen3.5-plus" {
		t.Fatalf("expected model qwen3.5-plus, got %q", got)
	}
}

func TestAlibabaProviderFromProfile_NoKeyYieldsPlaceholder(t *testing.T) {
	p := providerFromProfile("alibaba", config.ModelConfig{
		Model:      "qwen3.5-plus",
		MaxContext: 1000000,
		Protocol:   "openai-compatible",
	})
	ph, ok := p.(*PlaceholderProvider)
	if !ok {
		t.Fatalf("expected PlaceholderProvider for alibaba without API key, got %T", p)
	}
	if ph.Name() != "alibaba" {
		t.Fatalf("expected placeholder name alibaba, got %q", ph.Name())
	}
}

// --- Live-HTTP Complete with Alibaba-style response ---

func TestAlibabaProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test-alibaba" {
			t.Fatalf("expected Bearer auth, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "model":"qwen3.5-plus",
  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},
  "choices":[{"message":{"content":"Ni hao from Qwen"}}]
}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("alibaba", "qwen3.5-plus", "sk-test-alibaba", srv.URL, 65536, 1000000)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "say hello"}},
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if resp.Text != "Ni hao from Qwen" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

// --- Live-HTTP Stream with Alibaba-style SSE ---

func TestAlibabaProviderStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Qwen "}}]}`)
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"stream"}}]}`)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("alibaba", "qwen3.5-plus", "sk-test-alibaba", srv.URL, 65536, 1000000)
	stream, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "say hello"}},
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	var out strings.Builder
	gotDone := false
	for ev := range stream {
		switch ev.Type {
		case StreamDelta:
			out.WriteString(ev.Delta)
		case StreamDone:
			gotDone = true
		case StreamError:
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
	}
	if !gotDone {
		t.Fatal("expected stream done")
	}
	if got := out.String(); got != "Qwen stream" {
		t.Fatalf("unexpected stream text: %q", got)
	}
}

// --- Alibaba 429 throttle detection ---

func TestAlibabaStream_ThrottleWraps429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		http.Error(w, `{"error":{"code":"RateLimitExceeded","message":"Request was throttled"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("alibaba", "qwen3.5-plus", "sk-test-alibaba", srv.URL, 65536, 1000000)
	_, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "say hello"}},
	})
	if err == nil {
		t.Fatal("expected throttle error")
	}
	if !errors.Is(err, ErrProviderThrottled) {
		t.Fatalf("expected throttled error, got %v", err)
	}
}

// --- Complete with tool use (Qwen supports tools) ---

func TestAlibabaProviderComplete_WithTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "model":"qwen3.5-plus",
  "usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30},
  "choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]}}]
}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("alibaba", "qwen3.5-plus", "sk-test-alibaba", srv.URL, 65536, 1000000)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "read main.go"}},
		Tools: []ToolDescriptor{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("complete with tools failed: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected tool call read_file, got %q", resp.ToolCalls[0].Name)
	}
}

// --- Config: seed profile has correct Alibaba defaults ---

func TestAlibabaSeedProfile(t *testing.T) {
	profiles := config.ModelsDevSeedProfiles()
	alibaba, ok := profiles["alibaba"]
	if !ok {
		t.Fatal("expected alibaba entry in seed profiles")
	}
	if alibaba.Model != "qwen3.5-plus" {
		t.Fatalf("expected model qwen3.5-plus, got %q", alibaba.Model)
	}
	if alibaba.Protocol != "openai-compatible" {
		t.Fatalf("expected protocol openai-compatible, got %q", alibaba.Protocol)
	}
	if alibaba.BaseURL != "https://dashscope-intl.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("expected dashscope base URL, got %q", alibaba.BaseURL)
	}
}
