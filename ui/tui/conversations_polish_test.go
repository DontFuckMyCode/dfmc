package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
)

func TestConversationsHitsChip(t *testing.T) {
	if !strings.Contains(ansi.Strip(conversationsHitsChip(0)), "0 hits") {
		t.Errorf("zero-hit chip lost label")
	}
	if !strings.Contains(ansi.Strip(conversationsHitsChip(5)), "5 hits") {
		t.Errorf("nonzero chip should report count")
	}
}

func TestRenderBranchTree_GlyphsAndActiveMarker(t *testing.T) {
	branches := []conversationBranchSummary{
		{Name: "main", Messages: 12},
		{Name: "fork-debug", Messages: 5},
		{Name: "fork-refactor", Messages: 3},
	}
	out := renderBranchTree(branches, "fork-debug")
	stripped := strings.Join(out, "\n")
	stripped = ansi.Strip(stripped)
	// "branches:" header is present.
	if !strings.Contains(stripped, "branches:") {
		t.Errorf("expected branches: header, got:\n%s", stripped)
	}
	// First two branches use ├─; last uses └─.
	if !strings.Contains(stripped, "├─") {
		t.Errorf("expected ├─ glyph for mid-list branch, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "└─") {
		t.Errorf("expected └─ glyph for last branch, got:\n%s", stripped)
	}
	// Active marker present near fork-debug.
	if !strings.Contains(stripped, "●") {
		t.Errorf("expected ● active marker, got:\n%s", stripped)
	}
}

func TestRenderBranchTree_SingleBranchHandled(t *testing.T) {
	branches := []conversationBranchSummary{
		{Name: "main", Messages: 12},
	}
	// Helper renders whatever it gets — the caller is responsible for
	// the "len > 1" guard. Verify the output stays well-formed even on
	// a single-branch input.
	out := renderBranchTree(branches, "main")
	if len(out) == 0 {
		t.Fatal("expected at least the branches: header")
	}
	stripped := ansi.Strip(strings.Join(out, "\n"))
	if !strings.Contains(stripped, "└─") {
		t.Errorf("single branch should still use └─ glyph, got:\n%s", stripped)
	}
}

func TestRenderConversationsView_SurfacesLiveSearchAndHitChip(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.conversations.entries = []conversation.Summary{
		{ID: "abc-123", Provider: "anthropic", Model: "claude"},
		{ID: "def-456", Provider: "openai", Model: "gpt-4o"},
	}
	m.conversations.searchActive = true
	m.conversations.query = "anth"
	view := m.renderConversationsView(140)
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "Search:") {
		t.Errorf("expected live search box, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "1 hits") {
		t.Errorf("expected 1-hit chip, got:\n%s", stripped)
	}
}

func TestRenderConversationsView_ZeroHitChipOnMiss(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.conversations.entries = []conversation.Summary{
		{ID: "abc-123", Provider: "anthropic"},
	}
	m.conversations.query = "nonexistent"
	view := m.renderConversationsView(140)
	if !strings.Contains(ansi.Strip(view), "0 hits") {
		t.Errorf("expected 0-hit chip on miss, got:\n%s", view)
	}
}
