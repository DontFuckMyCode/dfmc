package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

// newDoctorTestModel returns a Model over an unitialized engine so /doctor
// can run without bbolt/providers/etc. describeHealth is a read-only
// snapshot so the shortcut is safe.
func newDoctorTestModel(t *testing.T, mutate func(*engine.Engine)) Model {
	t.Helper()
	cfg := config.DefaultConfig()
	eng := &engine.Engine{Config: cfg}
	if mutate != nil {
		mutate(eng)
	}
	return NewModel(context.Background(), eng)
}

func TestSlashDoctor_RunsAndIncludesSignature(t *testing.T) {
	m := newDoctorTestModel(t, nil)
	next, _, handled := m.executeChatCommand("/doctor")
	if !handled {
		t.Fatalf("/doctor must be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "health snapshot") {
		t.Fatalf("should carry header 'health snapshot', got:\n%s", last)
	}
	for _, needle := range []string{"provider:", "ast:", "tools:", "gate:", "hooks:"} {
		if !strings.Contains(last, needle) {
			t.Fatalf("doctor output missing field %q, got:\n%s", needle, last)
		}
	}
}

func TestSlashDoctor_HealthAlias(t *testing.T) {
	m := newDoctorTestModel(t, nil)
	next, _, handled := m.executeChatCommand("/health")
	if !handled {
		t.Fatalf("/health must be handled as alias")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "health snapshot") {
		t.Fatalf("/health alias should show the same snapshot, got:\n%s", last)
	}
}

func TestSlashDoctor_WarnsWhenProviderMisconfigured(t *testing.T) {
	m := newDoctorTestModel(t, nil)
	// status has empty Provider — the "no provider selected" branch.
	next, _, _ := m.executeChatCommand("/doctor")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "no provider") {
		t.Fatalf("missing-provider path should flag it, got:\n%s", last)
	}
}

func TestSlashDoctor_WarnsOnRegexAST(t *testing.T) {
	m := newDoctorTestModel(t, nil)
	m.status.ASTBackend = "regex"
	next, _, _ := m.executeChatCommand("/doctor")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "regex fallback") {
		t.Fatalf("regex backend should get a warning, got:\n%s", last)
	}
}

func TestSlashDoctor_ShowsGateOnWhenConfigured(t *testing.T) {
	m := newDoctorTestModel(t, func(eng *engine.Engine) {
		eng.Config.Tools.RequireApproval = []string{"write_file", "run_command"}
	})
	next, _, _ := m.executeChatCommand("/doctor")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "gate:     ON") {
		t.Fatalf("gate should report ON with 2 tools, got:\n%s", last)
	}
	if !strings.Contains(last, "2 tool") {
		t.Fatalf("should mention 2 tools gated, got:\n%s", last)
	}
}

func TestSlashDoctor_CountsRegisteredHooks(t *testing.T) {
	m := newDoctorTestModel(t, func(eng *engine.Engine) {
		eng.Hooks = hooks.New(config.HooksConfig{Entries: map[string][]config.HookEntry{
			"pre_tool": {
				{Name: "h1", Command: "echo a"},
				{Name: "h2", Command: "echo b"},
			},
			"session_start": {
				{Name: "boot", Command: "echo hi"},
			},
		}}, nil)
	})
	next, _, _ := m.executeChatCommand("/doctor")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "3 registered") {
		t.Fatalf("should count 3 hooks total, got:\n%s", last)
	}
}
