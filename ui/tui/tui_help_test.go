package tui

import (
	"strings"
	"testing"
)

func TestRenderTUIHelp_MixesRegistryAndShortcuts(t *testing.T) {
	out := renderTUIHelp()

	// Registry-backed verbs must be present.
	for _, verb := range []string{"ask", "status", "context", "conversation", "provider", "model"} {
		if !strings.Contains(out, verb) {
			t.Fatalf("TUI help missing registry verb %q", verb)
		}
	}
	// `serve` is CLI-only and must not leak onto the TUI surface.
	if strings.Contains(out, "serve") {
		t.Fatalf("TUI help must not expose CLI-only `serve`")
	}
	// TUI-only slash shortcuts are appended as a separate section.
	if !strings.Contains(out, "TUI-only shortcuts:") {
		t.Fatalf("TUI help missing shortcuts section")
	}
	for _, shortcut := range []string{"/reload", "/clear", "/quit", "/coach", "/hints", "/tools", "/diff", "/ls", "/grep", "/continue", "/btw"} {
		if !strings.Contains(out, shortcut) {
			t.Fatalf("TUI help missing shortcut %q", shortcut)
		}
	}
	// Mentions line documents the range-suffix syntax.
	if !strings.Contains(out, "@file.go:10-50") {
		t.Fatalf("TUI help missing mention range-syntax hint")
	}
	// Panel hotkey hint.
	if !strings.Contains(out, "F1 Chat") {
		t.Fatalf("TUI help missing panel hotkeys")
	}
}

func TestRenderTUICommandHelp_KnownAndUnknown(t *testing.T) {
	// Alias resolution: `conv` -> conversation details.
	detail := renderTUICommandHelp("conv")
	if !strings.Contains(detail, "conversation —") {
		t.Fatalf("expected conversation detail header, got %q", detail)
	}
	if !strings.Contains(detail, "Aliases: conv") {
		t.Fatalf("expected alias listing in detail view, got %q", detail)
	}

	// Unknown command yields a friendly pointer back to /help.
	miss := renderTUICommandHelp("definitely-not-a-command")
	if !strings.Contains(miss, "Unknown command") || !strings.Contains(miss, "/help") {
		t.Fatalf("unknown command should advise /help, got %q", miss)
	}
}
