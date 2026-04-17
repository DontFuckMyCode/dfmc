package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

// newHooksTestModel wires a Model to an engine that carries a specific
// Hooks dispatcher, skipping the real Init path (which would also create
// bbolt files, load providers, etc.). /hooks is a read-only view over
// the dispatcher, so that shortcut is safe.
func newHooksTestModel(t *testing.T, entries map[string][]config.HookEntry) Model {
	t.Helper()
	cfg := config.DefaultConfig()
	eng := &engine.Engine{
		Config: cfg,
		Hooks:  hooks.New(config.HooksConfig{Entries: entries}, nil),
	}
	return NewModel(context.Background(), eng)
}

func TestSlashHooks_NoneRegisteredShowsGuidance(t *testing.T) {
	m := newHooksTestModel(t, nil)
	next, _, handled := m.executeChatCommand("/hooks")
	if !handled {
		t.Fatalf("/hooks must be handled")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(last, "none registered") {
		t.Fatalf("empty dispatcher should say 'none registered', got:\n%s", last)
	}
	if !strings.Contains(last, "hooks:") {
		t.Fatalf("guidance should mention the `hooks:` config key, got:\n%s", last)
	}
}

func TestSlashHooks_ListsEventsAndEntries(t *testing.T) {
	m := newHooksTestModel(t, map[string][]config.HookEntry{
		"pre_tool": {
			{Name: "gate-apply", Condition: "tool_name == apply_patch", Command: "echo approved"},
			{Name: "log-all", Command: "echo all"},
		},
		"session_start": {
			{Name: "banner", Command: "echo hi"},
		},
	})
	next, _, _ := m.executeChatCommand("/hooks")
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content

	if !strings.Contains(last, "pre_tool (2)") {
		t.Fatalf("should summarise pre_tool count, got:\n%s", last)
	}
	if !strings.Contains(last, "session_start (1)") {
		t.Fatalf("should summarise session_start count, got:\n%s", last)
	}
	for _, needle := range []string{"gate-apply", "log-all", "banner"} {
		if !strings.Contains(last, needle) {
			t.Fatalf("should list hook name %q, got:\n%s", needle, last)
		}
	}
	// The conditional hook should carry its cond annotation; the plain
	// one must NOT (otherwise noise drowns the common case).
	if !strings.Contains(last, "cond: tool_name == apply_patch") {
		t.Fatalf("conditional hook should show its condition, got:\n%s", last)
	}
	if strings.Contains(last, "[cond:") && strings.Contains(last, "log-all [cond:") {
		t.Fatalf("unconditional hook should not print [cond:], got:\n%s", last)
	}
}
