package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("package main\n// marker TODO\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	eng := New(*config.DefaultConfig())
	return eng, tmp
}

func TestEngineRegistersMetaTools(t *testing.T) {
	eng, _ := newTestEngine(t)
	for _, name := range []string{"tool_search", "tool_help", "tool_call", "tool_batch_call"} {
		if _, ok := eng.Get(name); !ok {
			t.Fatalf("meta tool not registered: %s", name)
		}
	}
	meta := eng.MetaSpecs()
	if len(meta) != 4 {
		t.Fatalf("MetaSpecs() count: want 4, got %d", len(meta))
	}
	backend := eng.BackendSpecs()
	for _, s := range backend {
		if isMetaTool(s.Name) {
			t.Errorf("BackendSpecs should exclude meta: %s", s.Name)
		}
	}
}

func TestToolSearchReturnsRankedResults(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_search", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"query": "grep"},
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	if !strings.Contains(res.Output, "grep_codebase") {
		t.Fatalf("expected grep_codebase in search output: %q", res.Output)
	}
	// Meta tools must not appear in search results even if query matches them.
	if strings.Contains(res.Output, "tool_search") || strings.Contains(res.Output, "tool_call") {
		t.Fatalf("meta tools leaked into search output: %q", res.Output)
	}
	data, _ := res.Data["results"].([]map[string]any)
	if len(data) == 0 {
		t.Fatalf("expected results array, got %v", res.Data)
	}
}

func TestToolSearchRequiresQuery(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_search", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestToolHelpReturnsSchema(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_help", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "read_file"},
	})
	if err != nil {
		t.Fatalf("tool_help: %v", err)
	}
	if !strings.Contains(res.Output, "Args:") {
		t.Fatalf("expected Args: section in help, got %q", res.Output)
	}
	schema, _ := res.Data["schema"].(map[string]any)
	if schema == nil {
		t.Fatalf("expected schema in data, got %v", res.Data)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Fatalf("expected path in schema properties, got %v", props)
	}
}

func TestToolHelpUnknownTool(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_help", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "nope"},
	})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestToolCallDispatchesToBackend(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "read_file",
			"args": map[string]any{"path": "hello.go"},
		},
	})
	if err != nil {
		t.Fatalf("tool_call: %v", err)
	}
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected file contents, got %q", res.Output)
	}
}

func TestToolCallRefusesMetaTarget(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "tool_search",
			"args": map[string]any{"query": "read"},
		},
	})
	if err == nil {
		t.Fatal("expected error when calling meta via tool_call")
	}
	if !strings.Contains(err.Error(), "meta") {
		t.Fatalf("expected meta-tool refusal message, got %v", err)
	}
}

func TestToolCallAcceptsStringifiedArgs(t *testing.T) {
	eng, tmp := newTestEngine(t)
	argsJSON, _ := json.Marshal(map[string]any{"path": "hello.go"})
	res, err := eng.Execute(context.Background(), "tool_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"name": "read_file",
			"args": string(argsJSON),
		},
	})
	if err != nil {
		t.Fatalf("tool_call with stringified args: %v", err)
	}
	if !strings.Contains(res.Output, "package main") {
		t.Fatalf("expected file contents from stringified args, got %q", res.Output)
	}
}

func TestToolBatchCallCollectsResults(t *testing.T) {
	eng, tmp := newTestEngine(t)
	if err := os.WriteFile(filepath.Join(tmp, "second.go"), []byte("package second\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"calls": []any{
				map[string]any{"name": "read_file", "args": map[string]any{"path": "hello.go"}},
				map[string]any{"name": "read_file", "args": map[string]any{"path": "second.go"}},
				map[string]any{"name": "read_file", "args": map[string]any{"path": "missing.go"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 3 {
		t.Fatalf("expected 3 result entries, got %d", len(arr))
	}
	// First two succeed.
	for _, i := range []int{0, 1} {
		if ok, _ := arr[i]["success"].(bool); !ok {
			t.Errorf("results[%d] should succeed, got %v", i, arr[i])
		}
	}
	// Third fails but the batch continues.
	if ok, _ := arr[2]["success"].(bool); ok {
		t.Errorf("results[2] should fail, got %v", arr[2])
	}
	if _, ok := arr[2]["error"]; !ok {
		t.Errorf("results[2] should expose error message, got %v", arr[2])
	}
}

func TestToolBatchCallRefusesMetaTarget(t *testing.T) {
	eng, tmp := newTestEngine(t)
	res, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"calls": []any{
				map[string]any{"name": "tool_search", "args": map[string]any{"query": "x"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("tool_batch_call: %v", err)
	}
	arr, _ := res.Data["results"].([]map[string]any)
	if len(arr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(arr))
	}
	if ok, _ := arr[0]["success"].(bool); ok {
		t.Errorf("meta-target call should be refused, got %v", arr[0])
	}
}

func TestToolBatchCallRequiresCalls(t *testing.T) {
	eng, tmp := newTestEngine(t)
	_, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing calls")
	}
}
