// Regression tests for the Critical-severity fixes flagged in the
// 2026-04-17 review:
//
//   C1 — Memory.Load error no longer swallowed silently; degraded flag
//        surfaces in Status and an event is published.
//   C3 — /continue no longer grants unbounded fresh MaxSteps budgets;
//        cumulative steps/tokens enforce an outer ceiling.
//   M4 — EventBus.Publish is nil-receiver-safe so shutdown races can't
//        panic a best-effort publisher.

package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// --- M4: nil EventBus guards -------------------------------------

func TestEventBus_NilPublishDoesNotPanic(t *testing.T) {
	// A nil receiver must be a no-op, not a panic. This is the shape
	// of bug M4: a caller's "check e.EventBus != nil" is not atomic
	// with the Publish call during shutdown, so the receiver can race
	// to nil under concurrent teardown.
	var eb *EventBus
	eb.Publish(Event{Type: "probe"})
}

func TestEventBus_NilSubscribeReturnsClosedChannel(t *testing.T) {
	var eb *EventBus
	ch := eb.Subscribe("whatever")
	// A range loop over a closed channel terminates instead of
	// blocking, so the caller doesn't deadlock.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel, got an event")
		}
	default:
		t.Fatal("expected closed channel that receives immediately")
	}
}

func TestEventBus_NilUnsubscribeDoesNotPanic(t *testing.T) {
	var eb *EventBus
	eb.Unsubscribe("x", make(chan Event))
}

func TestEventBus_PublishToExternallyClosedChannelDoesNotPanic(t *testing.T) {
	eb := NewEventBus()
	ch := eb.Subscribe("probe")
	close(ch)
	eb.Publish(Event{Type: "probe"})
	if eb.DroppedCount() == 0 {
		t.Fatal("expected dropped count to increase when publishing to a closed subscriber channel")
	}
}

// --- C1: Status surfaces memory-degraded flag --------------------

// The Status copy-in must include the degraded flag verbatim, so the
// TUI / Web / remote clients can render a warning. This guards against
// someone adding a new Status field and forgetting to copy one of the
// two memory fields into the return struct.
func TestStatus_ExposesMemoryDegradedFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	e := &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		state:    StateReady,
	}
	e.memoryDegraded = true
	e.memoryLoadErr = "bolt: database not open"

	s := e.Status()
	if !s.MemoryDegraded {
		t.Fatal("Status().MemoryDegraded should mirror e.memoryDegraded")
	}
	if s.MemoryLoadErr != "bolt: database not open" {
		t.Fatalf("MemoryLoadErr not surfaced: %q", s.MemoryLoadErr)
	}
}

func TestAskBeforeInitReturnsDescriptiveError(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.Ask(context.Background(), "hello"); err == nil || !errors.Is(err, ErrEngineNotInitialized) {
		t.Fatalf("expected pre-init ask error, got %v", err)
	}
}

func TestCallToolBeforeInitReturnsDescriptiveError(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.CallTool(context.Background(), "read_file", map[string]any{"path": "go.mod"}); err == nil || !errors.Is(err, ErrEngineNotInitialized) {
		t.Fatalf("expected pre-init tool error, got %v", err)
	}
}

// --- C3: cumulative resume ceiling -------------------------------

// After the ceiling is reached, a further /continue must refuse with
// a clear error and re-park the snapshot so the user's state isn't
// lost. The core of the fix: Step and TotalTokens reset per resume
// (so each attempt really does progress), but CumulativeSteps keeps
// climbing and eventually trips resumeMaxMultiplier * MaxSteps.
func TestResumeAgent_RefusesAfterCumulativeStepsCeiling(t *testing.T) {
	// Two looping reads + a never-reached final answer — each resume
	// starts a fresh MaxSteps=1 budget, so each round consumes 1 step
	// then parks. With resumeMaxMultiplier=3 and MaxSteps=1, the
	// third resume must refuse.
	eng, _, _ := buildGuardTestEngine(t, 0, 1, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c1")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c2")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c3")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c4")}},
	})
	// Pin the historical 3x ceiling so this test still drives the
	// "third resume refuses" assertion. The default ceiling is now 10x
	// for sustained orchestration; without this override we'd need 11
	// scripted resume rounds to reach the refusal path.
	eng.Config.Agent.ResumeMaxMultiplier = 3
	eng.Config.Agent.AutonomousResume = "off"

	// Initial ask — this consumes the FIRST budget (MaxSteps=1) and
	// parks, accumulating CumulativeSteps=1 after the next resume.
	if _, err := eng.AskWithMetadata(context.Background(), "ceiling check"); err != nil {
		t.Fatalf("initial ask: %v", err)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected park after initial MaxSteps=1 round")
	}

	// First resume — consumes the SECOND budget, parks again,
	// cumulative=2 after the next resume.
	if _, err := eng.ResumeAgent(context.Background(), ""); err != nil {
		t.Fatalf("first resume should succeed: %v", err)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected park after first resume")
	}

	// Second resume — consumes the THIRD budget, parks again,
	// cumulative=3 after next resume. That equals the ceiling
	// (resumeMaxMultiplier=3 * MaxSteps=1), so the NEXT call must
	// refuse.
	if _, err := eng.ResumeAgent(context.Background(), ""); err != nil {
		t.Fatalf("second resume should succeed: %v", err)
	}
	if !eng.HasParkedAgent() {
		t.Fatal("expected park after second resume")
	}

	// Third resume — this is the one that should refuse.
	_, err := eng.ResumeAgent(context.Background(), "")
	if err == nil {
		t.Fatal("third resume must refuse once cumulative ceiling is reached")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("error message should mention ceiling, got %q", err.Error())
	}
	// Snapshot must still be recoverable — the user's work isn't lost.
	if !eng.HasParkedAgent() {
		t.Fatal("refused resume must re-park the snapshot")
	}
}

// TestAutonomousResume_CancelInClaimWindowRepArks guards the narrow
// race where ctx is cancelled AFTER attemptAutoResume has claimed the
// parked state out of the engine but BEFORE the loop re-enters
// runNativeToolLoop. Previously the top-of-loop ctx guard returned
// without re-parking the claimed seed, stranding the user with
// ErrNoParkedAgent on /continue and losing all accumulated work. The
// fix re-parks the claimed seed on that exit. We trigger the window
// deterministically by cancelling ctx during the first (and only)
// scripted round; with MaxSteps=1 the round parks for StepCap, the
// autonomous wrapper claims it via attemptAutoResume, and the next
// iteration's ctx check fires with a live, no-longer-parked seed.
func TestAutonomousResume_CancelInClaimWindowReparks(t *testing.T) {
	eng, stub, _ := buildGuardTestEngine(t, 0, 1, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{cancelProbeToolCall("c1")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c2")}}, // attempt 2 — must never be reached
	})
	// Autonomous resume must be ON (default) so the wrapper claims the
	// StepCap park via attemptAutoResume instead of surfacing it.
	eng.Config.Agent.AutonomousResume = "auto"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A backend tool that cancels ctx *during* its own execution in the
	// first round. The round still completes and parks for StepCap (its
	// trace is recorded, so this is NOT the empty-traces interrupt path);
	// the cancel only bites at the top of the SECOND loop attempt — after
	// attemptAutoResume has claimed the parked seed out of the engine but
	// before the loop re-enters. That is exactly the claim->re-enter
	// window the fix protects.
	eng.Tools.Register(&ctxCancelTool{cancel: cancel})

	_, err := eng.AskWithMetadata(ctx, "cancel-window check")
	if err == nil {
		t.Fatal("expected a context-cancellation error from the interrupted run")
	}
	// The core invariant: accumulated work survives the cancel as a
	// parked agent the user can /continue, rather than vanishing.
	if !eng.HasParkedAgent() {
		t.Fatal("seed claimed by attemptAutoResume must be re-parked when ctx is cancelled in the claim->re-enter window")
	}
	// The second scripted response must NOT have been consumed — the
	// loop bailed before re-entering runNativeToolLoop.
	stub.mu.Lock()
	remaining := len(stub.responses)
	stub.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("expected the second round to be unreached (1 response left), got %d left", remaining)
	}
}

// ctxCancelTool is a test backend tool that cancels a captured context
// the first time it executes, then reports success. Used to drive a
// deterministic mid-loop cancellation that lands in the autonomous
// resume claim->re-enter window.
type ctxCancelTool struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	fired  bool
}

func (c *ctxCancelTool) Name() string        { return "cancel_probe" }
func (c *ctxCancelTool) Description() string { return "test-only: cancels ctx on first execution" }
func (c *ctxCancelTool) Execute(_ context.Context, _ tools.Request) (tools.Result, error) {
	c.mu.Lock()
	first := !c.fired
	c.fired = true
	c.mu.Unlock()
	if first && c.cancel != nil {
		c.cancel()
	}
	return tools.Result{Output: "cancel_probe ok"}, nil
}

func cancelProbeToolCall(id string) provider.ToolCall {
	return provider.ToolCall{
		ID:   id,
		Name: "tool_call",
		Input: toolCallInput(map[string]any{
			"name": "cancel_probe",
			"args": map[string]any{},
		}),
	}
}

// TestNativeToolCallReadsSecretFile pins the file-name secret gate that
// backs the tool:result preview redaction (#M3). Pattern-based event
// redaction only catches recognized key shapes; this gate keys off the
// FILE NAME so custom-format secrets in a classified file are withheld.
func TestNativeToolCallReadsSecretFile(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		input    map[string]any
		want     bool
	}{
		{"direct read_file .env", "read_file", map[string]any{"path": ".env"}, true},
		{"direct read_file normal", "read_file", map[string]any{"path": "main.go"}, false},
		{"meta tool_call credentials", "tool_call", map[string]any{"name": "read_file", "args": map[string]any{"path": "config/credentials.json"}}, true},
		{"meta tool_call normal", "tool_call", map[string]any{"name": "read_file", "args": map[string]any{"path": "README.md"}}, false},
		{"batch one secret member", "tool_batch_call", map[string]any{"calls": []any{
			map[string]any{"name": "read_file", "args": map[string]any{"path": "a.go"}},
			map[string]any{"name": "read_file", "args": map[string]any{"path": "id_rsa"}},
		}}, true},
		{"non-read tool on secret path", "tool_call", map[string]any{"name": "grep_codebase", "args": map[string]any{"path": ".env"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nativeToolCallReadsSecretFile(c.toolName, c.input); got != c.want {
				t.Fatalf("nativeToolCallReadsSecretFile(%q, …) = %v, want %v", c.toolName, got, c.want)
			}
		})
	}
}

// TestToolResultEvent_RedactsSecretFilePreview confirms the gate is
// wired into the live tool:result publisher: a read of a .env carrying
// a custom-shaped value (which no redaction PATTERN would catch) must
// not leak that value into the event stream subscribers consume.
func TestToolResultEvent_RedactsSecretFilePreview(t *testing.T) {
	eng, _, evCh := buildGuardTestEngine(t, 0, 1, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{readFileToolCall("c1", ".env")}},
	})
	eng.Config.Agent.AutonomousResume = "off"
	// A value whose shape matches none of the redaction patterns, so the
	// only thing that can withhold it is the file-name gate.
	const rawSecret = "INTERNAL_DB_PASSWORD=hunter2-custom-shape-no-pattern"
	if err := os.WriteFile(filepath.Join(eng.ProjectRoot, ".env"), []byte(rawSecret+"\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	if _, err := eng.AskWithMetadata(context.Background(), "read the env file"); err != nil {
		t.Fatalf("ask: %v", err)
	}

	events := collectRecentEvents(evCh, 128, 250*time.Millisecond)
	ev, ok := findEventByType(events, "tool:result")
	if !ok {
		t.Fatal("expected a tool:result event")
	}
	payload, ok := ev.Payload.(map[string]any)
	if !ok {
		t.Fatalf("tool:result payload must be a map, got %T", ev.Payload)
	}
	preview, _ := payload["output_preview"].(string)
	if !strings.Contains(preview, "REDACTED") {
		t.Fatalf("secret-file preview must be redacted, got %q", preview)
	}
	if strings.Contains(preview, "hunter2") {
		t.Fatalf("raw secret leaked into the event preview: %q", preview)
	}
	if payload["redacted_secret_file"] != true {
		t.Fatalf("redacted_secret_file flag must be set, payload=%v", payload)
	}
}

func readFileToolCall(id, path string) provider.ToolCall {
	return provider.ToolCall{
		ID:   id,
		Name: "tool_call",
		Input: toolCallInput(map[string]any{
			"name": "read_file",
			"args": map[string]any{"path": path},
		}),
	}
}

// Sanity: a resume with no parked state returns the existing "no
// parked agent" error unchanged. The ceiling check must NOT fire here
// because seed is nil.
func TestResumeAgent_NoParkedStateStillErrors(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 1, nil)
	_, err := eng.ResumeAgent(context.Background(), "")
	if err == nil || !errors.Is(err, ErrNoParkedAgent) {
		t.Fatalf("want 'no parked agent' error, got %v", err)
	}
}

func TestRunSubagentRejectsUnknownProfileOverride(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 2, []scriptedResponse{{Text: "unused"}})
	_, err := eng.RunSubagent(context.Background(), tools.SubagentRequest{
		Task:  "inspect note.txt",
		Model: "does-not-exist",
	})
	if err == nil {
		t.Fatal("unknown sub-agent profile override must error")
	}
	if !strings.Contains(err.Error(), "unknown sub-agent model/profile override") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Dead-code: symbols inside raw-string literals --------------------

// The regex-fallback AST happily picks identifiers out of anywhere,
// including `const foo = ...` embedded in a Go raw-string literal
// that serves a JS/HTML payload to a browser. Those aren't real Go
// symbols — they're text. The detector must strip strings/comments
// BEFORE ranking symbols and skip any symbol whose declaration line
// doesn't look like a real decl in the host language.
//
// The bug surfaced on ui/web/server.go, which embeds sizeable JS
// payloads for the SSE/remote-control front-ends. Pre-fix, `wrapper`,
// `perEvent`, `providerNames`, and others surfaced as "dead Go code."
func TestDetectDeadCode_IgnoresSymbolsInsideRawStrings(t *testing.T) {
	dir := t.TempDir()
	// A real unused Go symbol (must appear) plus a raw string that
	// embeds JS with decl-shaped lines (must NOT appear).
	src := "package p\n\n" +
		"func unusedReal() {}\n\n" +
		"var jsPayload = `\n" +
		"<script>\n" +
		"const wrapper = () => {};\n" +
		"let perEvent = 0;\n" +
		"function providerNames() { return []; }\n" +
		"</script>\n" +
		"`\n\n" +
		"// jsPayload is held so it isn't itself dead.\n" +
		"var _ = jsPayload\n"
	path := filepath.Join(dir, "server.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	e := &Engine{AST: ast.New()}
	items, err := e.detectDeadCode(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("detectDeadCode: %v", err)
	}

	// unusedReal is the only legitimate dead symbol; the JS names
	// must not appear.
	gotNames := make(map[string]bool, len(items))
	for _, it := range items {
		gotNames[it.Name] = true
	}
	for _, banned := range []string{"wrapper", "perEvent", "providerNames"} {
		if gotNames[banned] {
			t.Fatalf("symbol %q comes from inside a Go raw-string literal; must not surface as dead code. Items: %+v", banned, items)
		}
	}
	if !gotNames["unusedReal"] {
		t.Fatalf("real unused Go func should still be flagged; items: %+v", items)
	}
}
