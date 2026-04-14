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

func TestAnthropicProviderComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got == "" {
			t.Fatal("missing x-api-key header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "model":"claude-sonnet-4-6",
  "usage":{"input_tokens":10,"output_tokens":6},
  "content":[{"type":"text","text":"hello from anthropic"}]
}`))
	}))
	defer srv.Close()

	p := NewAnthropicProvider("claude-sonnet-4-6", "test-key", srv.URL)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		System: "You are helpful.",
		Messages: []Message{
			{Role: types.RoleUser, Content: "say hello"},
		},
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if resp.Text != "hello from anthropic" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 16 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestAnthropicProviderStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `event: content_block_delta`)
		_, _ = fmt.Fprintln(w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}`)
		_, _ = fmt.Fprintln(w, ``)
		_, _ = fmt.Fprintln(w, `event: content_block_delta`)
		_, _ = fmt.Fprintln(w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"anthropic"}}`)
		_, _ = fmt.Fprintln(w, ``)
		_, _ = fmt.Fprintln(w, `event: message_stop`)
		_, _ = fmt.Fprintln(w, `data: {"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropicProvider("claude-sonnet-4-6", "test-key", srv.URL)
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
	if got := out.String(); got != "hello anthropic" {
		t.Fatalf("unexpected stream text: %q", got)
	}
}
