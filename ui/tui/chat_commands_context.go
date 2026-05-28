package tui

// chat_commands_context.go — `/context messages` and `/context drop`
// implementations. The user explicitly asked for context surgery:
// "context window neden tool calling veya arada asssistant in mesajı
// dışında yapılan iş summary si vs içermiyor". The messages table
// makes the rolling history TANGIBLE — every message gets an ID, role,
// rough token cost, has-tools flag, and a content preview — so the
// user can SEE what the LLM is paying to carry round-to-round and
// surgically prune the noise.
//
// /context messages          — print the table (newest first by index)
// /context drop <id> [<id>…] — drop one or more message IDs from the
//                              active branch (alias: remove, rm). IDs
//                              come from the messages table.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// contextCommandMessagesTable renders one row per message in the
// active branch: index · ID · role · ~tokens · tools? · content preview.
// Designed to fit a typical 100-col TUI; long content gets truncated
// to 60 runes with an ellipsis so the table stays one-row-per-message.
func (m Model) contextCommandMessagesTable() string {
	if m.eng == nil || m.eng.ConversationActive() == nil {
		return "/context messages: no active conversation."
	}
	msgs := m.eng.ConversationActive().Messages()
	if len(msgs) == 0 {
		return "/context messages: active conversation has no messages yet."
	}
	var b strings.Builder
	totalTokens := 0
	totalToolCalls := 0
	for _, msg := range msgs {
		totalTokens += contextMessageTokens(msg)
		totalToolCalls += len(msg.ToolCalls)
	}
	fmt.Fprintf(&b,
		"Active branch: %d messages · ~%d tokens (rough estimate) · %d tool call(s) total\n",
		len(msgs), totalTokens, totalToolCalls,
	)
	fmt.Fprintf(&b, "  %-3s  %-12s  %-9s  %-7s  %-5s  %s\n",
		"#", "ID", "ROLE", "~TOKENS", "TOOLS", "PREVIEW")
	b.WriteString("  ")
	b.WriteString(strings.Repeat("─", 96))
	b.WriteByte('\n')
	for i, msg := range msgs {
		id := strings.TrimSpace(msg.ID)
		if id == "" {
			id = "(unset)"
		}
		role := contextRoleAbbrev(msg.Role)
		tokenCount := contextMessageTokens(msg)
		toolsCell := "-"
		if n := len(msg.ToolCalls); n > 0 {
			toolsCell = fmt.Sprintf("×%d", n)
		}
		preview := contextMessagePreview(msg)
		fmt.Fprintf(&b, "  %-3d  %-12s  %-9s  %-7d  %-5s  %s\n",
			i+1, id, role, tokenCount, toolsCell, preview,
		)
	}
	b.WriteString("\n/context drop <id> [<id>…] removes messages by ID. The model's [cleanup:] tail does this automatically — use /context drop only when you want to override.")
	return b.String()
}

// runContextDropCommand removes one or more message IDs from the
// active branch. IDs are matched case-insensitively (matching the
// AssignMessageID format `<role>-<hex>` in lower-case). Reports how
// many messages were actually dropped — useful when an ID was
// already cleaned up by a previous turn.
func (m Model) runContextDropCommand(args []string) (tea.Model, tea.Cmd, bool) {
	if m.eng == nil {
		return m.appendSystemMessage("/context drop: engine unavailable."), nil, true
	}
	conv := m.eng.ConversationActive()
	if conv == nil {
		return m.appendSystemMessage("/context drop: no active conversation."), nil, true
	}
	if len(args) == 0 {
		return m.appendSystemMessage("Usage: /context drop <id> [<id>…]\nList IDs first with /context messages."), nil, true
	}
	ids := []string{}
	for _, raw := range args {
		for _, tok := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == ' '
		}) {
			tok = strings.TrimSpace(tok)
			if tok != "" {
				ids = append(ids, tok)
			}
		}
	}
	if len(ids) == 0 {
		return m.appendSystemMessage("/context drop: no valid IDs in arguments."), nil, true
	}
	if m.eng.Conversation == nil {
		return m.appendSystemMessage("/context drop: conversation manager unavailable (engine partially initialised?)"), nil, true
	}
	dropped := m.eng.Conversation.RemoveMessagesByID(ids)
	m.notice = fmt.Sprintf("/context drop: %d/%d removed", dropped, len(ids))
	if dropped == 0 {
		return m.appendSystemMessage(fmt.Sprintf(
			"/context drop: nothing dropped — none of [%s] matched a current message ID. Use /context messages to see live IDs.",
			strings.Join(ids, ", "),
		)), nil, true
	}
	return m.appendSystemMessage(fmt.Sprintf(
		"/context drop: removed %d/%d message(s). Active branch trimmed.",
		dropped, len(ids),
	)), nil, true
}

func contextRoleAbbrev(role types.MessageRole) string {
	switch role {
	case types.RoleUser:
		return "user"
	case types.RoleAssistant:
		return "assistant"
	case types.RoleSystem:
		return "system"
	case types.RoleTool:
		return "tool"
	}
	r := strings.TrimSpace(string(role))
	if r == "" {
		return "?"
	}
	return r
}

// contextMessageTokens returns the persisted TokenCnt when the
// recorder set one, else falls back to a tokens.Estimate of the
// content. Tool calls/results live in dedicated record arrays on the
// message, so we add a tiny per-call surcharge to reflect the JSON
// they cost when serialised back to the LLM.
func contextMessageTokens(msg types.Message) int {
	if msg.TokenCnt > 0 {
		return msg.TokenCnt
	}
	count := tokens.Estimate(msg.Content)
	for _, call := range msg.ToolCalls {
		// 24 ≈ JSON envelope overhead per call shape; cheap enough to
		// avoid Estimate for every tool param map but useful so a
		// turn full of tool work doesn't read as zero tokens.
		count += 24
		count += tokens.Estimate(call.Name)
	}
	for _, res := range msg.Results {
		count += tokens.Estimate(res.Output)
	}
	return count
}

func contextMessagePreview(msg types.Message) string {
	content := strings.TrimSpace(msg.Content)
	content = strings.ReplaceAll(content, "\n", " ⏎ ")
	if content == "" {
		if n := len(msg.ToolCalls); n > 0 {
			names := []string{}
			for _, call := range msg.ToolCalls {
				names = append(names, call.Name)
			}
			return "(tool work: " + strings.Join(names, ", ") + ")"
		}
		return "(empty)"
	}
	return truncateForLine(content, 60)
}
