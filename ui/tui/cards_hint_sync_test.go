package tui

import (
	"context"
	"strings"
	"testing"
)

// cards_hint_sync_test.go pins the card-based affordance hints in
// the Files and Tools panels against drift. These two panels don't
// use a single inline hint line (covered by hint_help_sync_test.go);
// they expose keys through panelCard.FooterHint and per-row Key
// fields. The contract is the same: every key claimed must exist in
// the panel's key handler AND show up in the help overlay.
//
// Real bugs this test would have caught when written:
//   - Files Actions card claimed `F4 Patch for diffs` but F4 opens
//     Workflow; Patch is on F3. The card lied about a tab-switch
//     key, so a user following the hint would land in the wrong
//     panel and lose context for the rest of the task.

// TestFilesMetaCards_KeysExistInPanelHandler asserts every per-row
// Key field on the Files cards corresponds to a real handler binding.
// Catches "card mentions a key the handler dropped" regressions.
func TestFilesMetaCards_KeysExistInPanelHandler(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.filesView.path = "internal/engine/engine.go" // populate metadata
	m.filesView.preview = "package engine\n"
	cards := m.filesMetaCards()
	if len(cards) == 0 {
		t.Fatal("expected files metadata cards to render with a file selected")
	}
	// handleFilesKey accepts: r, j/down, k/up, enter, right, l, p, i, e, v, /, c.
	supported := map[string]bool{
		"r": true, "j": true, "k": true, "enter": true, "right": true,
		"l": true, "p": true, "i": true, "e": true, "v": true,
		"/": true, "c": true,
	}
	for _, card := range cards {
		for _, row := range card.Rows {
			key := strings.ToLower(strings.TrimSpace(row.Key))
			if key == "" {
				continue
			}
			// Only check single-letter keys against the panel handler.
			// Multi-word keys (`up/down`, etc.) are descriptive labels.
			if len(key) == 1 && !supported[key] {
				t.Errorf("[Files %s card] row claims key %q but handleFilesKey doesn't bind it",
					card.Title, key)
			}
		}
	}
}

// TestFilesMetaCards_FKeyTabReferencesAreAccurate is the regression
// pin for the F4-vs-F3 bug. Any FooterHint mentioning an F-key tab
// reference must name the tab the F-key actually activates.
func TestFilesMetaCards_FKeyTabReferencesAreAccurate(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.filesView.path = "main.go"
	cards := m.filesMetaCards()

	// Canonical F-key → tab mapping from shortcut_panels.go.
	expected := map[string]string{
		"F1": "Chat",
		"F2": "Files",
		"F3": "Patch",
		"F4": "Workflow",
		"F5": "Activity",
		"F6": "Memory",
	}

	for _, card := range cards {
		hint := card.FooterHint
		for fkey, tab := range expected {
			if !strings.Contains(hint, fkey) {
				continue
			}
			// If the hint mentions this F-key, the immediately-following
			// word should be the right tab name (allowing for stylistic
			// commas, spaces, etc.).
			afterIdx := strings.Index(hint, fkey) + len(fkey)
			if afterIdx >= len(hint) {
				continue
			}
			rest := hint[afterIdx:]
			// Grab the next word.
			rest = strings.TrimLeft(rest, " ")
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			claimed := strings.TrimRight(fields[0], ",")
			if !strings.EqualFold(claimed, tab) {
				t.Errorf("[Files %s card] FooterHint says %q but %s actually opens %q. Hint: %q",
					card.Title, fkey+" "+claimed, fkey, tab, hint)
			}
		}
	}
}

func TestToolsMetaCards_KeysExistInPanelHandler(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// Tools cards need a non-empty tools list to render.
	tools := []string{"read_file", "edit_file", "grep_codebase"}
	cards := m.toolsMetaCards(tools)
	if len(cards) == 0 {
		t.Fatal("expected tools metadata cards to render with a tools list")
	}
	// handleToolsKey + handleToolSelectionKey accept: down, up, enter,
	// right, e, x, r, /, c.
	supported := map[string]bool{
		"up": true, "down": true, "enter": true, "right": true,
		"e": true, "x": true, "r": true, "/": true, "c": true,
		"up/down": true, // composite label
	}
	for _, card := range cards {
		// Only the "Actions" card lists keyboard bindings — the
		// "Current" card uses Rows to display informational fields
		// (Name / Position / Args), not keys.
		if card.Title != "Actions" {
			continue
		}
		for _, row := range card.Rows {
			key := strings.ToLower(strings.TrimSpace(row.Key))
			if key == "" {
				continue
			}
			if !supported[key] {
				t.Errorf("[Tools %s card] row claims key %q but the handler doesn't bind it",
					card.Title, key)
			}
		}
	}
}

// TestCardFooterHints_KeysAppearInHelpOverlay asserts each FooterHint
// substring with a key reference shows up somewhere in renderTUIHelp().
// Global keys (Ctrl+W, F3) live in the chat-tab / panel-list sections;
// panel-local keys live in their PANEL section. We just require the
// key glyph appears anywhere in the overlay.
func TestCardFooterHints_KeysAppearInHelpOverlay(t *testing.T) {
	help := strings.ToLower(renderTUIHelp())
	m := NewModel(context.Background(), nil)
	m.filesView.path = "main.go"

	cases := []struct {
		owner string
		hint  string
		// keys that must each appear somewhere in renderTUIHelp().
		keys []string
	}{
		{"Files Status", "p toggle · r reload index", []string{"p", "r"}},
		{"Files Actions", "Ctrl+W context preview · F3 Patch for diffs", []string{"ctrl+w", "f3"}},
		{"Tools Actions", "ctrl+h keys", []string{"ctrl+h"}},
	}
	for _, c := range cases {
		// First sanity: the hint string is what we claim it is.
		// Otherwise our test is asserting against fiction.
		found := false
		for _, card := range append(m.filesMetaCards(), m.toolsMetaCards([]string{"read_file"})...) {
			if card.FooterHint == c.hint {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[%s] expected FooterHint %q not present on any card — test data is stale",
				c.owner, c.hint)
			continue
		}
		for _, key := range c.keys {
			if !strings.Contains(help, strings.ToLower(key)) {
				t.Errorf("[%s] key %q from FooterHint not found anywhere in help overlay", c.owner, key)
			}
		}
	}
}
