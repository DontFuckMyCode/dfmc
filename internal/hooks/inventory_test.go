package hooks

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestInventory_NilDispatcher(t *testing.T) {
	var d *Dispatcher
	if got := d.Inventory(); got != nil {
		t.Fatalf("nil dispatcher Inventory = %v, want nil", got)
	}
}

func TestInventory_EmptyConfig(t *testing.T) {
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{}}, nil)
	if got := d.Inventory(); got != nil {
		t.Fatalf("empty config Inventory = %v, want nil", got)
	}
}

func TestInventory_ReturnsNamesAndConditions(t *testing.T) {
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {
			{Name: "gate-apply", Condition: "tool_name == apply_patch", Command: "echo gate"},
			{Name: "log-all", Command: "echo all"},
		},
		"session_end": {
			{Name: "farewell", Command: "echo bye"},
		},
	}}, nil)

	inv := d.Inventory()
	if len(inv) != 2 {
		t.Fatalf("expected 2 events in inventory, got %d: %+v", len(inv), inv)
	}
	preTool := inv[EventPreTool]
	if len(preTool) != 2 {
		t.Fatalf("expected 2 pre_tool entries, got %d", len(preTool))
	}
	if preTool[0].Name != "gate-apply" || preTool[0].Condition != "tool_name == apply_patch" {
		t.Fatalf("first entry mismatch: %+v", preTool[0])
	}
	if preTool[1].Name != "log-all" || preTool[1].Condition != "" {
		t.Fatalf("second entry mismatch: %+v", preTool[1])
	}
	sessionEnd := inv[EventSessionEnd]
	if len(sessionEnd) != 1 || sessionEnd[0].Name != "farewell" {
		t.Fatalf("session_end inventory wrong: %+v", sessionEnd)
	}
}
