package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// captureRequest serves one canned response and stores the inbound request
// body+headers so the test can assert on them.
type captureRequest struct {
	path    string
	headers http.Header
	body    []byte
}

func newGoogleTestServer(t *testing.T, status int, response string) (*httptest.Server, *captureRequest) {
	t.Helper()
	cap := &captureRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.path = r.URL.Path + "?" + r.URL.RawQuery
		cap.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		cap.body = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func TestGoogleCompleteHappyPath(t *testing.T) {
	resp := `{
		"candidates": [{
			"content": {"role": "model", "parts": [{"text": "hello from gemini"}]},
			"finishReason": "STOP",
			"index": 0
		}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 3, "totalTokenCount": 13}
	}`
	srv, cap := newGoogleTestServer(t, 200, resp)

	p := NewGoogleProvider("gemini-test", "test-key", srv.URL, 0, 0)
	got, err := p.Complete(context.Background(), CompletionRequest{
		System: "be concise",
		Messages: []Message{
			{Role: "user", Content: "ping"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "hello from gemini" {
		t.Fatalf("text=%q", got.Text)
	}
	if got.StopReason != StopEnd {
		t.Fatalf("stop=%v want end_turn", got.StopReason)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 3 {
		t.Fatalf("usage=%+v", got.Usage)
	}
	// Endpoint shape: /models/{model}:generateContent (no key in URL; it
	// lives on the header).
	if !strings.Contains(cap.path, "/models/gemini-test:generateContent") {
		t.Fatalf("unexpected path=%q", cap.path)
	}
	if cap.headers.Get("x-goog-api-key") != "test-key" {
		t.Fatalf("missing api key header; got %+v", cap.headers)
	}
	var payload map[string]any
	if err := json.Unmarshal(cap.body, &payload); err != nil {
		t.Fatalf("decode outbound body: %v", err)
	}
	if sys, ok := payload["systemInstruction"].(map[string]any); !ok {
		t.Fatalf("missing systemInstruction: %v", payload)
	} else {
		parts := sys["parts"].([]any)
		if parts[0].(map[string]any)["text"] != "be concise" {
			t.Fatalf("system text not forwarded: %+v", sys)
		}
	}
}

func TestGoogleMissingAPIKeyUnavailable(t *testing.T) {
	p := NewGoogleProvider("gemini-test", "", "", 0, 0)
	_, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err=%v want ErrProviderUnavailable", err)
	}
}

func TestGoogleContextOverflowSurfaced(t *testing.T) {
	srv, _ := newGoogleTestServer(t, 400, `{"error":{"message":"input exceeds the maximum number of tokens"}}`)
	p := NewGoogleProvider("gemini-test", "test-key", srv.URL, 0, 0)
	_, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: "user", Content: "too big"}}})
	if err == nil || !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("err=%v want ErrContextOverflow", err)
	}
}

func TestGoogleToolCallRoundTrip(t *testing.T) {
	resp := `{
		"candidates": [{
			"content": {"role":"model","parts":[
				{"text":"thinking..."},
				{"functionCall":{"name":"read_file","args":{"path":"README.md"}}}
			]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 2, "totalTokenCount": 7}
	}`
	srv, cap := newGoogleTestServer(t, 200, resp)
	p := NewGoogleProvider("gemini-test", "test-key", srv.URL, 0, 0)
	got, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "read the readme"}},
		Tools: []ToolDescriptor{{
			Name:        "read_file",
			Description: "read a file",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.StopReason != StopTool {
		t.Fatalf("stop=%v want tool_use", got.StopReason)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls=%+v", got.ToolCalls)
	}
	if got.ToolCalls[0].Input["path"] != "README.md" {
		t.Fatalf("tool input=%+v", got.ToolCalls[0].Input)
	}
	// Outbound body should contain a tools[].functionDeclarations entry and
	// a toolConfig.functionCallingConfig.mode=AUTO.
	var payload map[string]any
	_ = json.Unmarshal(cap.body, &payload)
	tools, _ := payload["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%+v", tools)
	}
	decls, _ := tools[0].(map[string]any)["functionDeclarations"].([]any)
	if len(decls) != 1 || decls[0].(map[string]any)["name"] != "read_file" {
		t.Fatalf("functionDeclarations=%+v", decls)
	}
	cfg, _ := payload["toolConfig"].(map[string]any)
	mode := cfg["functionCallingConfig"].(map[string]any)["mode"]
	if mode != "AUTO" {
		t.Fatalf("mode=%v want AUTO", mode)
	}
}

func TestGoogleFunctionResponseRoundTrip(t *testing.T) {
	srv, cap := newGoogleTestServer(t, 200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)
	p := NewGoogleProvider("gemini-test", "test-key", srv.URL, 0, 0)
	// Simulate a tool round-trip: user → assistant tool_call → tool result → ask for final answer.
	_, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "read README"},
			{Role: "assistant", Content: "calling read_file", ToolCalls: []ToolCall{{ID: "call_read_file_0", Name: "read_file", Input: map[string]any{"path": "README.md"}}}},
			{Role: "tool", Content: "# DFMC\nhello", ToolCallID: "call_read_file_0", ToolName: "read_file"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal(cap.body, &payload)
	contents, _ := payload["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("want 3 contents, got %d: %+v", len(contents), contents)
	}
	// Second turn should be role=model with a functionCall part.
	second, _ := contents[1].(map[string]any)
	if second["role"] != "model" {
		t.Fatalf("second role=%v", second["role"])
	}
	parts2, _ := second["parts"].([]any)
	var foundCall bool
	for _, raw := range parts2 {
		part, _ := raw.(map[string]any)
		if _, ok := part["functionCall"]; ok {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatalf("no functionCall part in model turn: %+v", parts2)
	}
	// Third turn should be role=user with a functionResponse part.
	third, _ := contents[2].(map[string]any)
	if third["role"] != "user" {
		t.Fatalf("third role=%v", third["role"])
	}
	parts3, _ := third["parts"].([]any)
	fr, _ := parts3[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "read_file" {
		t.Fatalf("functionResponse name=%v", fr["name"])
	}
	if got := fr["response"].(map[string]any)["result"]; got != "# DFMC\nhello" {
		t.Fatalf("tool result text=%v", got)
	}
}

func TestGoogleContextChunksInjected(t *testing.T) {
	srv, cap := newGoogleTestServer(t, 200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	p := NewGoogleProvider("gemini-test", "test-key", srv.URL, 0, 0)
	_, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "summarise"}},
		Context:  []types.ContextChunk{{Path: "foo.go", Content: "package foo", Score: 0.9}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal(cap.body, &payload)
	contents, _ := payload["contents"].([]any)
	first, _ := contents[0].(map[string]any)
	parts, _ := first["parts"].([]any)
	text := parts[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "foo.go") {
		t.Fatalf("context chunk not injected: %q", text)
	}
}

func TestGoogleStreamHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "alt=sse") {
			t.Errorf("stream request missing alt=sse: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		frames := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]},"index":0}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"index":0}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`,
		}
		for _, f := range frames {
			_, _ = w.Write([]byte("data: " + f + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	p := NewGoogleProvider("gemini-test", "test-key", srv.URL, 0, 0)
	ch, err := p.Stream(context.Background(), CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var builder strings.Builder
	var done StreamEvent
	for evt := range ch {
		switch evt.Type {
		case StreamDelta:
			builder.WriteString(evt.Delta)
		case StreamDone:
			done = evt
		case StreamError:
			t.Fatalf("stream error: %v", evt.Err)
		}
	}
	if builder.String() != "hello!" {
		t.Fatalf("deltas=%q want hello!", builder.String())
	}
	if done.Type != StreamDone {
		t.Fatalf("missing Done event")
	}
	if done.StopReason != StopEnd {
		t.Fatalf("stop=%v", done.StopReason)
	}
	if done.Usage == nil || done.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%+v", done.Usage)
	}
}

func TestGoogleHintsAdvertiseTools(t *testing.T) {
	p := NewGoogleProvider("gemini-test", "key", "", 0, 0)
	h := p.Hints()
	if !h.SupportsTools || h.ToolStyle != "function-calling" {
		t.Fatalf("hints=%+v", h)
	}
}
