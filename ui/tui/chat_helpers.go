package tui

import (
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func truncateForPanel(text string, width int) string {
	return truncateForPanelSized(text, width, 18)
}

// truncateForPanelSized lets callers choose the row cap so panels can
// scale with the user's terminal height instead of the historic 18-line
// hard cap that left tall windows mostly empty.
func truncateForPanelSized(text string, width, maxLines int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if maxLines <= 0 {
		maxLines = 18
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "... [truncated]")
	}
	for i, line := range lines {
		if width > 0 && len([]rune(line)) > width {
			runes := []rune(line)
			trimTo := max(width-14, 0)
			lines[i] = string(runes[:trimTo]) + "... [trimmed]"
		}
	}
	return strings.Join(lines, "\n")
}

// chatBubbleContent returns the text the chat transcript should render for
// one message. Unlike chatPreviewForLine (which collapses to a one-line
// digest for compact side views), this is the full content, optionally
// decorated with a streaming caret while the assistant is still generating.
func chatBubbleContent(item chatLine, streaming bool) string {
	content := strings.TrimRight(item.Content, " \t\r\n")
	if streaming {
		if content == "" {
			return subtleStyle.Render("… thinking") + " ▎"
		}
		return content + " ▎"
	}
	return content
}

func renderChatInputLine(input string, cursor int, manual bool, manualInput string, sending bool) string {
	// Multi-line composition: a literal "\n" in the buffer becomes a new
	// physical row. Continuation rows get a "  " indent instead of the "> "
	// prompt so the prompt glyph never repeats. The cursor "|" lands on the
	// correct logical row. Sending/streaming displays the raw buffer without
	// a cursor since we're not collecting keystrokes at that moment.
	if sending {
		return renderSendingInputBuffer(input)
	}
	runes := []rune(input)
	total := len(runes)
	if manual && manualInput != input {
		manual = false
	}
	if !manual {
		cursor = total
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > total {
		cursor = total
	}
	before := string(runes[:cursor])
	after := string(runes[cursor:])
	withCursor := before + "|" + after
	logical := strings.Split(withCursor, "\n")
	out := make([]string, 0, len(logical))
	for i, row := range logical {
		prefix := "> "
		if i > 0 {
			prefix = "  "
		}
		out = append(out, prefix+row)
	}
	return strings.Join(out, "\n")
}

// renderSendingInputBuffer prints the frozen input while a turn is streaming
// (no cursor, just the text with the same prompt rules as the live editor).
func renderSendingInputBuffer(input string) string {
	if !strings.ContainsRune(input, '\n') {
		return "> " + input
	}
	logical := strings.Split(input, "\n")
	out := make([]string, 0, len(logical))
	for i, row := range logical {
		prefix := "> "
		if i > 0 {
			prefix = "  "
		}
		out = append(out, prefix+row)
	}
	return strings.Join(out, "\n")
}

func chatDigest(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	preview := trimmed
	if first, _, ok := strings.Cut(trimmed, "\n"); ok {
		first = strings.TrimSpace(first)
		if first == "" {
			first = "[multiline]"
		}
		preview = first + " ..."
	}
	return preview
}

func blankFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func composeChatPrompt(current, addition string) string {
	current = strings.TrimSpace(current)
	addition = strings.TrimSpace(addition)
	switch {
	case current == "":
		return addition
	case addition == "":
		return current
	case strings.Contains(current, addition):
		return current
	case strings.HasSuffix(current, "[[file:") || strings.HasSuffix(current, " ") || strings.HasSuffix(current, "\n"):
		return current + addition
	default:
		return current + " " + addition
	}
}

func fileMarker(rel string) string {
	return fileMarkerRange(rel, "")
}

// fileMarkerRange emits the context-manager marker with an optional line
// range suffix (`#L10` or `#L10-L50`). The context manager's regex only
// accepts `#L<start>[-L?<end>]`, so callers must pass a pre-normalized
// suffix (see splitMentionToken). Uses types.FileMarkerPrefix/Suffix so
// the wire shape stays in sync with the parser.
func fileMarkerRange(rel, rangeSuffix string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return ""
	}
	rangeSuffix = strings.TrimSpace(rangeSuffix)
	return types.FileMarkerPrefix + rel + rangeSuffix + types.FileMarkerSuffix
}
