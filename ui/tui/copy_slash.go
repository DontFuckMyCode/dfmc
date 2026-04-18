// /copy slash command — copy a specific transcript response to the
// system clipboard via OSC 52 (see clipboard.go).
//
// Shapes:
//   /copy               → last assistant response
//   /copy last          → same as above (explicit)
//   /copy N             → Nth assistant response (1-based, matches the
//                         #N chip rendered in each assistant header)
//   /copy -N            → Nth-from-last assistant response
//   /copy code [N]      → last (or Nth-from-last) fenced code block
//                         found walking the transcript backward
//   /copy all           → every assistant response joined with blank
//                         lines — useful for pasting a whole session
//
// The copy goes through tea.Printf → OSC 52 → terminal. There's no
// acknowledgement so the notice is based on what we *sent*, not what
// the terminal accepted. Users on terminals that block OSC 52 will
// see the notice but nothing in the clipboard — we document that in
// the notice text.

package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleCopySlash(args []string) (tea.Model, tea.Cmd, bool) {
	// Defaults to "last assistant response" when no args were passed.
	if len(args) == 0 {
		return m.copyAssistantResponseAt(-1)
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "last":
		return m.copyAssistantResponseAt(-1)
	case "all":
		return m.copyAllAssistantResponses()
	case "code":
		which := -1
		if len(args) > 1 {
			if n, err := strconv.Atoi(strings.TrimSpace(args[1])); err == nil {
				which = n
			}
		}
		return m.copyLastCodeBlock(which)
	}
	// Numeric index: positive = 1-based, negative = from-last.
	if n, err := strconv.Atoi(sub); err == nil {
		return m.copyAssistantResponseAt(n)
	}
	m.notice = "Usage: /copy [N | last | code [N] | all]"
	return m.appendSystemMessage("Usage: /copy [N | last | code [N] | all]. Each assistant response shows a `#N` chip; pass that integer to `/copy N`."), nil, true
}

// assistantIndices returns the transcript positions of every
// assistant response, in order. Used to translate a 1-based (or
// negative) argument into a concrete slot.
func (m Model) assistantIndices() []int {
	out := make([]int, 0, len(m.chat.transcript))
	for i, item := range m.chat.transcript {
		if item.Role.Eq(chatRoleAssistant) {
			out = append(out, i)
		}
	}
	return out
}

// copyAssistantResponseAt copies the Nth assistant response to the
// clipboard. Positive N is 1-based ("copy response #3"); -1 is the
// most recent response, -2 the one before it. Out-of-range values
// surface a clean error message rather than silently picking a
// neighbour — an agent told "copy 99" should hear "there is no 99".
func (m Model) copyAssistantResponseAt(n int) (tea.Model, tea.Cmd, bool) {
	idxs := m.assistantIndices()
	if len(idxs) == 0 {
		m.notice = "Nothing to copy — no assistant responses yet."
		return m, nil, true
	}
	slot := -1
	switch {
	case n == 0:
		slot = idxs[len(idxs)-1]
	case n > 0:
		if n > len(idxs) {
			m.notice = fmt.Sprintf("/copy %d — only %d assistant response(s) exist.", n, len(idxs))
			return m, nil, true
		}
		slot = idxs[n-1]
	default: // n < 0
		from := len(idxs) + n
		if from < 0 {
			m.notice = fmt.Sprintf("/copy %d — only %d assistant response(s) exist.", n, len(idxs))
			return m, nil, true
		}
		slot = idxs[from]
	}

	content := strings.TrimSpace(m.chat.transcript[slot].Content)
	if content == "" {
		m.notice = "Selected response is empty."
		return m, nil, true
	}
	cmd, res := copyToClipboardCmd(content)
	label := "#?"
	for i, idx := range idxs {
		if idx == slot {
			label = fmt.Sprintf("#%d", i+1)
			break
		}
	}
	m.notice = copyNotice("response "+label, res)
	return m, cmd, true
}

// copyAllAssistantResponses stitches every assistant message in order
// and ships the blob to the clipboard. Useful for "give me the whole
// conversation" hand-offs. Tool-result / user lines are skipped —
// the user asked for responses, not the round-trip.
func (m Model) copyAllAssistantResponses() (tea.Model, tea.Cmd, bool) {
	idxs := m.assistantIndices()
	if len(idxs) == 0 {
		m.notice = "Nothing to copy — no assistant responses yet."
		return m, nil, true
	}
	parts := make([]string, 0, len(idxs))
	for _, idx := range idxs {
		c := strings.TrimSpace(m.chat.transcript[idx].Content)
		if c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		m.notice = "All assistant responses are empty."
		return m, nil, true
	}
	joined := strings.Join(parts, "\n\n---\n\n")
	cmd, res := copyToClipboardCmd(joined)
	m.notice = copyNotice(fmt.Sprintf("%d response(s)", len(parts)), res)
	return m, cmd, true
}

// copyLastCodeBlock walks the transcript backward looking for fenced
// ``` code blocks in assistant messages. Which=-1 returns the most
// recent block; -N steps further back. Positive N is 1-based from
// the END of the transcript (since users almost always mean "the
// latest code thing"). Returns a clean error when nothing matches.
func (m Model) copyLastCodeBlock(which int) (tea.Model, tea.Cmd, bool) {
	idxs := m.assistantIndices()
	if len(idxs) == 0 {
		m.notice = "No assistant responses — no code to copy."
		return m, nil, true
	}
	// Gather blocks newest-first so the index math is easy.
	type block struct {
		text        string
		respLabel   string
		blockInResp int
	}
	var blocks []block
	for i := len(idxs) - 1; i >= 0; i-- {
		content := m.chat.transcript[idxs[i]].Content
		found := extractFencedBlocks(content)
		label := fmt.Sprintf("#%d", i+1)
		for bi, b := range found {
			blocks = append(blocks, block{text: b, respLabel: label, blockInResp: bi + 1})
		}
	}
	if len(blocks) == 0 {
		m.notice = "No fenced code blocks found in any assistant response."
		return m, nil, true
	}
	slot := 0
	if which > 0 {
		// 1-based from the newest block.
		if which > len(blocks) {
			m.notice = fmt.Sprintf("/copy code %d — only %d block(s) exist.", which, len(blocks))
			return m, nil, true
		}
		slot = which - 1
	} else if which < -1 {
		// -2 means "one before latest", -3 "two before", etc.
		slot = (-which) - 1
		if slot >= len(blocks) {
			m.notice = fmt.Sprintf("/copy code %d — only %d block(s) exist.", which, len(blocks))
			return m, nil, true
		}
	}
	chosen := blocks[slot]
	cmd, res := copyToClipboardCmd(chosen.text)
	m.notice = copyNotice(fmt.Sprintf("code block %d of %s", chosen.blockInResp, chosen.respLabel), res)
	return m, cmd, true
}

// extractFencedBlocks returns the content of every ``` fenced block
// in the given text, in appearance order. The language tag on the
// opening fence is stripped; only the body is returned so pasting
// lands executable source into the user's editor.
func extractFencedBlocks(text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	var cur strings.Builder
	inside := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inside {
				out = append(out, strings.TrimRight(cur.String(), "\n"))
				cur.Reset()
				inside = false
			} else {
				inside = true
			}
			continue
		}
		if inside {
			cur.WriteString(line)
			cur.WriteString("\n")
		}
	}
	// Trailing unclosed fence — include what we captured; better than
	// losing the payload when the assistant's message was cut off.
	if inside && cur.Len() > 0 {
		out = append(out, strings.TrimRight(cur.String(), "\n"))
	}
	return out
}

// copyNotice formats the user-facing notice line. Truncation and
// terminal-support caveats go in here so the copy slash keeps a
// uniform voice.
func copyNotice(target string, res clipboardResult) string {
	if res.Err != nil {
		return "Copy failed: " + res.Err.Error()
	}
	if res.Bytes == 0 {
		return "Nothing to copy — " + target + " was empty."
	}
	msg := fmt.Sprintf("Copied %s to clipboard (%d bytes).", target, res.Bytes)
	if res.Truncated {
		msg += " Payload truncated to " + strconv.Itoa(clipboardMaxBytes) + " bytes — your terminal may limit OSC 52 further."
	}
	return msg
}
