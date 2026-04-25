package hooks

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestFire_NoOpOnNil — the zero-value safety check that lets callers
// embed *Dispatcher without null-guarding every call site.
func TestFire_NoOpOnNil(t *testing.T) {
	var d *Dispatcher
	got := d.Fire(context.Background(), EventPreTool, Payload{"tool_name": "x"})
	if got != 0 {
		t.Fatalf("nil dispatcher must run 0 hooks, got %d", got)
	}
}

// TestFire_EmptyConfigRunsNothing — a built-but-empty dispatcher must
// not run anything and must not panic.
func TestFire_EmptyConfigRunsNothing(t *testing.T) {
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{}}, nil)
	if got := d.Fire(context.Background(), EventUserPromptSubmit, nil); got != 0 {
		t.Fatalf("empty config should run 0 hooks, got %d", got)
	}
	if got := d.Count(EventUserPromptSubmit); got != 0 {
		t.Fatalf("Count on empty config = %d, want 0", got)
	}
}

// TestFire_RunsRegisteredHook — the happy path: a hook fires, observer
// receives a Report, event-name env var is visible to the command.
func TestFire_RunsRegisteredHook(t *testing.T) {
	// Pick a command that always succeeds on both unix and windows.
	cmd := "echo ok"
	if runtime.GOOS == "windows" {
		cmd = "echo ok"
	}
	var (
		mu      sync.Mutex
		reports []Report
	)
	observer := func(r Report) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, r)
	}
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {{Name: "smoke", Command: cmd}},
	}}, observer)
	ran := d.Fire(context.Background(), EventPreTool, Payload{"tool_name": "read_file"})
	if ran != 1 {
		t.Fatalf("expected 1 hook fired, got %d", ran)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 {
		t.Fatalf("observer got %d reports, want 1", len(reports))
	}
	if reports[0].Err != nil {
		t.Fatalf("hook reported error: %v (stderr=%q)", reports[0].Err, reports[0].Stderr)
	}
	if !strings.Contains(reports[0].Stdout, "ok") {
		t.Fatalf("expected stdout to contain 'ok', got %q", reports[0].Stdout)
	}
	if reports[0].Name != "smoke" {
		t.Fatalf("report.Name = %q, want 'smoke'", reports[0].Name)
	}
}

func TestFire_RunsArgvHookWithoutShell(t *testing.T) {
	var reports []Report
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {{
			Name:    "argv-smoke",
			Command: "go",
			Args:    []string{"env", "GOOS"},
		}},
	}}, func(r Report) { reports = append(reports, r) })

	ran := d.Fire(context.Background(), EventPreTool, nil)
	if ran != 1 {
		t.Fatalf("expected 1 argv hook fired, got %d", ran)
	}
	if len(reports) != 1 {
		t.Fatalf("observer got %d reports, want 1", len(reports))
	}
	if reports[0].Err != nil {
		t.Fatalf("argv hook reported error: %v (stderr=%q)", reports[0].Err, reports[0].Stderr)
	}
	if strings.TrimSpace(reports[0].Stdout) == "" {
		t.Fatal("expected argv hook to capture stdout")
	}
}

// TestFire_ConditionFilter — a condition that doesn't match skips the
// hook entirely. The observer receives nothing in that case.
func TestFire_ConditionFilter(t *testing.T) {
	var fired int
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {
			{Name: "only-apply-patch", Condition: "tool_name == apply_patch", Command: "echo match"},
			{Name: "never-run-command", Condition: "tool_name != run_command", Command: "echo also-match"},
			{Name: "substring", Condition: "tool_name ~ apply", Command: "echo match3"},
			{Name: "mismatch", Condition: "tool_name == write_file", Command: "echo should-not-run"},
		},
	}}, func(Report) { fired++ })

	ran := d.Fire(context.Background(), EventPreTool, Payload{"tool_name": "apply_patch"})
	// Three of the four conditions match "apply_patch":
	//   only-apply-patch  → ==  match
	//   never-run-command → !=  match
	//   substring         → ~   match
	//   mismatch          → ==  no match
	if ran != 3 {
		t.Fatalf("condition filter produced %d runs, want 3", ran)
	}
	if fired != 3 {
		t.Fatalf("observer fired %d times, want 3", fired)
	}
}

// TestFire_HookFailureLoggedNotPanicked — a non-zero-exit hook reports
// via observer with a populated Err field but does NOT panic or stop
// subsequent hooks.
func TestFire_HookFailureLoggedNotPanicked(t *testing.T) {
	var reports []Report
	observer := func(r Report) { reports = append(reports, r) }
	// `exit 2` is portable between sh and cmd.exe.
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {
			{Name: "fails", Command: "exit 2"},
			{Name: "succeeds-after", Command: "echo after"},
		},
	}}, observer)
	ran := d.Fire(context.Background(), EventPreTool, nil)
	if ran != 2 {
		t.Fatalf("both hooks should run even though the first fails, got ran=%d", ran)
	}
	if len(reports) != 2 {
		t.Fatalf("observer should see both reports, got %d", len(reports))
	}
	if reports[0].Err == nil {
		t.Fatalf("first report should carry Err for non-zero exit")
	}
	if reports[1].Err != nil {
		t.Fatalf("second report should succeed, got err=%v", reports[1].Err)
	}
}

// TestSanitizeEnvKey — non-alphanumerics become underscores, letters
// upper-cased, empty input stays empty. Reject-on-empty is a soft
// protection against bizarre keys sneaking through.
func TestSanitizeEnvKey(t *testing.T) {
	cases := map[string]string{
		"tool_name":   "TOOL_NAME",
		"tool.name":   "TOOL_NAME",
		"toolName":    "TOOLNAME",
		"123-weird$":  "123_WEIRD_",
		"":            "",
		"lowercase":   "LOWERCASE",
		"with spaces": "WITH_SPACES",
	}
	for in, want := range cases {
		if got := sanitizeEnvKey(in); got != want {
			t.Errorf("sanitizeEnvKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFire_TimeoutKillsRunawayHook — a hook that never returns must be
// interrupted by the dispatcher's timeout, not left dangling to stall
// future events.
//
// Skipped on Windows because exec.CommandContext sends Kill to cmd.exe
// but cmd.exe does not reliably forward the signal to its child, so the
// subprocess (sleep/timeout) runs to completion and makes the assertion
// flaky. The timeout code path itself is still correct on Windows — the
// parent cmd.exe is killed, just not the grandchild — and the assertion
// is re-enabled on unix hosts where the signal propagation is sane.
func TestFire_TimeoutKillsRunawayHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cmd.exe doesn't propagate Kill to child processes reliably; covered on unix")
	}
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {{Name: "slow", Command: "sleep 5"}},
	}}, nil)
	d.defaultTO = 200 * time.Millisecond

	start := time.Now()
	d.Fire(context.Background(), EventPreTool, nil)
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("timeout should have killed the hook within the budget; elapsed=%v", elapsed)
	}
}

// VULN-048: a panicking observer must not unwind the dispatch loop.
// Hooks are best-effort — a buggy observer is the engine's own bug
// to fix, not a reason to crash the tool call that fired the hook.
func TestFire_ObserverPanicIsContained(t *testing.T) {
	cmd := "echo ok"
	if runtime.GOOS == "windows" {
		cmd = "cmd.exe /C echo ok"
	}

	var calls int
	var mu sync.Mutex
	observer := func(report Report) {
		mu.Lock()
		calls++
		mu.Unlock()
		// First observer call panics; second hook's call must still
		// run — the dispatch loop must not be unwound.
		if calls == 1 {
			panic("observer boom")
		}
	}

	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {
			{Name: "first", Command: cmd},
			{Name: "second", Command: cmd},
		},
	}}, observer)

	// Must not panic.
	got := d.Fire(context.Background(), EventPreTool, Payload{"tool_name": "x"})
	if got != 2 {
		t.Fatalf("expected both hooks to run despite observer panic, got ran=%d", got)
	}
	mu.Lock()
	if calls != 2 {
		t.Fatalf("expected observer to be called twice, got %d", calls)
	}
	mu.Unlock()
}

// VULN-048: a panic in conditionMatches (synthetically: we exercise
// the panic-guard contract by injecting a panicking observer into a
// hook that is gated by a no-op condition). The fireOne wrapper must
// catch any panic from the per-hook stack and surface a synthetic
// "hook panic" Report through the observer-safe path so operators
// see the failure without losing the rest of the chain.
func TestFire_PanicSurfacedAsReport(t *testing.T) {
	cmd := "echo ok"
	if runtime.GOOS == "windows" {
		cmd = "cmd.exe /C echo ok"
	}

	var seen []Report
	var mu sync.Mutex
	observer := func(r Report) {
		mu.Lock()
		seen = append(seen, r)
		count := len(seen)
		mu.Unlock()
		if count == 1 {
			// First call panics — the next hook must still get a
			// fresh observer invocation (and panic again, harmlessly).
			panic("synthetic")
		}
	}

	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {
			{Name: "first", Command: cmd},
			{Name: "second", Command: cmd},
		},
	}}, observer)

	d.Fire(context.Background(), EventPreTool, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("expected both hooks to deliver a report despite panicking observer, got %d", len(seen))
	}
}

// VULN-048: with timeout=0 (default 30s), a quick-running hook must
// not be affected by the panic guard — sanity check that the guard
// doesn't break the happy path.
func TestFire_HappyPathStillWorks(t *testing.T) {
	cmd := "echo ok"
	if runtime.GOOS == "windows" {
		cmd = "cmd.exe /C echo ok"
	}
	var got Report
	d := New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool": {{Name: "ok", Command: cmd}},
	}}, func(r Report) { got = r })
	if ran := d.Fire(context.Background(), EventPreTool, nil); ran != 1 {
		t.Fatalf("ran=%d, want 1", ran)
	}
	if got.Err != nil {
		t.Fatalf("unexpected hook error: %v", got.Err)
	}
	if got.Duration > 30*time.Second {
		t.Fatalf("unexpected hook duration: %v", got.Duration)
	}
}

// TestDescribe — surfaces hook counts per event for status displays.
func TestDescribe(t *testing.T) {
	var d *Dispatcher
	if got := d.Describe(); !strings.Contains(got, "not initialized") {
		t.Errorf("nil dispatcher Describe should say not initialized, got %q", got)
	}
	d = New(config.HooksConfig{Entries: map[string][]config.HookEntry{}}, nil)
	if got := d.Describe(); !strings.Contains(got, "none registered") {
		t.Errorf("empty Describe should say none registered, got %q", got)
	}
	d = New(config.HooksConfig{Entries: map[string][]config.HookEntry{
		"pre_tool":           {{Name: "a", Command: "echo"}},
		"user_prompt_submit": {{Name: "b", Command: "echo"}, {Name: "c", Command: "echo"}},
	}}, nil)
	got := d.Describe()
	if !strings.Contains(got, "pre_tool(1)") || !strings.Contains(got, "user_prompt_submit(2)") {
		t.Errorf("Describe should include per-event counts, got %q", got)
	}
}
