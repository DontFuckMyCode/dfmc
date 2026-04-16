package promptlib

import (
	"strings"
	"testing"
)

func TestSplitRenderedBundle_NoMarker(t *testing.T) {
	bundle := splitRenderedBundle("hello world")
	if bundle == nil {
		t.Fatalf("expected bundle, got nil")
	}
	if len(bundle.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(bundle.Sections))
	}
	if bundle.Sections[0].Cacheable {
		t.Fatalf("section without marker must not be cacheable")
	}
	if bundle.Sections[0].Label != "dynamic" {
		t.Fatalf("expected label=dynamic, got %q", bundle.Sections[0].Label)
	}
	if bundle.HasCacheable() {
		t.Fatalf("bundle without marker should report no cacheable sections")
	}
}

func TestSplitRenderedBundle_WithMarker(t *testing.T) {
	text := "stable prefix line one\nstable prefix line two\n" + CacheBreakMarker + "\ndynamic tail"
	bundle := splitRenderedBundle(text)
	if len(bundle.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(bundle.Sections))
	}
	if !bundle.Sections[0].Cacheable || bundle.Sections[0].Label != "stable" {
		t.Fatalf("section 0 should be stable+cacheable: %+v", bundle.Sections[0])
	}
	if bundle.Sections[1].Cacheable || bundle.Sections[1].Label != "dynamic" {
		t.Fatalf("section 1 should be dynamic: %+v", bundle.Sections[1])
	}
	if strings.Contains(bundle.Sections[0].Text, CacheBreakMarker) || strings.Contains(bundle.Sections[1].Text, CacheBreakMarker) {
		t.Fatalf("marker must be stripped from both sections")
	}
}

func TestSplitRenderedBundle_MarkerWithEmptySide(t *testing.T) {
	// Marker at the very start — only dynamic content.
	b := splitRenderedBundle(CacheBreakMarker + "\nonly dynamic")
	if len(b.Sections) != 1 || b.Sections[0].Cacheable {
		t.Fatalf("leading marker should yield 1 dynamic-only section: %+v", b.Sections)
	}
	// Marker at the end — only stable content.
	b = splitRenderedBundle("only stable\n" + CacheBreakMarker)
	if len(b.Sections) != 1 || !b.Sections[0].Cacheable {
		t.Fatalf("trailing marker should yield 1 stable-only section: %+v", b.Sections)
	}
}

func TestSplitRenderedBundle_Empty(t *testing.T) {
	b := splitRenderedBundle("")
	if b == nil {
		t.Fatalf("expected non-nil bundle for empty input")
	}
	if len(b.Sections) != 0 {
		t.Fatalf("empty input should produce no sections, got %d", len(b.Sections))
	}
	if b.Text() != "" {
		t.Fatalf("empty bundle Text() should be empty")
	}
	if b.HasCacheable() {
		t.Fatalf("empty bundle must not report cacheable")
	}
}

func TestPromptBundle_AccessorSplit(t *testing.T) {
	text := "stable-only\n" + CacheBreakMarker + "\ndynamic-only"
	b := splitRenderedBundle(text)
	if got := b.CacheableText(); got != "stable-only" {
		t.Fatalf("CacheableText mismatch: %q", got)
	}
	if got := b.DynamicText(); got != "dynamic-only" {
		t.Fatalf("DynamicText mismatch: %q", got)
	}
	if !b.HasCacheable() {
		t.Fatalf("bundle with stable section should report cacheable")
	}
	flat := b.Text()
	if !strings.Contains(flat, "stable-only") || !strings.Contains(flat, "dynamic-only") {
		t.Fatalf("Text() should include both sections: %q", flat)
	}
	if strings.Contains(flat, CacheBreakMarker) {
		t.Fatalf("Text() must not reintroduce marker")
	}
}

func TestRenderBundle_UsesMarkerFromTemplate(t *testing.T) {
	// The default system.base template carries the marker — rendering the
	// shipped library should yield a cacheable prefix.
	lib := New()
	bundle := lib.RenderBundle(RenderRequest{
		Type: "system",
		Task: "general",
		Vars: map[string]string{
			"project_root":     "/tmp/demo",
			"task":             "general",
			"language":         "go",
			"profile":          "compact",
			"role":             "generalist",
			"project_brief":    "demo brief",
			"tools_overview":   "read_file, write_file",
			"tool_call_policy": "- be precise",
			"response_policy":  "- be terse",
			"user_query":       "hello world",
			"context_files":    "none",
			"injected_context": "",
		},
	})
	if !bundle.HasCacheable() {
		t.Fatalf("expected cacheable prefix when template carries marker, got bundle=%+v", bundle)
	}
	if !strings.Contains(bundle.CacheableText(), "Baseline contract") {
		t.Fatalf("stable prefix should include baseline contract: %q", bundle.CacheableText())
	}
	if !strings.Contains(bundle.DynamicText(), "hello world") {
		t.Fatalf("dynamic section should include user_query: %q", bundle.DynamicText())
	}
	if strings.Contains(bundle.CacheableText(), "hello world") {
		t.Fatalf("stable prefix must not include per-request user_query")
	}
}
