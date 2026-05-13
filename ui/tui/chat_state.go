package tui

// chat_state.go — chat composer + transcript + paste/queue/intent
// state. Hot-path fields touched on every keystroke and stream event;
// keeping them in one place makes the keypress/render code easier to
// reason about. Sibling state lives in:
//
//   panel_states.go    — diagnostic-tab panel state (memory, codemap,
//                        prompts, etc.) + statusPanelState +
//                        diagnosticPanelsState grouping.
//   runtime_state.go   — agentLoopState, sessionTelemetry,
//                        subagentRuntimeItem, statsPanelMode, uiToggles.
//   view_state.go      — Files/Patch/Tools/Workflow tab state +
//                        floating tasks overlay.

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// chatState — the chat tab's hot path: composer state, transcript,
// in-flight stream lifecycle, and the FIFO of queued submissions /btw
// notes that arrive while the engine is busy. The fields cluster into
// three loose groups that all live here because they're touched together
// on every keystroke and stream event:
//
//   - composer    — input, cursor, cursorManual, cursorInput
//   - stream      — sending, streamIndex/Messages/Cancel/StartedAt,
//     userCancelledStream, spinnerFrame/Ticking, scrollback
//   - queue/tools — pendingQueue, pendingNoteCount, toolPending, toolName
//
// `transcript` is the rendered history; `scrollback` is how far PageUp
// has scrolled us back from the tail (0 = pinned to latest).
type chatState struct {
	transcript          []chatLine
	input               string
	cursor              int
	cursorManual        bool
	cursorInput         string
	sending             bool
	streamIndex         int
	streamMessages      <-chan tea.Msg
	streamCancel        context.CancelFunc
	userCancelledStream bool
	pendingQueue        []string
	pendingNoteCount    int
	streamStartedAt     time.Time
	streamInputTokens   int
	spinnerFrame        int
	spinnerTicking      bool
	scrollback          int
	toolPending         bool
	toolName            string
	mentionPickerOpen   bool
	// pasteBlocks stores atomic multi-line paste segments. The composer
	// only contains their placeholders; composeInput replaces placeholders
	// with the original text at submit time. pasteBurst* catches terminals
	// that send multi-line paste as "line text" + Enter events while a
	// stream is active, so pasted lines do not become many queued messages.
	pasteBlocks         []pasteBlock
	pasteBurstUntil     time.Time
	pasteBurstBlock     int
	pasteCandidateStart int
	pasteCandidateEnd   int
	pasteCandidateRunes int
	pasteCandidateBulk  bool
	pasteCandidateSince time.Time
	pasteCandidateLast  time.Time
	suppressPasteRender bool
	// pinnedAssistantTurns marks 1-based assistant-turn numbers as
	// transcript anchors. Phase E item 1 — pin/fork/save chips render
	// under each assistant message; pinning is a local toggle (engine
	// stays out of it) so the user can earmark answers worth jumping
	// back to. Cleared on /chat new because anchors are scoped to the
	// active conversation.
	pinnedAssistantTurns map[int]bool
}

// pasteBlock represents one multi-line paste operation.
type pasteBlock struct {
	content   string // original pasted text (newlines preserved)
	blockNum  int    // 1-based sequence number
	lineCount int    // number of lines in the content
}

// assistantNextActionsState caches the most recent `[next: ...]` tail
// block parsed from an assistant turn. Rendered as a numbered starter
// strip under the latest answer — pressing 1/2/3 (or clicking, in a
// future mouse pass) drops the corresponding action text into the
// composer. Cleared on the next user submission because the
// suggestions were scoped to the previous answer's situation.
type assistantNextActionsState struct {
	actions    []string
	receivedAt time.Time
}

// composeInput reconstructs the full submission text from all paste blocks
// and the visible composer text. Paste block placeholders are replaced
// with the original content.
func (m Model) composeInput() string {
	var full strings.Builder
	// Reconstruct from stored blocks + visible input
	blocks := m.chat.pasteBlocks
	if len(blocks) == 0 {
		return m.chat.input
	}
	// The visible m.chat.input contains placeholders like
	// "[Pasted #1 3 lines]" interleaved with regular typed text.
	// We reconstruct by scanning the input left-to-right and substituting.
	rest := m.chat.input
	for len(rest) > 0 {
		matched := false
		for _, b := range blocks {
			placeholder := b.placeholder()
			if strings.HasPrefix(rest, placeholder) {
				full.WriteString(b.content)
				rest = rest[len(placeholder):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// No placeholder match — take one character
		r, size := utf8.DecodeRuneInString(rest)
		if r == utf8.RuneError && size == 0 {
			break
		}
		full.WriteRune(r)
		rest = rest[size:]
	}
	return full.String()
}

// placeholder returns the compact display string for this block. Keep this
// ASCII and visually atomic; deleteInputRange treats any edit inside it as a
// deletion of the stored paste content too.
func (b pasteBlock) placeholder() string {
	return fmt.Sprintf("[Pasted #%d %d lines]", b.blockNum, b.lineCount)
}

// intentState — most recent decision from the engine's intent router,
// plus a verbose flag that controls whether enrichments surface in the
// chat transcript as gray "you said X / agent saw Y" pairs. The chip
// in the chat header reads from active+source; the slash command
// /intent show prints the full last decision via Recent.
//
// Engine publishes "intent:decision" events via EventBus; the TUI
// handler pulls fields out of the payload and assigns into this
// struct. Empty struct = "intent layer hasn't fired yet this session".
type intentState struct {
	verbose          bool   // /intent verbose toggles transcript pairs
	lastIntent       string // "resume" | "new" | "clarify" | ""
	lastSource       string // "llm" | "fallback" | ""
	lastRaw          string
	lastEnriched     string
	lastReasoning    string
	lastFollowUp     string
	lastLatencyMs    int64
	lastDecisionAtMs int64 // Unix millis; 0 when never fired
}

// inputHistoryState — chat composer command history (up/down recall),
// plus the in-progress draft we stash before navigating into history so
// pressing down past the newest entry restores what the user was typing.
type inputHistoryState struct {
	history []string
	index   int
	draft   string
}

// slashMenuState — composer popup indices for the four completion menus
// (slash command, slash argument, file mention, quick action). Each is
// the highlighted-row index inside the corresponding rendered list.
type slashMenuState struct {
	command     int
	commandArg  int
	mention     int
	quickAction int
}

func (s *slashMenuState) resetIndices() {
	s.command = 0
	s.commandArg = 0
	s.mention = 0
	s.quickAction = 0
}

// commandPickerState — modal chooser state for slash commands that need
// an interactive selection (provider/model/skill). Active flips on while
// the picker is open and pins keyboard focus to the picker handler.
type commandPickerState struct {
	active  bool
	kind    string
	query   string
	index   int
	persist bool
	all     []commandPickerItem
}
