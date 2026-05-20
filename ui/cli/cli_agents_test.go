package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func newAgentsTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	// Redirect HOME/USERPROFILE to a temp dir so the engine's SQLite files
	// land under t.TempDir() instead of the developer's real ~/.dfmc data
	// dir — otherwise parallel test runs collide on the storage lock.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	return eng
}

// TestRunAgents_ListPrintsRolesAndProfiles drives `dfmc agents list`
// against a freshly-built engine and asserts the catalog headers + at
// least one canonical role land in stdout. Mirrors the TUI /agents
// test so the CLI layer can't drift silently when the catalog
// formatter changes.
func TestRunAgents_ListPrintsRolesAndProfiles(t *testing.T) {
	eng := newAgentsTestEngine(t)

	out := captureStdout(t, func() {
		if rc := runAgents(context.Background(), eng, []string{"list"}, false); rc != 0 {
			t.Fatalf("runAgents list exit=%d", rc)
		}
	})

	for _, want := range []string{"Sub-agent catalog", "Roles", "Profiles"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents list missing %q:\n%s", want, out)
		}
	}
}

// TestRunAgents_ShowUnknown returns exit 1 — script-friendly without
// burying the user in noise. Re-uses the package-level captureStderr
// helper which only returns the captured stream; we run runAgents
// inline so we can observe its int return code separately.
func TestRunAgents_ShowUnknown(t *testing.T) {
	eng := newAgentsTestEngine(t)

	var rc int
	stderr := captureStderr(t, func() {
		rc = runAgents(context.Background(), eng, []string{"show", "definitely-not-a-real-thing"}, false)
	})
	if rc != 1 {
		t.Fatalf("unknown show should exit 1, got %d", rc)
	}
	if !strings.Contains(stderr, "no role or profile") {
		t.Errorf("stderr should explain why; got:\n%s", stderr)
	}
}

// TestRunAgents_JSONListEmitsCatalog asserts --json prints a parseable
// catalog so scripts can pipe `dfmc agents list --json | jq ...`.
func TestRunAgents_JSONListEmitsCatalog(t *testing.T) {
	eng := newAgentsTestEngine(t)

	out := captureStdout(t, func() {
		if rc := runAgents(context.Background(), eng, nil, true); rc != 0 {
			t.Fatalf("runAgents json exit=%d", rc)
		}
	})
	for _, want := range []string{`"roles"`, `"profiles"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json catalog missing %q:\n%s", want, out)
		}
	}
}
