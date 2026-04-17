// End-to-end integration test that exercises the full Engine surface
// in one shot: Init → tool call (write_file + read_file) → memory
// add+search → conversation save+load → codemap (deferred until a
// project is built). The goal is to catch the "subsystems work in
// isolation but break together" class of regressions that the
// per-package unit tests miss by definition.
//
// We use the offline provider so this test runs on every machine
// regardless of API key availability — the agent_loop tests cover
// the provider-driven path.

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// newE2EEngine spins up a real Engine bound to a temp project root,
// with the offline provider as primary so no network is involved.
func newE2EEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "offline"

	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	eng.ProjectRoot = tmp
	t.Cleanup(func() { eng.Shutdown() })
	return eng, tmp
}

// Subsystems-together smoke test:
//   1. tool: write a Go file under the project root
//   2. tool: read it back, confirm content round-trip
//   3. memory: add an episodic entry, search for it
//   4. conversation: start, add messages, save, list
// If any of these subsystems was broken at construction (Init order,
// shared bbolt handle, project-root resolution), this test catches
// it without needing a live LLM.
func TestE2E_ToolWriteReadMemoryConversation(t *testing.T) {
	eng, root := newE2EEngine(t)
	ctx := context.Background()

	// 1. write_file via the tool registry.
	target := "hello.go"
	body := "package main\n\nfunc main() {}\n"
	res, err := eng.CallTool(ctx, "write_file", map[string]any{
		"path":    target,
		"content": body,
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !res.Success {
		t.Fatalf("write_file unsuccessful: %s", res.Output)
	}
	// Verify the file actually landed on disk under the project root.
	full := filepath.Join(root, target)
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("file not on disk after write_file: %v", err)
	}
	if string(got) != body {
		t.Fatalf("file content mismatch: got %q want %q", got, body)
	}

	// 2. read_file round-trip via the tool registry.
	readRes, err := eng.CallTool(ctx, "read_file", map[string]any{
		"path":       target,
		"line_start": 1,
		"line_end":   10,
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(readRes.Output, "func main()") {
		t.Fatalf("read_file output missing expected line; got %q", readRes.Output)
	}

	// 3. memory: add + search.
	if err := eng.MemoryAdd(types.MemoryEntry{
		Tier:       types.MemoryEpisodic,
		Category:   "test",
		Key:        "auth flow",
		Value:      "OAuth2 with PKCE",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("MemoryAdd: %v", err)
	}
	hits, err := eng.MemorySearch("auth", types.MemoryEpisodic, 5)
	if err != nil {
		t.Fatalf("MemorySearch: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected memory search to find the auth-flow entry")
	}

	// 4. conversation lifecycle: start, add, save.
	if eng.Conversation == nil {
		t.Skip("conversation manager not wired in this build")
	}
	conv := eng.Conversation.Start("offline", "offline-v1")
	if conv == nil {
		t.Fatalf("Conversation.Start returned nil")
	}
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleUser, Content: "what changed?",
	})
	eng.Conversation.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleAssistant, Content: "see hello.go",
	})
	if err := eng.Conversation.SaveActive(); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	list, err := eng.Conversation.List()
	if err != nil {
		t.Fatalf("conversation List: %v", err)
	}
	if len(list) == 0 {
		t.Fatalf("expected at least one conversation in list after save")
	}
}

// Status() must surface every subsystem the engine owns. If a future
// refactor accidentally drops a field from the Status struct (e.g.
// while extracting status_types.go further), this test fails with a
// clear pointer to which field went missing.
func TestE2E_StatusReportsAllSubsystems(t *testing.T) {
	eng, _ := newE2EEngine(t)
	st := eng.Status()

	if st.State == 0 {
		t.Errorf("Status.State should be populated after Init")
	}
	if strings.TrimSpace(st.Provider) == "" {
		t.Errorf("Status.Provider should be populated (offline default)")
	}
	if strings.TrimSpace(st.ASTBackend) == "" {
		t.Errorf("Status.ASTBackend should report regex or treesitter")
	}
	if strings.TrimSpace(st.ProjectRoot) == "" {
		t.Errorf("Status.ProjectRoot should be populated")
	}
	// Tools should be registered after Init — read_file at minimum.
	if eng.Tools == nil {
		t.Errorf("Engine.Tools should be non-nil after Init")
	} else {
		names := eng.Tools.List()
		found := false
		for _, n := range names {
			if n == "read_file" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected read_file in registered tools; got %v", names)
		}
	}
}

// Shutdown must be safe to call twice. main.go's defer eng.Shutdown()
// + a CTRL-C-driven Shutdown can both fire on exit; idempotency is
// the contract we rely on.
func TestE2E_ShutdownIdempotent(t *testing.T) {
	eng, _ := newE2EEngine(t)
	// First Shutdown via t.Cleanup (registered in newE2EEngine).
	// Manually call again — should not panic, should not error.
	eng.Shutdown()
	eng.Shutdown()
}
