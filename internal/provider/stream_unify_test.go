package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// collectStream drains a stream channel and returns the events in order.
func collectStream(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var out []StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

// TestAnthropicStream_EmitsStartAndUsage verifies the Anthropic stream
// surfaces a StreamStart event at the top and returns StreamDone with model,
// usage, and stop_reason drawn from the SSE message_start/message_delta
// frames.
func TestAnthropicStream_EmitsStartAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// message_start with model + input token count
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":42,\"output_tokens\":0}}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		// content_block_delta carrying text
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello \"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"world\"}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		// message_delta with stop_reason + final output usage
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := NewAnthropicProvider("claude-sonnet-4-6", "test", srv.URL, 0, 0)
	ch, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "say hi"}},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	events := collectStream(t, ch)
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != StreamStart {
		t.Fatalf("first event should be StreamStart, got %+v", events[0])
	}
	if events[0].Model != "claude-sonnet-4-6" {
		t.Fatalf("StreamStart model mismatch: %+v", events[0])
	}
	if events[0].Provider != "anthropic" {
		t.Fatalf("StreamStart provider mismatch: %+v", events[0])
	}

	// Last event must be StreamDone with populated metadata.
	last := events[len(events)-1]
	if last.Type != StreamDone {
		t.Fatalf("last event should be StreamDone, got %+v", last)
	}
	if last.Model != "claude-sonnet-4-6" {
		t.Fatalf("StreamDone model mismatch: %+v", last)
	}
	if last.StopReason != StopEnd {
		t.Fatalf("StreamDone stop_reason should be end_turn, got %q", last.StopReason)
	}
	if last.Usage == nil {
		t.Fatalf("StreamDone usage should be populated, got nil")
	}
	if last.Usage.InputTokens != 42 || last.Usage.OutputTokens != 7 {
		t.Fatalf("usage mismatch: %+v", last.Usage)
	}
	if last.Usage.TotalTokens != 49 {
		t.Fatalf("total tokens should be computed: got %d", last.Usage.TotalTokens)
	}

	// Deltas must concatenate to the full message.
	var acc string
	for _, ev := range events {
		if ev.Type == StreamDelta {
			acc += ev.Delta
		}
	}
	if acc != "hello world" {
		t.Fatalf("concatenated deltas mismatch: %q", acc)
	}
}

// TestOpenAICompatStream_EmitsStartAndFinishReason covers the
// OpenAI-compatible branch: StreamStart appears with the provider label,
// StreamDone carries finish_reason translated to StopReason, and usage is
// surfaced when the upstream emits it (OpenAI returns usage only when
// stream_options.include_usage is set, but we accept it either way).
func TestOpenAICompatStream_EmitsStartAndFinishReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"delta\":{\"content\":\"foo \"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"delta\":{\"content\":\"bar\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("generic", "gpt-test", "", srv.URL, 0, 0)
	ch, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	events := collectStream(t, ch)
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != StreamStart {
		t.Fatalf("first event should be StreamStart, got %+v", events[0])
	}
	if events[0].Provider != "generic" {
		t.Fatalf("provider label mismatch: %+v", events[0])
	}

	last := events[len(events)-1]
	if last.Type != StreamDone {
		t.Fatalf("last event should be StreamDone, got %+v", last)
	}
	if last.StopReason != StopEnd {
		t.Fatalf("finish_reason=stop should map to StopEnd, got %q", last.StopReason)
	}
	if last.Usage == nil {
		t.Fatalf("usage should be populated when upstream emits it")
	}
	if last.Usage.InputTokens != 10 || last.Usage.OutputTokens != 3 || last.Usage.TotalTokens != 13 {
		t.Fatalf("usage mismatch: %+v", last.Usage)
	}

	var acc string
	for _, ev := range events {
		if ev.Type == StreamDelta {
			acc += ev.Delta
		}
	}
	if acc != "foo bar" {
		t.Fatalf("concatenated deltas mismatch: %q", acc)
	}
}

// TestOfflineStream_EmitsMetadata ensures the always-on offline provider
// participates in the unified stream shape so TUI/web consumers can treat all
// providers uniformly.
func TestOfflineStream_EmitsMetadata(t *testing.T) {
	p := NewOfflineProvider()
	ch, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "any question"}},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	events := collectStream(t, ch)
	if len(events) < 2 {
		t.Fatalf("expected StreamStart + StreamDone at minimum, got %d", len(events))
	}
	if events[0].Type != StreamStart {
		t.Fatalf("first event should be StreamStart, got %+v", events[0])
	}
	if events[0].Provider == "" {
		t.Fatalf("offline start should set Provider")
	}
	last := events[len(events)-1]
	if last.Type != StreamDone {
		t.Fatalf("last event should be StreamDone, got %+v", last)
	}
	if last.Usage == nil {
		t.Fatalf("offline done should populate usage")
	}
	if last.StopReason == "" {
		t.Fatalf("offline done should populate stop_reason")
	}
}
