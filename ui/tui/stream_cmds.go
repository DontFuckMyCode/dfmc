// Chat/event stream bridging: tea.Cmd factories that fan an engine
// stream through bubbletea's message pump, plus the Model-level cancel
// helpers that abort an in-flight stream. Extracted from tui.go so the
// tui.go dispatcher stays focused on Model/View/Update.

package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

func undoConversationCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return conversationUndoMsg{err: fmt.Errorf("engine is nil")}
		}
		removed, err := eng.ConversationUndoLast()
		return conversationUndoMsg{removed: removed, err: err}
	}
}

func startChatStream(ctx context.Context, eng *engine.Engine, question string) <-chan tea.Msg {
	out := make(chan tea.Msg, 64)
	go func() {
		defer close(out)
		if eng == nil {
			sendChatStreamMsg(ctx, out, chatErrMsg{err: fmt.Errorf("engine is nil")})
			return
		}
		stream, err := eng.StreamAsk(ctx, question)
		if err != nil {
			sendChatStreamMsg(ctx, out, chatErrMsg{err: err})
			return
		}
		for ev := range stream {
			switch ev.Type {
			case provider.StreamDelta:
				if !sendChatStreamMsg(ctx, out, chatDeltaMsg{delta: ev.Delta}) {
					return
				}
			case provider.StreamError:
				if ev.Err != nil {
					sendChatStreamMsg(ctx, out, chatErrMsg{err: ev.Err})
				} else {
					sendChatStreamMsg(ctx, out, chatErrMsg{err: fmt.Errorf("stream error")})
				}
				return
			case provider.StreamDone:
				sendChatStreamMsg(ctx, out, chatDoneMsg{})
				return
			}
		}
		sendChatStreamMsg(ctx, out, streamClosedMsg{})
	}()
	return out
}

func sendChatStreamMsg(ctx context.Context, out chan<- tea.Msg, msg tea.Msg) bool {
	if out == nil {
		return false
	}
	if ctx == nil {
		out <- msg
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case out <- msg:
		return true
	}
}

// startChatResumeStream runs ResumeAgent in a goroutine and surfaces the
// resulting answer through the same chatDelta/chatDone/chatErr channel the
// normal stream path uses. Mirrors startChatStream so the UI needs no new
// wiring — this is the minimum integration surface for resume.
func startChatResumeStream(ctx context.Context, eng *engine.Engine, note string) <-chan tea.Msg {
	out := make(chan tea.Msg, 8)
	go func() {
		defer close(out)
		if eng == nil {
			out <- chatErrMsg{err: fmt.Errorf("engine is nil")}
			return
		}
		completion, err := eng.ResumeAgent(ctx, note)
		if err != nil {
			out <- chatErrMsg{err: err}
			return
		}
		if answer := strings.TrimSpace(completion.Answer); answer != "" {
			out <- chatDeltaMsg{delta: answer}
		}
		out <- chatDoneMsg{}
	}()
	return out
}

func waitForStreamMsg(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return msg
	}
}

func subscribeEventsCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.EventBus == nil {
			return nil
		}
		return eventSubscribedMsg{ch: eng.EventBus.Subscribe("*")}
	}
}

func waitForEventMsg(ch <-chan engine.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return engineEventMsg{event: ev}
	}
}

// clearStreamCancel drops the stored per-stream CancelFunc. Called from
// every chat-lifecycle terminus (done, err, closed, explicit cancel) so
// the next send starts clean and a stale cancel func can't be fired after
// the stream it owned already finished.
func (m *Model) clearStreamCancel() {
	m.chat.streamCancel = nil
}

// cancelActiveStream aborts an in-flight chat stream if one is running.
// Returns true if a cancel fired — the caller uses that to decide whether
// to emit the "cancelled by user" notice vs. fall through to other esc
// behavior like dismissing the parked-resume banner. The userCancelled
// flag lets the chatErrMsg reader distinguish a clean user-driven stop
// from a provider/network error so we can tailor the message.
func (m *Model) cancelActiveStream() bool {
	if m.chat.streamCancel == nil {
		return false
	}
	m.chat.streamCancel()
	m.chat.streamCancel = nil
	m.chat.userCancelledStream = true
	return true
}
