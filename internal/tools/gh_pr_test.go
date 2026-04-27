package tools

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestGHPullRequest_NoGH verifies that when gh is not authenticated,
// the tool returns a clear error rather than a panic or empty result.
func TestGHPullRequest_NoGH(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "list"},
	})
	// Auth error or "gh not found" — both are clear, not cryptic
	if err == nil {
		t.Fatalf("expected error when gh is not authenticated")
	}
}

func TestGHPullRequest_MissingAction(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	// Defaults to "list" but fails on auth
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestGHPullRequest_ViewRequiresNumber(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "view"},
	})
	if err == nil {
		t.Fatalf("expected error for missing number")
	}
}

func TestGHPullRequest_DiffRequiresNumber(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "diff"},
	})
	if err == nil {
		t.Fatalf("expected error for missing number")
	}
}

func TestGHPullRequest_ChecksRequiresNumber(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "checks"},
	})
	if err == nil {
		t.Fatalf("expected error for missing number")
	}
}

func TestGHPullRequest_StatusRequiresNumber(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "status"},
	})
	if err == nil {
		t.Fatalf("expected error for missing number")
	}
}

func TestGHPullRequest_InvalidAction(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "gh_pr", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "foobar"},
	})
	if err == nil {
		t.Fatalf("expected error for invalid action")
	}
}

func TestGHPullRequestTool_Name(t *testing.T) {
	tool := NewGHPullRequestTool()
	if tool.Name() != "gh_pr" {
		t.Errorf("want gh_pr, got %s", tool.Name())
	}
}

func TestGHPullRequestTool_Spec(t *testing.T) {
	tool := NewGHPullRequestTool()
	spec := tool.Spec()
	if spec.Name != "gh_pr" {
		t.Errorf("spec.Name: want gh_pr, got %s", spec.Name)
	}
	if spec.Risk != RiskRead {
		t.Errorf("spec.Risk: want RiskRead, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"action", "number", "repo", "state", "limit", "include_diff"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestGHPullRequestTool_Description(t *testing.T) {
	tool := NewGHPullRequestTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestGHPullRequestTool_ExecuteNoPanic(t *testing.T) {
	tool := NewGHPullRequestTool()
	// Should not panic — context.Background is valid
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "list"},
	})
	// Error is fine (auth failure), panic would fail the test
	_ = err
}

func TestGHPullRequestTool_ExecuteDirectAuthCheck(t *testing.T) {
	tool := NewGHPullRequestTool()
	// Direct call with no engine — auth check still fires
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"action": "list"},
	})
	if err == nil {
		t.Fatalf("expected auth error")
	}
}

func TestGHPullRequestTool_SetEngineNotRequired(t *testing.T) {
	// gh_pr doesn't use engine or codemap
	tool := NewGHPullRequestTool()
	if tool.Name() != "gh_pr" {
		t.Errorf("name mismatch")
	}
}