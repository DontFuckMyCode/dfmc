package tui

import (
	"context"
	"strings"
	"testing"
)

// TestHelpOverlayMentionsNewTranscriptCommands ensures the help overlay
// surface lists every slash + keybinding we added in the chat history
// polish pass. If a new command lands in chat_commands_dispatch_groups.go
// without a help entry, this fails — keeping the discoverability path
// honest as the feature set grows.
func TestHelpOverlayMentionsNewTranscriptCommands(t *testing.T) {
	text := renderTUIHelp()
	wants := []string{
		"/history search",
		"/jump N",
		"/next",
		"/prev",
		"/expand",
		"/collapse",
		"/toolshow",
		"Ctrl+Home",
		"Ctrl+Y",
	}
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("help overlay missing %q", w)
		}
	}
}

func TestShortcutsPanelMentionsNewKeybindings(t *testing.T) {
	m := NewModel(context.Background(), nil)
	rows := m.shortcutsChatComposerSection()
	joined := strings.Join(rows, "\n")
	wants := []string{
		"Ctrl+Home/End",
		"Ctrl+Y",
		"/history search",
		"/next /prev",
		"/jump N",
		"/toolshow",
	}
	for _, w := range wants {
		if !strings.Contains(joined, w) {
			t.Errorf("shortcuts composer section missing %q, got:\n%s", w, joined)
		}
	}
}
