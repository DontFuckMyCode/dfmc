package engine

// engine_passthrough_conversation.go — thin wrappers around the
// Conversation subsystem plus the RecentConversation projection used
// by the intent layer. Every method nil-checks e.Conversation so the
// engine can run in degraded-storage mode without panicking.
//
// LoadReadOnly vs Load: previewers (TUI Conversations tab) call
// LoadReadOnly so peeking at history doesn't silently swap the active
// chat. Document this distinction at the call site to prevent the
// "click to preview, lose your session" footgun.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) ConversationActive() *conversation.Conversation {
	if e == nil || e.Conversation == nil {
		return nil
	}
	return e.Conversation.Active()
}

func (e *Engine) ConversationSave() error {
	if e == nil || e.Conversation == nil {
		return nil
	}
	return e.Conversation.SaveActive()
}

func (e *Engine) ConversationStart() *conversation.Conversation {
	if e == nil || e.Conversation == nil {
		return nil
	}
	return e.Conversation.Start(e.provider(), e.model())
}

func (e *Engine) ConversationLoad(id string) (*conversation.Conversation, error) {
	if e == nil || e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.Load(id)
}

// ConversationLoadReadOnly returns a conversation without making it the
// active one. Used by preview / inspection surfaces (e.g. the TUI
// Conversations tab) where loading a row to peek must not silently
// swap the user's chat session.
func (e *Engine) ConversationLoadReadOnly(id string) (*conversation.Conversation, error) {
	if e == nil || e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.LoadReadOnly(id)
}

func (e *Engine) ConversationList() ([]conversation.Summary, error) {
	if e == nil || e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.List()
}

func (e *Engine) ConversationSearch(query string, limit int) ([]conversation.Summary, error) {
	if e == nil || e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.Search(query, limit)
}

// RecentConversationContext walks the active conversation backwards and
// extracts a compact view of the most recent activity: the last assistant
// message text (truncated to maxAssistantChars) and the names of up to N
// most recent tool calls. Returns zero values when the conversation is
// empty or unavailable. Cheap (one slice scan); safe to call on every
// user submit. Used by the intent layer to give its classifier just
// enough state to disambiguate "fix it" / "do that for the others".
type RecentConversation struct {
	LastAssistant     string   // truncated to maxAssistantChars runes
	LastAssistantRole string   // empty when no assistant turn exists yet
	RecentToolNames   []string // newest first, capped at maxToolNames
	UserTurnCount     int      // total user turns across the active branch
}

func (e *Engine) RecentConversationContext(maxAssistantChars, maxToolNames int) RecentConversation {
	out := RecentConversation{}
	if e == nil || e.Conversation == nil {
		return out
	}
	active := e.Conversation.Active()
	if active == nil {
		return out
	}
	msgs := active.Messages()
	if maxAssistantChars <= 0 {
		maxAssistantChars = 500
	}
	if maxToolNames <= 0 {
		maxToolNames = 5
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == types.RoleUser {
			out.UserTurnCount++
		}
		if out.LastAssistant == "" && m.Role == types.RoleAssistant {
			out.LastAssistantRole = string(m.Role)
			content := strings.TrimSpace(m.Content)
			if r := []rune(content); len(r) > maxAssistantChars {
				content = string(r[:maxAssistantChars]) + "..."
			}
			out.LastAssistant = content
		}
		if len(out.RecentToolNames) < maxToolNames {
			for _, tc := range m.ToolCalls {
				name := strings.TrimSpace(tc.Name)
				if name == "" {
					continue
				}
				// Unwrap meta wrappers so the intent classifier sees the
				// actual backend tool the agent used. Without this, every
				// entry on the dominant tool-capable-provider path is
				// just "tool_call" / "tool_batch_call" — useless noise.
				if inner := metaInnerNames(name, tc.Params); len(inner) > 0 {
					for _, n := range inner {
						out.RecentToolNames = append(out.RecentToolNames, n)
						if len(out.RecentToolNames) >= maxToolNames {
							break
						}
					}
				} else {
					out.RecentToolNames = append(out.RecentToolNames, name)
				}
				if len(out.RecentToolNames) >= maxToolNames {
					break
				}
			}
		}
	}
	return out
}

func (e *Engine) ConversationBranchCreate(name string) error {
	if e == nil || e.Conversation == nil {
		return fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchCreate(name)
}

func (e *Engine) ConversationBranchSwitch(name string) error {
	if e == nil || e.Conversation == nil {
		return fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchSwitch(name)
}

func (e *Engine) ConversationBranchList() []string {
	if e == nil || e.Conversation == nil {
		return nil
	}
	return e.Conversation.BranchList()
}

func (e *Engine) ConversationBranchCompare(a, b string) (conversation.BranchComparison, error) {
	if e == nil || e.Conversation == nil {
		return conversation.BranchComparison{}, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchCompare(a, b)
}

func (e *Engine) ConversationUndoLast() (int, error) {
	if e == nil || e.Conversation == nil {
		return 0, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.UndoLast()
}
