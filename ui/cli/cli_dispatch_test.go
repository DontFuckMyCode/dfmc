package cli

import (
	"context"
	"strings"
	"testing"
)

func TestDispatchCommandHelpAliases(t *testing.T) {
	for _, cmd := range []string{"help", "-h", "--help"} {
		out := captureStdout(t, func() {
			code, ok := dispatchCommand(context.Background(), nil, cmd, nil, globalOptions{}, "test")
			if !ok {
				t.Fatalf("%s was not dispatched", cmd)
			}
			if code != 0 {
				t.Fatalf("%s exit=%d, want 0", cmd, code)
			}
		})
		if !strings.Contains(out, "Usage:") {
			t.Fatalf("%s help output missing Usage: %q", cmd, out)
		}
	}
}

func TestDispatchCommandKnownAliases(t *testing.T) {
	for _, cmd := range []string{"conv", "agent", "approve", "permissions"} {
		if _, ok := commandHandlerRegistry()[cmd]; !ok {
			t.Fatalf("commandHandlerRegistry missing alias %q", cmd)
		}
	}
}

func TestDispatchCommandSkillShortcuts(t *testing.T) {
	for _, cmd := range skillShortcutCommands {
		if !isSkillShortcut(cmd) {
			t.Fatalf("%q should be a skill shortcut", cmd)
		}
	}
	if isSkillShortcut("ask") {
		t.Fatal("ask must remain a regular command, not a skill shortcut")
	}
}

func TestCommandNamesDerivedFromDispatcher(t *testing.T) {
	names := map[string]struct{}{}
	for _, name := range commandNames() {
		names[name] = struct{}{}
		if strings.HasPrefix(name, "-") {
			t.Fatalf("commandNames should not expose flag alias %q", name)
		}
	}

	for name := range commandHandlerRegistry() {
		if strings.HasPrefix(name, "-") {
			continue
		}
		if _, ok := names[name]; !ok {
			t.Fatalf("commandNames missing dispatcher command %q", name)
		}
	}
	for _, name := range skillShortcutCommands {
		if _, ok := names[name]; !ok {
			t.Fatalf("commandNames missing skill shortcut %q", name)
		}
	}
}

func TestDispatchCommandUnknown(t *testing.T) {
	if _, ok := dispatchCommand(context.Background(), nil, "not-a-command", nil, globalOptions{}, "test"); ok {
		t.Fatal("unknown command should not dispatch")
	}
}
