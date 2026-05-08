package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// contextTestModel builds a Model with a fully wired conversation
// manager so /context messages and /context drop have something to
// observe + mutate. We seed a small, stable dialogue with explicit IDs
// so assertions don't depend on AssignMessageID's randomness.
func contextTestModel(t *testing.T) (Model, *conversation.Manager) {
	t.Helper()
	mgr := conversation.New(nil)
	mgr.AddMessage("offline", "offline-v1", types.Message{
		ID: "u-aaaa11", Role: types.RoleUser, Content: "first question",
	})
	mgr.AddMessage("offline", "offline-v1", types.Message{
		ID:        "a-bbbb22",
		Role:      types.RoleAssistant,
		Content:   "first answer",
		ToolCalls: []types.ToolCallRecord{{Name: "read_file"}},
	})
	mgr.AddMessage("offline", "offline-v1", types.Message{
		ID: "u-cccc33", Role: types.RoleUser, Content: "follow-up",
	})

	cfg := config.DefaultConfig()
	eng := &engine.Engine{
		Config:       cfg,
		ProjectRoot:  t.TempDir(),
		EventBus:     engine.NewEventBus(),
		Conversation: mgr,
	}
	m := NewModel(context.Background(), eng)
	return m, mgr
}

func TestContextMessagesTable_RendersAllMessages(t *testing.T) {
	m, _ := contextTestModel(t)
	out := m.contextCommandMessagesTable()
	for _, want := range []string{"u-aaaa11", "a-bbbb22", "u-cccc33", "user", "assistant", "first question", "first answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("messages table missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "×1") {
		t.Errorf("expected tool-call count chip ×1 for the assistant row, got:\n%s", out)
	}
}

func TestContextMessagesTable_HandlesEmptyConversation(t *testing.T) {
	cfg := config.DefaultConfig()
	mgr := conversation.New(nil)
	eng := &engine.Engine{
		Config:       cfg,
		ProjectRoot:  t.TempDir(),
		EventBus:     engine.NewEventBus(),
		Conversation: mgr,
	}
	m := NewModel(context.Background(), eng)
	out := m.contextCommandMessagesTable()
	if !strings.Contains(out, "no active conversation") {
		t.Errorf("empty manager: expected 'no active conversation', got %q", out)
	}
}

func TestRunContextDropCommand_RemovesByID(t *testing.T) {
	m, mgr := contextTestModel(t)
	if _, _, handled := m.runContextDropCommand([]string{"u-aaaa11", "a-bbbb22"}); !handled {
		t.Fatalf("expected /context drop to be handled")
	}
	msgs := mgr.Active().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after drop, got %d", len(msgs))
	}
	if msgs[0].ID != "u-cccc33" {
		t.Errorf("wrong message survived: %q", msgs[0].ID)
	}
}

func TestRunContextDropCommand_AcceptsCommaSeparatedIDs(t *testing.T) {
	m, mgr := contextTestModel(t)
	if _, _, handled := m.runContextDropCommand([]string{"u-aaaa11,a-bbbb22"}); !handled {
		t.Fatalf("expected /context drop to be handled")
	}
	if got := len(mgr.Active().Messages()); got != 1 {
		t.Errorf("comma-separated IDs not parsed: %d msgs left", got)
	}
}

func TestRunContextDropCommand_NoArgsShowsUsage(t *testing.T) {
	m, mgr := contextTestModel(t)
	if _, _, handled := m.runContextDropCommand([]string{}); !handled {
		t.Fatalf("expected /context drop with no args to be handled (usage)")
	}
	if got := len(mgr.Active().Messages()); got != 3 {
		t.Errorf("usage path must not mutate; got %d messages", got)
	}
}

func TestRunContextDropCommand_UnknownIDsLeaveLogIntact(t *testing.T) {
	m, mgr := contextTestModel(t)
	if _, _, handled := m.runContextDropCommand([]string{"u-doesnotexist"}); !handled {
		t.Fatalf("expected /context drop to be handled")
	}
	if got := len(mgr.Active().Messages()); got != 3 {
		t.Errorf("unknown ID must be a no-op, got %d messages", got)
	}
}

func TestContextMessagePreview_TrimsAndEscapesNewlines(t *testing.T) {
	preview := contextMessagePreview(types.Message{Content: "line one\nline two\nline three"})
	if strings.Contains(preview, "\n") {
		t.Errorf("preview must escape newlines, got %q", preview)
	}
	if !strings.Contains(preview, "⏎") {
		t.Errorf("preview should mark newlines with ⏎, got %q", preview)
	}
}

func TestContextMessagePreview_FallsBackToToolWork(t *testing.T) {
	got := contextMessagePreview(types.Message{
		ToolCalls: []types.ToolCallRecord{{Name: "read_file"}, {Name: "edit_file"}},
	})
	if !strings.Contains(got, "tool work") {
		t.Errorf("empty content with tool calls should fall back to tool-work label, got %q", got)
	}
	if !strings.Contains(got, "read_file") || !strings.Contains(got, "edit_file") {
		t.Errorf("tool-work preview should list call names, got %q", got)
	}
}
