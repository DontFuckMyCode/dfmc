package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestOpenAICompatibleProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
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

	p := NewOpenAICompatibleProvider("openai", "gpt-5.4", "test-key", srv.URL)
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

	p := NewOpenAICompatibleProvider("openai", "gpt-5.4", "test-key", srv.URL)
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
