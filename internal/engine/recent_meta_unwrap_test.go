package engine

import (
	"reflect"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestRecentConversationContext_UnwrapsMetaToolCalls pins the behavior
// that feeds the intent classifier a meaningful RECENT_TOOLS list. On
// tool-capable providers the assistant turns record the meta wrapper
// name (tool_call / tool_batch_call) in msg.ToolCalls; without an
// unwrap pass the classifier sees only those two strings repeated and
// has no way to tell whether the agent was editing, searching, or
// running shell commands. The unwrap is what makes "fix it" / "do that
// for the others" disambiguatable.
func TestRecentConversationContext_UnwrapsMetaToolCalls(t *testing.T) {
	eng := &Engine{Conversation: conversation.New(nil)}

	now := time.Now()
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleUser,
		Content:   "audit the repo",
		Timestamp: now,
	})
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleAssistant,
		Content:   "scanning",
		Timestamp: now,
		ToolCalls: []types.ToolCallRecord{
			{
				Name: "tool_batch_call",
				Params: map[string]any{
					"calls": []any{
						map[string]any{"name": "grep_codebase", "args": map[string]any{"pattern": "TODO"}},
						map[string]any{"name": "read_file", "args": map[string]any{"path": "main.go"}},
					},
				},
			},
			{
				Name: "tool_call",
				Params: map[string]any{
					"name": "edit_file",
					"args": map[string]any{"path": "main.go", "old_string": "a", "new_string": "b"},
				},
			},
		},
	})

	got := eng.RecentConversationContext(500, 5)
	want := []string{"grep_codebase", "read_file", "edit_file"}
	if !reflect.DeepEqual(got.RecentToolNames, want) {
		t.Fatalf("RecentToolNames = %#v, want %#v", got.RecentToolNames, want)
	}
}

// TestRecentConversationContext_RegularToolsPassThrough makes sure the
// unwrap path doesn't silently drop non-meta tool calls (e.g. the
// offline/non-bridged dispatch path, or plugin tools that bypass the
// meta layer entirely).
func TestRecentConversationContext_RegularToolsPassThrough(t *testing.T) {
	eng := &Engine{Conversation: conversation.New(nil)}

	now := time.Now()
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleUser,
		Content:   "hello",
		Timestamp: now,
	})
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleAssistant,
		Content:   "doing it",
		Timestamp: now,
		ToolCalls: []types.ToolCallRecord{
			{Name: "run_command", Params: map[string]any{"command": "go test"}},
		},
	})

	got := eng.RecentConversationContext(500, 5)
	want := []string{"run_command"}
	if !reflect.DeepEqual(got.RecentToolNames, want) {
		t.Fatalf("RecentToolNames = %#v, want %#v", got.RecentToolNames, want)
	}
}

// TestRecentConversationContext_RespectsCapAcrossBatch verifies the
// maxToolNames cap still trips mid-batch when a single tool_batch_call
// would otherwise expand to more inner names than the caller asked for.
func TestRecentConversationContext_RespectsCapAcrossBatch(t *testing.T) {
	eng := &Engine{Conversation: conversation.New(nil)}

	now := time.Now()
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleAssistant,
		Content:   "scanning",
		Timestamp: now,
		ToolCalls: []types.ToolCallRecord{
			{
				Name: "tool_batch_call",
				Params: map[string]any{
					"calls": []any{
						map[string]any{"name": "read_file"},
						map[string]any{"name": "grep_codebase"},
						map[string]any{"name": "write_file"},
						map[string]any{"name": "edit_file"},
					},
				},
			},
		},
	})

	got := eng.RecentConversationContext(500, 2)
	if len(got.RecentToolNames) != 2 {
		t.Fatalf("expected cap=2 honoured across batch, got %#v", got.RecentToolNames)
	}
	want := []string{"read_file", "grep_codebase"}
	if !reflect.DeepEqual(got.RecentToolNames, want) {
		t.Fatalf("RecentToolNames = %#v, want %#v", got.RecentToolNames, want)
	}
}
