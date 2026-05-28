package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestEngineTodosFromSpecFile_BasicIngest(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "PLAN.md")
	body := strings.Join([]string{
		"# Plan",
		"",
		"## Phase 1",
		"",
		"- [ ] Add the parser",
		"- [x] Old item already done",
		"- [ ] Verify migration",
	}, "\n")
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg := config.DefaultConfig()
	eng := &Engine{
		Config:      cfg,
		ProjectRoot: dir,
		Tools:       tools.NewFromConfig(cfg),
	}

	todos, dropped, err := eng.TodosFromSpecFile(context.Background(), "PLAN.md", "", false)
	if err != nil {
		t.Fatalf("TodosFromSpecFile: %v", err)
	}
	if dropped != 0 {
		t.Errorf("dropped should be 0 for well-formed spec, got %d", dropped)
	}
	// Default: skip done items, so 2 pending TODOs.
	if len(todos) != 2 {
		t.Fatalf("want 2 todos, got %d (%+v)", len(todos), todos)
	}
	if todos[0].Origin != "spec" {
		t.Errorf("origin should be 'spec', got %q", todos[0].Origin)
	}
	// "Verify" classifies as review/read_only — confirms the
	// classifier survived the round-trip through the engine helper.
	if !todos[1].ReadOnly || todos[1].WorkerClass != "reviewer" {
		t.Errorf("verify task should be reviewer/read_only: %+v", todos[1])
	}
}

func TestEngineTodosFromSpecFile_NilEngine(t *testing.T) {
	var e *Engine
	_, _, err := e.TodosFromSpecFile(context.Background(), "x.md", "", false)
	if !errors.Is(err, ErrEngineNil) {
		t.Errorf("nil engine should return ErrEngineNil, got %v", err)
	}
}

func TestEngineTodosFromSpecFile_MissingFileSurfacesAsError(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := &Engine{
		Config:      cfg,
		ProjectRoot: t.TempDir(),
		Tools:       tools.NewFromConfig(cfg),
	}
	_, _, err := eng.TodosFromSpecFile(context.Background(), "nope.md", "", false)
	if err == nil {
		t.Fatal("missing file should produce an error")
	}
	if !strings.Contains(err.Error(), "spec_to_todo") {
		t.Errorf("error should be wrapped with 'spec_to_todo' context: %v", err)
	}
}
