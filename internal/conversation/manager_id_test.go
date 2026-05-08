package conversation

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestAddMessage_AssignsIDWhenMissing(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleUser, Content: "first", Timestamp: time.Now(),
	})
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleAssistant, Content: "reply", Timestamp: time.Now(),
	})
	msgs := mgr.Active().Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if msgs[0].ID == "" || msgs[1].ID == "" {
		t.Fatal("AddMessage must auto-assign an ID when missing")
	}
	if !strings.HasPrefix(msgs[0].ID, "u-") {
		t.Errorf("user ID should start with u-: %q", msgs[0].ID)
	}
	if !strings.HasPrefix(msgs[1].ID, "a-") {
		t.Errorf("assistant ID should start with a-: %q", msgs[1].ID)
	}
	if msgs[0].ID == msgs[1].ID {
		t.Error("each message must have a distinct ID")
	}
}

func TestAddMessage_PreservesExistingID(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.AddMessage("offline", "offline-v1", types.Message{
		ID: "u-fixed1", Role: types.RoleUser, Content: "with id",
	})
	msgs := mgr.Active().Messages()
	if msgs[0].ID != "u-fixed1" {
		t.Errorf("ID was overwritten: got %q", msgs[0].ID)
	}
}

func TestRemoveMessagesByID_DropsMatching(t *testing.T) {
	mgr := New(openConvStore(t))
	for _, c := range []struct {
		id, content string
		role        types.MessageRole
	}{
		{"u-1", "first", types.RoleUser},
		{"a-1", "reply", types.RoleAssistant},
		{"u-2", "second", types.RoleUser},
		{"a-2", "reply2", types.RoleAssistant},
	} {
		mgr.AddMessage("offline", "offline-v1", types.Message{
			ID: c.id, Role: c.role, Content: c.content,
		})
	}

	dropped := mgr.RemoveMessagesByID([]string{"u-1", "a-1"})
	if dropped != 2 {
		t.Fatalf("expected 2 dropped, got %d", dropped)
	}
	msgs := mgr.Active().Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(msgs))
	}
	if msgs[0].ID != "u-2" || msgs[1].ID != "a-2" {
		t.Errorf("wrong messages survived: %v / %v", msgs[0].ID, msgs[1].ID)
	}
}

func TestRemoveMessagesByID_UnknownIDsAreNoOp(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.AddMessage("offline", "offline-v1", types.Message{
		ID: "u-1", Role: types.RoleUser, Content: "hi",
	})
	if got := mgr.RemoveMessagesByID([]string{"u-does-not-exist", ""}); got != 0 {
		t.Errorf("unknown / empty IDs should drop nothing, got %d", got)
	}
	if len(mgr.Active().Messages()) != 1 {
		t.Error("RemoveMessagesByID with unknown IDs must not touch the log")
	}
}

func TestAssignMessageID_RolePrefix(t *testing.T) {
	cases := map[types.MessageRole]string{
		types.RoleUser:      "u-",
		types.RoleAssistant: "a-",
		types.RoleSystem:    "s-",
		types.RoleTool:      "t-",
	}
	for role, prefix := range cases {
		got := AssignMessageID(role)
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("role %s: got %q want prefix %q", role, got, prefix)
		}
		if len(got) != len(prefix)+6 {
			t.Errorf("role %s: ID length %d != %d", role, len(got), len(prefix)+6)
		}
	}
}
