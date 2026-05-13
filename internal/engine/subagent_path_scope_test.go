package engine

import (
	"context"
	"strings"
	"testing"
)

func TestNormalisePathPrefix_Cases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"internal/parsers", "internal/parsers"},
		{"internal/parsers/", "internal/parsers"},
		{"internal\\parsers", "internal/parsers"},
		{"internal/parsers/.", "internal/parsers"},
		{"  internal/parsers  ", "internal/parsers"},
		{"./foo", "foo"},
		{"", ""},
		{"   ", ""},
		{"/", ""},
	}
	for _, c := range cases {
		if got := normalisePathPrefix(c.in); got != c.want {
			t.Errorf("normalisePathPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPathScopeAllows_PrefixBoundary(t *testing.T) {
	allow := []string{"internal/parsers", "docs/specs"}
	cases := []struct {
		path string
		want bool
	}{
		{"internal/parsers", true},           // exact
		{"internal/parsers/foo.go", true},    // direct child
		{"internal/parsers/sub/x.go", true},  // nested
		{"docs/specs/PLAN.md", true},         // alt root
		{"internal/parsers_aux/x.go", false}, // adjacent name — must NOT admit
		{"internal/other/x.go", false},
		{"unrelated.go", false},
		{"", false},
	}
	for _, c := range cases {
		got := pathScopeAllows(normalisePathPrefix(c.path), allow)
		if got != c.want {
			t.Errorf("pathScopeAllows(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestCheckSubagentPathScope_NoActiveListIsNoop(t *testing.T) {
	got := checkSubagentPathScope(context.Background(), "write_file", map[string]any{"path": "/etc/passwd"})
	if got != "" {
		t.Errorf("no-scope context must permit, got refusal: %q", got)
	}
}

func TestCheckSubagentPathScope_PermitsInsideAllow(t *testing.T) {
	ctx := withSubagentPathScope(context.Background(), []string{"internal/parsers"})
	if got := checkSubagentPathScope(ctx, "write_file", map[string]any{"path": "internal/parsers/foo.go"}); got != "" {
		t.Errorf("inside-prefix write must pass, got %q", got)
	}
}

func TestCheckSubagentPathScope_RefusesOutside(t *testing.T) {
	ctx := withSubagentPathScope(context.Background(), []string{"internal/parsers"})
	got := checkSubagentPathScope(ctx, "write_file", map[string]any{"path": "internal/auth/foo.go"})
	if got == "" {
		t.Fatalf("outside-prefix write must be refused")
	}
	if !strings.Contains(got, "internal/auth/foo.go") {
		t.Errorf("refusal should name the offending path, got %q", got)
	}
	if !strings.Contains(got, "internal/parsers") {
		t.Errorf("refusal should name the allow list, got %q", got)
	}
}

func TestCheckSubagentPathScope_SymbolMoveBothEnds(t *testing.T) {
	ctx := withSubagentPathScope(context.Background(), []string{"internal/parsers"})
	// Both ends inside → permit.
	if got := checkSubagentPathScope(ctx, "symbol_move", map[string]any{
		"from_path": "internal/parsers/a.go",
		"to_path":   "internal/parsers/b.go",
	}); got != "" {
		t.Errorf("both ends inside must pass, got %q", got)
	}
	// One end outside → refuse.
	got := checkSubagentPathScope(ctx, "symbol_move", map[string]any{
		"from_path": "internal/parsers/a.go",
		"to_path":   "internal/auth/b.go",
	})
	if got == "" {
		t.Fatalf("symbol_move into outside-prefix must be refused")
	}
}

func TestCheckSubagentPathScope_ReadToolsUnaffected(t *testing.T) {
	// Path scope only inspects write tools (extractWriteToolPaths
	// returns nothing for read_file). A scoped sub-agent must still
	// be able to read freely — operators who want a read restriction
	// drop read_file from AllowedTools instead.
	ctx := withSubagentPathScope(context.Background(), []string{"internal/parsers"})
	if got := checkSubagentPathScope(ctx, "read_file", map[string]any{"path": "internal/auth/foo.go"}); got != "" {
		t.Errorf("read_file outside scope should NOT be refused (scope is write-only), got %q", got)
	}
}

func TestCheckSubagentPathScope_NormalisesBothSides(t *testing.T) {
	// Allow list uses Windows separator; param uses Unix separator.
	ctx := withSubagentPathScope(context.Background(), []string{`internal\parsers`})
	if got := checkSubagentPathScope(ctx, "write_file", map[string]any{"path": "internal/parsers/foo.go"}); got != "" {
		t.Errorf("cross-separator match must pass, got %q", got)
	}
}

// Pins the bypass-defence: a sub-agent that tries to write outside
// scope by routing through tool_call({name:write_file, ...}) must be
// refused. Without the meta-unwrap branch in checkSubagentPathScope,
// path enforcement would only apply when the model invoked
// write_file directly — i.e. essentially never, since the native
// tool loop wraps everything via tool_call.
func TestCheckSubagentPathScope_RefusesViaMetaToolCall(t *testing.T) {
	ctx := withSubagentPathScope(context.Background(), []string{"internal/parsers"})
	got := checkSubagentPathScope(ctx, "tool_call", map[string]any{
		"name": "write_file",
		"args": map[string]any{"path": "internal/auth/foo.go", "content": "x"},
	})
	if got == "" {
		t.Fatalf("tool_call wrapper around an outside-scope write must be refused")
	}
	if !strings.Contains(got, "internal/auth/foo.go") {
		t.Errorf("refusal should name the offending path, got %q", got)
	}
}

func TestCheckSubagentPathScope_RefusesViaToolBatchCall(t *testing.T) {
	ctx := withSubagentPathScope(context.Background(), []string{"internal/parsers"})
	// Mixed batch — one inside scope, one outside. The all-or-nothing
	// rule must refuse the whole dispatch.
	got := checkSubagentPathScope(ctx, "tool_batch_call", map[string]any{
		"calls": []any{
			map[string]any{"name": "write_file", "args": map[string]any{"path": "internal/parsers/ok.go", "content": "x"}},
			map[string]any{"name": "write_file", "args": map[string]any{"path": "internal/auth/bad.go", "content": "x"}},
		},
	})
	if got == "" {
		t.Fatalf("batch with one outside-scope write must refuse the whole dispatch")
	}
	if !strings.Contains(got, "internal/auth/bad.go") {
		t.Errorf("refusal should name the offending path, got %q", got)
	}
}

func TestCheckSubagentPathScope_EmptyListNoEnforcement(t *testing.T) {
	ctx := withSubagentPathScope(context.Background(), []string{})
	if got := checkSubagentPathScope(ctx, "write_file", map[string]any{"path": "/etc/passwd"}); got != "" {
		t.Errorf("empty list must be no-op, got %q", got)
	}
	ctx = withSubagentPathScope(context.Background(), []string{"", "  "})
	if got := checkSubagentPathScope(ctx, "write_file", map[string]any{"path": "/etc/passwd"}); got != "" {
		t.Errorf("whitespace-only list must be no-op, got %q", got)
	}
}
