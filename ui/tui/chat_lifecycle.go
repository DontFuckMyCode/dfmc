// Chat lifecycle: the entry points that turn user input into a live
// chat stream (submitChatQuestion), drain queued follow-ups
// (drainPendingQueue), resume a parked agent (startChatResume), kick
// off a tool-command flow (startChatToolCommand), nudge the spinner
// scheduler (ensureSpinnerTick), and format tool results into a
// transcript-friendly block (formatToolResultForChat). Extracted
// from tui.go so the file stays focused on Model / lifecycle.

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

// submitChatQuestion is the single entry point for turning user input into
// a chat turn. It resolves auto-tool intent, offline-guardrails, tool-use
// enforcement, quick actions, and streaming into the transcript with one
// cohesive path so Update doesn't grow a parallel chat state machine.
func (m Model) submitChatQuestion(question string, quickActions []quickActionSuggestion) (Model, tea.Cmd) {
	question = strings.TrimSpace(question)
	if question == "" {
		return m, nil
	}
	m.resetAgentRuntime()
	m.ui.resumePromptActive = false
	m.agentLoop.toolTimeline = nil
	m.chat.scrollback = 0
	// Offline-mode guardrail. When the user sends what clearly looks like
	// an action ("update X", "write Y", "güncelle", "fix the bug", plus a
	// [[file:]] marker) but the active provider is the offline analyzer,
	// surface the mismatch before they wait on a stream that can't modify
	// anything. The offline analyzer happily reads the file and prints
	// heuristic observations — users reasonably mistake that for "my file
	// got updated and nothing happened". A prepended system message kills
	// the ambiguity without blocking the turn.
	if m.looksLikeActionRequest(question) && !m.hasToolCapableProvider() {
		m = m.appendSystemMessage(
			"⚠ This looks like an action request (write/update/edit), but the active provider is the offline analyzer — it can only summarize files, it cannot modify them. " +
				"Run /provider to pick a tool-capable provider (anthropic, openai, deepseek, kimi, zai, alibaba), then retry with /retry.",
		)
	}
	// Tool-use enforcement for action requests on tool-capable providers.
	// Weaker LLMs (e.g. GLM/Qwen/DeepSeek via openai-compat) routinely
	// respond to "update README.md" by describing the changes in prose
	// instead of calling apply_patch/edit_file/write_file — users then see
	// the file content echoed back and conclude "nothing happened".
	// Prepending an explicit directive dramatically raises the chance the
	// model routes through a real tool call. Strong models (Claude, GPT-4)
	// follow the directive anyway; weak models finally take the hint.
	// Applied only when: intent is clear, provider is tool-capable, and
	// the question doesn't already instruct tool use.
	// In plan mode, inject the opposite directive: investigate only, no
	// mutations. Takes precedence over the enforce-tool-use directive.
	if m.ui.planMode {
		question = strings.TrimRight(question, "\n") +
			"\n\n[DFMC plan mode] You are in INVESTIGATE-ONLY mode. " +
			"Use ONLY read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff, web_fetch, web_search). " +
			"Do NOT call write_file, edit_file, apply_patch, or run_command with destructive arguments. " +
			"Produce a concrete plan as the answer — numbered steps, files to touch, expected diffs — that the user can approve before any files are modified."
	} else {
		question = m.enforceToolUseForActionRequests(question)
	}
	if len(quickActions) > 0 {
		selected := quickActions[clampIndex(m.slashMenu.quickAction, len(quickActions))]
		m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleUser, question))
		m = m.appendSystemMessage("Auto action: " + selected.Reason)
		m = m.startChatToolCommand(selected.Tool, selected.Params)
		return m, runToolCmd(m.ctx, m.eng, selected.Tool, selected.Params)
	}
	if name, params, reason, ok := m.autoToolIntentFromQuestion(question); ok {
		m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleUser, question))
		m = m.appendSystemMessage("Auto action: " + reason)
		m = m.startChatToolCommand(name, params)
		return m, runToolCmd(m.ctx, m.eng, name, params)
	}
	m.chat.transcript = append(m.chat.transcript,
		newChatLine(chatRoleUser, question),
		newChatLine(chatRoleAssistant, ""),
	)
	m.chat.streamIndex = len(m.chat.transcript) - 1
	m.chat.sending = true
	m.chat.streamStartedAt = time.Now()
	m.notice = "Streaming answer... (esc cancels)"
	// Per-stream context so esc can cancel this turn without killing the
	// whole TUI's ctx (which would kill timers and subscriptions too).
	streamCtx, cancel := context.WithCancel(m.ctx)
	m.chat.streamCancel = cancel
	m.chat.streamMessages = startChatStream(streamCtx, m.eng, question)
	return m, tea.Batch(waitForStreamMsg(m.chat.streamMessages), m.ensureSpinnerTick())
}

// ensureSpinnerTick schedules the spinner tick when needed, but only if one
// isn't already in flight. Mutates m.chat.spinnerTicking and returns the cmd (nil
// when no schedule is needed).
func (m *Model) ensureSpinnerTick() tea.Cmd {
	if m.chat.spinnerTicking {
		return nil
	}
	if !m.chat.sending && !m.agentLoop.active {
		return nil
	}
	m.chat.spinnerTicking = true
	return spinnerTickCmd()
}

// drainPendingQueue pops the oldest queued message and submits it as if the
// user had just pressed enter. Called when the current stream finishes so
// follow-up messages flow without the user babysitting the composer.
func (m Model) drainPendingQueue() (Model, tea.Cmd) {
	if len(m.chat.pendingQueue) == 0 {
		return m, nil
	}
	next := m.chat.pendingQueue[0]
	m.chat.pendingQueue = m.chat.pendingQueue[1:]
	m = m.appendSystemMessage(fmt.Sprintf("▸ draining queued message (%d remaining): %s", len(m.chat.pendingQueue), next))
	if expanded, ok := m.expandSlashSelection(next); ok {
		next = expanded
	}
	m.pushInputHistory(next)
	if nextModel, cmd, handled := m.executeChatCommand(next); handled {
		return nextModel.(Model), cmd
	}
	return m.submitChatQuestion(next, nil)
}

// startChatResume kicks off a resumed agent loop from the engine's parked
// state and wires the result into the same streaming path that submitChatQuestion
// uses, so the UI treats it identically.
func (m Model) startChatResume(note string) (Model, tea.Cmd) {
	m.resetAgentRuntime()
	m.ui.resumePromptActive = false
	m.agentLoop.toolTimeline = nil
	m.chat.scrollback = 0
	banner := "Resuming parked agent loop"
	if note != "" {
		banner += " with note: " + note
	}
	m = m.appendSystemMessage(banner + "...")
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleAssistant, ""))
	m.chat.streamIndex = len(m.chat.transcript) - 1
	m.chat.sending = true
	m.chat.streamStartedAt = time.Now()
	m.notice = "Resuming agent loop..."
	m.chat.streamMessages = startChatResumeStream(m.ctx, m.eng, note)
	return m, tea.Batch(waitForStreamMsg(m.chat.streamMessages), m.ensureSpinnerTick())
}

func (m Model) startChatToolCommand(name string, params map[string]any) Model {
	name = strings.TrimSpace(name)
	m.setChatInput("")
	m.chat.toolPending = true
	m.chat.toolName = name
	if params == nil {
		params = map[string]any{}
	}
	m.notice = "Running tool from chat: " + name
	m = m.appendSystemMessage("Running tool: " + name + " " + formatToolParams(params))
	return m
}

func formatToolResultForChat(name string, params map[string]any, res toolruntime.Result, err error) string {
	_ = params
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	header := "Tool result: " + name
	if err != nil {
		text := strings.TrimSpace(err.Error())
		if text == "" {
			text = "unknown error"
		}
		body := ""
		if out := strings.TrimSpace(res.Output); out != "" {
			body = "\n" + truncateCommandBlock(out, 1000)
		}
		return header + " failed: " + text + body
	}
	summary := fmt.Sprintf("%s success (%dms)", header, res.DurationMs)
	warnings := toolResultWarnings(name, res)
	out := strings.TrimSpace(res.Output)
	if out == "" && len(warnings) == 0 {
		return summary
	}
	parts := []string{summary}
	if len(warnings) > 0 {
		parts = append(parts, strings.Join(warnings, "\n"))
	}
	if out != "" {
		parts = append(parts, truncateCommandBlock(out, 1200))
	}
	return strings.Join(parts, "\n")
}
