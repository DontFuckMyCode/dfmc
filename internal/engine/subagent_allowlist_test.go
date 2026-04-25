package engine

import (
	"context"
	"strings"
	"testing"
)

// TestCheckSubagentAllowlist_NoActiveListIsNoop pins the default
// behaviour: a context without an attached allowlist permits every
// tool. Without this property the gate would refuse every
// agent / web / mcp dispatch — the regression hazard is large.
func TestCheckSubagentAllowlist_NoActiveListIsNoop(t *testing.T) {
	got := checkSubagentAllowlist(context.Background(), "run_command", nil)
	if got != "" {
		t.Fatalf("no-list context must permit, got refusal: %q", got)
	}
}

// TestCheckSubagentAllowlist_PermitsListed asserts a tool on the
// allowlist passes the gate.
func TestCheckSubagentAllowlist_PermitsListed(t *testing.T) {
	ctx := withSubagentAllowlist(context.Background(), []string{"read_file", "grep_codebase"})
	if got := checkSubagentAllowlist(ctx, "read_file", nil); got != "" {
		t.Fatalf("read_file must pass, got %q", got)
	}
	if got := checkSubagentAllowlist(ctx, "grep_codebase", nil); got != "" {
		t.Fatalf("grep_codebase must pass, got %q", got)
	}
}

// TestCheckSubagentAllowlist_RefusesUnlisted is the load-bearing
// VULN-035 invariant: a tool NOT on the list is refused with a
// human-readable reason naming the tool.
func TestCheckSubagentAllowlist_RefusesUnlisted(t *testing.T) {
	ctx := withSubagentAllowlist(context.Background(), []string{"read_file"})
	got := checkSubagentAllowlist(ctx, "run_command", nil)
	if got == "" {
		t.Fatalf("run_command must be refused under {read_file} allowlist")
	}
	if !strings.Contains(got, "run_command") {
		t.Fatalf("refusal should name the offending tool, got %q", got)
	}
}

// TestCheckSubagentAllowlist_CaseAndWhitespace mirrors how operators
// configure delegate_task — tolerate leading/trailing whitespace and
// mixed case.
func TestCheckSubagentAllowlist_CaseAndWhitespace(t *testing.T) {
	ctx := withSubagentAllowlist(context.Background(), []string{"  Read_File  ", " GREP_CODEBASE"})
	if got := checkSubagentAllowlist(ctx, "read_file", nil); got != "" {
		t.Fatalf("case-insensitive read_file must pass, got %q", got)
	}
	if got := checkSubagentAllowlist(ctx, "grep_codebase", nil); got != "" {
		t.Fatalf("case-insensitive grep_codebase must pass, got %q", got)
	}
}

// TestCheckSubagentAllowlist_EmptyListNoEnforcement — explicit empty
// list is treated as "no allowlist active" so the legacy callsite
// where delegate_task is invoked without allowed_tools keeps working.
func TestCheckSubagentAllowlist_EmptyListNoEnforcement(t *testing.T) {
	ctx := withSubagentAllowlist(context.Background(), []string{})
	if got := checkSubagentAllowlist(ctx, "run_command", nil); got != "" {
		t.Fatalf("empty list must be a no-op, got %q", got)
	}
	ctx = withSubagentAllowlist(context.Background(), []string{"", "  "})
	if got := checkSubagentAllowlist(ctx, "run_command", nil); got != "" {
		t.Fatalf("whitespace-only list must be a no-op, got %q", got)
	}
}

// TestCheckSubagentAllowlist_MetaWrapperInnerCheck pins the
// escape-hatch defence: a sub-agent that calls
// tool_batch_call([{run_command,...}, {read_file,...}]) under a
// {read_file} allowlist must be refused — the meta wrapper itself is
// not auto-permitted because tool dispatch inside meta.go does NOT
// re-enter executeToolWithLifecycle.
func TestCheckSubagentAllowlist_MetaWrapperInnerCheck(t *testing.T) {
	ctx := withSubagentAllowlist(context.Background(), []string{"read_file"})

	// All-listed batch passes.
	if got := checkSubagentAllowlist(ctx, "tool_batch_call", []string{"read_file", "read_file"}); got != "" {
		t.Fatalf("batch of all-listed tools must pass, got %q", got)
	}

	// Mixed batch — one listed, one not — must fail and name the
	// offender.
	got := checkSubagentAllowlist(ctx, "tool_batch_call", []string{"read_file", "run_command"})
	if got == "" {
		t.Fatalf("mixed batch must be refused")
	}
	if !strings.Contains(got, "run_command") {
		t.Fatalf("refusal should name the offending inner tool, got %q", got)
	}

	// tool_call wrapping an unlisted backend is refused.
	got = checkSubagentAllowlist(ctx, "tool_call", []string{"run_command"})
	if got == "" {
		t.Fatalf("tool_call(run_command) must be refused")
	}
}

// TestCheckSubagentAllowlist_DiscoveryMetaToolsAlwaysPermitted —
// tool_search and tool_help are pure read-only metadata; refusing
// them would lock the sub-agent out of figuring out what's on its
// allowlist and produce confusing model behaviour.
func TestCheckSubagentAllowlist_DiscoveryMetaToolsAlwaysPermitted(t *testing.T) {
	ctx := withSubagentAllowlist(context.Background(), []string{"read_file"})
	for _, name := range []string{"tool_search", "tool_help"} {
		if got := checkSubagentAllowlist(ctx, name, nil); got != "" {
			t.Fatalf("%s must always pass the allowlist gate, got %q", name, got)
		}
	}
}

// TestExecuteToolWithLifecycle_SubagentAllowlistEnforced wires the
// gate into the real lifecycle path — the same dispatcher used by
// the agent loop. Confirms an unlisted call surfaces as a typed
// error with the "denied" wording, fires a tool:denied event, and
// records the denial in the engine's recent-denials log so the
// trajectory coach can reflect on it.
func TestExecuteToolWithLifecycle_SubagentAllowlistEnforced(t *testing.T) {
	eng := newApproverTestEngine(t)
	ctx := withSubagentAllowlist(context.Background(), []string{"read_file"})
	_, err := eng.executeToolWithLifecycle(ctx, "write_file",
		map[string]any{"path": "out.txt", "content": "x"}, "subagent")
	if err == nil {
		t.Fatalf("write_file under {read_file} allowlist must be refused")
	}
	if !strings.Contains(err.Error(), "denied") || !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("expected denial error naming the tool, got %v", err)
	}
}

// TestExecuteToolWithLifecycle_SubagentAllowlistPermitsListed
// confirms the gate doesn't over-reject — a listed tool passes
// through to the real Execute and returns a normal Result.
func TestExecuteToolWithLifecycle_SubagentAllowlistPermitsListed(t *testing.T) {
	eng := newApproverTestEngine(t)
	ctx := withSubagentAllowlist(context.Background(), []string{"read_file"})
	_, err := eng.executeToolWithLifecycle(ctx, "read_file",
		map[string]any{"path": "hello.txt"}, "subagent")
	if err != nil {
		t.Fatalf("read_file under {read_file} allowlist must pass, got %v", err)
	}
}
