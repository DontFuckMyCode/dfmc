package provider

import (
	"testing"
)

// TestAnthropicSystemPayload_StringFallback covers requests that only carry a
// flat System string — the payload should be that string unchanged.
func TestAnthropicSystemPayload_StringFallback(t *testing.T) {
	req := CompletionRequest{System: "flat system prompt"}
	got := anthropicSystemPayload(req)
	if got != "flat system prompt" {
		t.Fatalf("expected flat string echo, got %#v", got)
	}
}

// TestAnthropicSystemPayload_NilWhenEmpty guarantees we never send an empty
// system field to Anthropic (they reject it).
func TestAnthropicSystemPayload_NilWhenEmpty(t *testing.T) {
	if got := anthropicSystemPayload(CompletionRequest{}); got != nil {
		t.Fatalf("expected nil for empty request, got %#v", got)
	}
	if got := anthropicSystemPayload(CompletionRequest{System: "   "}); got != nil {
		t.Fatalf("whitespace-only system should be nil, got %#v", got)
	}
}

// TestAnthropicSystemPayload_BlocksEmitCacheControl verifies that SystemBlocks
// produce an array-form payload with cache_control:ephemeral on cacheable
// blocks and no cache_control on dynamic blocks.
func TestAnthropicSystemPayload_BlocksEmitCacheControl(t *testing.T) {
	req := CompletionRequest{
		SystemBlocks: []SystemBlock{
			{Label: "stable", Text: "stable prefix", Cacheable: true},
			{Label: "dynamic", Text: "per-request tail", Cacheable: false},
		},
	}
	raw := anthropicSystemPayload(req)
	arr, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T: %#v", raw, raw)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(arr))
	}
	if arr[0]["text"] != "stable prefix" {
		t.Fatalf("stable block text mismatch: %#v", arr[0])
	}
	cc, ok := arr[0]["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("stable block missing cache_control: %#v", arr[0])
	}
	if cc["type"] != "ephemeral" {
		t.Fatalf("cache_control.type mismatch: %#v", cc)
	}
	if _, present := arr[1]["cache_control"]; present {
		t.Fatalf("dynamic block must not carry cache_control: %#v", arr[1])
	}
	if arr[1]["text"] != "per-request tail" {
		t.Fatalf("dynamic block text mismatch: %#v", arr[1])
	}
}

// TestAnthropicSystemPayload_SkipsEmptyBlocks ensures whitespace-only blocks
// are filtered out before we emit the array payload.
func TestAnthropicSystemPayload_SkipsEmptyBlocks(t *testing.T) {
	req := CompletionRequest{
		SystemBlocks: []SystemBlock{
			{Label: "stable", Text: " ", Cacheable: true},
			{Label: "real", Text: "real block", Cacheable: true},
		},
	}
	raw := anthropicSystemPayload(req)
	arr, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("expected slice payload, got %T", raw)
	}
	if len(arr) != 1 {
		t.Fatalf("whitespace block should be skipped: %#v", arr)
	}
	if arr[0]["text"] != "real block" {
		t.Fatalf("wrong block preserved: %#v", arr[0])
	}
}

// TestAnthropicSystemPayload_PrefersBlocksOverString documents the precedence
// rule: when both SystemBlocks and System are set, blocks win.
func TestAnthropicSystemPayload_PrefersBlocksOverString(t *testing.T) {
	req := CompletionRequest{
		System: "fallback string",
		SystemBlocks: []SystemBlock{
			{Label: "stable", Text: "block wins", Cacheable: true},
		},
	}
	raw := anthropicSystemPayload(req)
	arr, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("expected array when blocks present, got %T", raw)
	}
	if arr[0]["text"] != "block wins" {
		t.Fatalf("expected blocks to win, got %#v", arr[0])
	}
}
