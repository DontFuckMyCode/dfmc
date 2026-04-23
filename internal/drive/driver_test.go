// Driver tests use a fake Runner — no real provider, no real engine.
// That keeps the tests fast and deterministic, and lets us pin the
// exact behavior the CLI/TUI depends on (event ordering, retry
// counts, brief stitching, deadline enforcement).

package drive

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// fakeRunner is the test double for drive.Runner. PlanFunc and
// ExecFunc are programmable per-test; both default to "succeed with
// trivial output" so tests only set what they care about.
type fakeRunner struct {
	mu sync.Mutex

	// PlanFunc returns a JSON envelope (the planner LLM "raw" text).
	// Tests override to inject malformed JSON, cycles, etc.
	PlanFunc func(req PlannerRequest) (string, error)

	// ExecFunc is called per TODO. Tests override to fail specific
	// TODOs or assert the brief seeded into req.
	ExecFunc func(req ExecuteTodoRequest) (ExecuteTodoResponse, error)

	// Calls log every ExecuteTodo invocation in order, so tests can
	// assert the scheduler walked TODOs in the expected sequence.
	Calls []ExecuteTodoRequest

	// AutoApprove* fields capture BeginAutoApprove invocations so
	// tests can assert the driver activated/released the scope as
	// expected. Started fires on the call; Released fires when the
	// returned closure runs.
	AutoApproveStarted  bool
	AutoApproveTools    []string
	AutoApproveReleased bool
}

func (f *fakeRunner) PlannerCall(_ context.Context, req PlannerRequest) (PlannerResponse, error) {
	if f.PlanFunc != nil {
		text, err := f.PlanFunc(req)
		return PlannerResponse{Text: text, Provider: "fake", Model: "fake-model"}, err
	}
	// Default: 2 trivial sequential TODOs.
	return PlannerResponse{
		Text: `{"todos":[
			{"id":"T1","title":"first","detail":"do first thing"},
			{"id":"T2","title":"second","detail":"do second thing","depends_on":["T1"]}
		]}`,
		Provider: "fake",
		Model:    "fake-model",
	}, nil
}

func (f *fakeRunner) ExecuteTodo(_ context.Context, req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, req)
	f.mu.Unlock()
	if f.ExecFunc != nil {
		return f.ExecFunc(req)
	}
	return ExecuteTodoResponse{
		Summary:    "did " + req.TodoID,
		ToolCalls:  1,
		DurationMs: 10,
	}, nil
}

// BeginAutoApprove records the requested allow list and returns a
// release closure that flips the recorded state back. Tests inspect
// AutoApproveStarted / AutoApproveTools / AutoApproveReleased to
// verify the driver activated and released the scope correctly.
func (f *fakeRunner) BeginAutoApprove(tools []string) func() {
	f.mu.Lock()
	f.AutoApproveStarted = true
	f.AutoApproveTools = append([]string(nil), tools...)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.AutoApproveReleased = true
		f.mu.Unlock()
	}
}

// captureEvents returns a Publisher that appends every event into the
// provided slice. Tests assert event ordering / count from this.
func captureEvents(events *[]string, mu *sync.Mutex) Publisher {
	return func(typ string, _ map[string]any) {
		mu.Lock()
		*events = append(*events, typ)
		mu.Unlock()
	}
}

func captureEventPayloads(payloads *[]map[string]any, events *[]string, mu *sync.Mutex) Publisher {
	return func(typ string, payload map[string]any) {
		mu.Lock()
		*events = append(*events, typ)
		clone := make(map[string]any, len(payload)+1)
		clone["_type"] = typ
		for k, v := range payload {
			clone[k] = v
		}
		*payloads = append(*payloads, clone)
		mu.Unlock()
	}
}

func TestDriverRunsHappyPathSequentially(t *testing.T) {
	runner := &fakeRunner{}
	var events []string
	var mu sync.Mutex
	d := NewDriver(runner, nil, captureEvents(&events, &mu), Config{})

	run, err := d.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s (reason=%q)", run.Status, run.Reason)
	}
	done, blocked, skipped, _ := run.Counts()
	if done != 2 || blocked != 0 || skipped != 0 {
		t.Fatalf("expected 2 done, got done=%d blocked=%d skipped=%d", done, blocked, skipped)
	}

	// Scheduler must walk T1 before T2 (T2 depends on T1).
	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 ExecuteTodo calls, got %d", len(runner.Calls))
	}
	if runner.Calls[0].TodoID != "T1" || runner.Calls[1].TodoID != "T2" {
		t.Fatalf("expected T1 then T2, got %v", []string{runner.Calls[0].TodoID, runner.Calls[1].TodoID})
	}

	// T2 must see T1's brief in its prompt context.
	if !strings.Contains(runner.Calls[1].Brief, "T1") || !strings.Contains(runner.Calls[1].Brief, "did T1") {
		t.Fatalf("T2 brief missing T1 summary: %q", runner.Calls[1].Brief)
	}

	// Events must include start, plan, two todo:start/done, run:done.
	wantSubstr := []string{
		EventRunStart,
		EventPlanStart,
		EventPlanDone,
		EventTodoStart, EventTodoDone,
		EventTodoStart, EventTodoDone,
		EventRunDone,
	}
	for i, w := range wantSubstr {
		if i >= len(events) || events[i] != w {
			t.Fatalf("event[%d]: want %q, got %q (full=%v)", i, w, safeIdx(events, i), events)
		}
	}
}

func TestNewRunRejectsBlankTaskWithActionableError(t *testing.T) {
	_, err := NewRun(" \n\t ")
	if err == nil {
		t.Fatal("expected blank task to fail")
	}
	if !strings.Contains(err.Error(), "non-empty task description") {
		t.Fatalf("expected actionable empty-task error, got %v", err)
	}
}

func TestRunPreparedRejectsBlankTaskWithActionableError(t *testing.T) {
	d := NewDriver(&fakeRunner{}, nil, nil, Config{})
	_, err := d.RunPrepared(context.Background(), &Run{ID: "run_test", Task: " \n "})
	if err == nil {
		t.Fatal("expected blank task to fail")
	}
	if !strings.Contains(err.Error(), "non-empty task description") {
		t.Fatalf("expected actionable empty-task error, got %v", err)
	}
}

func TestDriverRetryThenBlocks(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"only","detail":"do it"}]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			return ExecuteTodoResponse{}, errors.New("simulated failure")
		},
	}
	var events []string
	var mu sync.Mutex
	d := NewDriver(runner, nil, captureEvents(&events, &mu), Config{Retries: 1})

	run, _ := d.Run(context.Background(), "task")
	// One retry => 2 attempts, then blocked.
	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 attempts (initial + 1 retry), got %d", len(runner.Calls))
	}
	if run.Todos[0].Status != TodoBlocked {
		t.Fatalf("expected blocked, got %s", run.Todos[0].Status)
	}
	if run.Todos[0].Attempts != 2 {
		t.Fatalf("expected 2 attempts logged, got %d", run.Todos[0].Attempts)
	}
	// Run terminates with RunDone (single TODO blocked, no consecutive
	// blocks limit hit because MaxFailedTodos default is 3 and we only
	// blocked 1).
	if run.Status != RunDone {
		t.Fatalf("with 1 blocked TODO and MaxFailedTodos=3, expected RunDone, got %s", run.Status)
	}

	// Must have exactly one EventTodoRetry between the two attempts.
	retries := 0
	for _, e := range events {
		if e == EventTodoRetry {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("expected 1 retry event, got %d (events=%v)", retries, events)
	}
}

func TestDriverMaxFailedTodosFails(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a"},
				{"id":"T2","title":"b","detail":"b"},
				{"id":"T3","title":"c","detail":"c"}
			]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			return ExecuteTodoResponse{}, errors.New("always fail")
		},
	}
	d := NewDriver(runner, nil, nil, Config{Retries: 0, MaxFailedTodos: 2})
	run, _ := d.Run(context.Background(), "doomed task")
	if run.Status != RunFailed {
		t.Fatalf("expected RunFailed after 2 consecutive blocks, got %s (reason=%q)", run.Status, run.Reason)
	}
	if !strings.Contains(run.Reason, "max_failed_todos") {
		t.Fatalf("expected reason to cite max_failed_todos, got %q", run.Reason)
	}
}

func TestDriverDependencyChainSkipsDescendants(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			// T1 -> T2 -> T3, plus an independent T4 that should still run.
			return `{"todos":[
				{"id":"T1","title":"root","detail":"do root"},
				{"id":"T2","title":"mid","detail":"do mid","depends_on":["T1"]},
				{"id":"T3","title":"leaf","detail":"do leaf","depends_on":["T2"]},
				{"id":"T4","title":"indep","detail":"unrelated"}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				return ExecuteTodoResponse{}, errors.New("T1 fails hard")
			}
			return ExecuteTodoResponse{Summary: "ok"}, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{Retries: 0, MaxFailedTodos: 5})
	run, _ := d.Run(context.Background(), "chain task")

	statusOf := func(id string) TodoStatus {
		for _, t := range run.Todos {
			if t.ID == id {
				return t.Status
			}
		}
		return ""
	}
	if statusOf("T1") != TodoBlocked {
		t.Fatalf("T1 must be blocked, got %s", statusOf("T1"))
	}
	if statusOf("T2") != TodoSkipped {
		t.Fatalf("T2 must be skipped (T1 blocked), got %s", statusOf("T2"))
	}
	if statusOf("T3") != TodoSkipped {
		t.Fatalf("T3 must be skipped (T2 skipped), got %s", statusOf("T3"))
	}
	if statusOf("T4") != TodoDone {
		t.Fatalf("T4 must run independently, got %s", statusOf("T4"))
	}
}

func TestDriverPlannerErrorFailsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return "", errors.New("upstream model timeout")
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err == nil {
		t.Fatal("expected error on planner failure")
	}
	if run.Status != RunFailed {
		t.Fatalf("expected RunFailed, got %s", run.Status)
	}
	if !strings.Contains(run.Reason, "plan failed") {
		t.Fatalf("reason should mention plan failure, got %q", run.Reason)
	}
}

func TestDriverContextCancelStopsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a"},
				{"id":"T2","title":"b","detail":"b"}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				return ExecuteTodoResponse{Summary: "ok"}, nil
			}
			return ExecuteTodoResponse{}, errors.New("never reached")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDriver(runner, nil, nil, Config{})
	// Cancel after the first TODO completes by hooking into ExecFunc.
	runner.ExecFunc = func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
		if req.TodoID == "T1" {
			cancel()
			return ExecuteTodoResponse{Summary: "ok"}, nil
		}
		return ExecuteTodoResponse{Summary: "shouldnt"}, nil
	}
	run, _ := d.Run(ctx, "task")
	if run.Status != RunStopped {
		t.Fatalf("expected RunStopped on ctx cancel, got %s", run.Status)
	}
	// T1 done, T2 should remain Pending (not Running).
	statusByID := map[string]TodoStatus{}
	for _, t := range run.Todos {
		statusByID[t.ID] = t.Status
	}
	if statusByID["T1"] != TodoDone {
		t.Fatalf("T1 must be Done after cancel, got %s", statusByID["T1"])
	}
	if statusByID["T2"] != TodoPending {
		t.Fatalf("T2 must remain Pending after cancel, got %s", statusByID["T2"])
	}
}

func TestDriverDeadlineStopsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"slow","detail":"slow"},
				{"id":"T2","title":"never","detail":"unreached"}
			]}`, nil
		},
		ExecFunc: func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			if req.TodoID == "T1" {
				time.Sleep(50 * time.Millisecond) // pushes us past the deadline
				return ExecuteTodoResponse{Summary: "ok"}, nil
			}
			return ExecuteTodoResponse{}, fmt.Errorf("should not reach T2")
		},
	}
	d := NewDriver(runner, nil, nil, Config{MaxWallTime: 10 * time.Millisecond})
	run, _ := d.Run(context.Background(), "task")
	if run.Status != RunStopped {
		t.Fatalf("expected RunStopped on deadline, got %s (reason=%q)", run.Status, run.Reason)
	}
	if !strings.Contains(run.Reason, "max_wall_time") {
		t.Fatalf("expected reason to cite max_wall_time, got %q", run.Reason)
	}
}

func TestDrainAndFinalize_UsesConfigurableGraceWindow(t *testing.T) {
	d := NewDriver(&fakeRunner{}, nil, nil, Config{DrainGraceWindow: 5 * time.Millisecond})
	run := &Run{
		ID:        "drv-grace-timeout",
		Task:      "task",
		Status:    RunRunning,
		CreatedAt: time.Now(),
		Todos: []Todo{
			{ID: "T1", Title: "slow", Detail: "wait", Status: TodoRunning, StartedAt: time.Now()},
		},
	}
	results := make(chan todoOutcome)
	start := time.Now()
	d.drainAndFinalize(context.Background(), run, results, 1, RunStopped, "ctx cancelled")
	elapsed := time.Since(start)
	if run.Status != RunStopped {
		t.Fatalf("expected RunStopped after grace timeout, got %s", run.Status)
	}
	if elapsed < 4*time.Millisecond {
		t.Fatalf("expected drain to honor configured grace window, elapsed=%s", elapsed)
	}
	if run.Todos[0].Status != TodoRunning {
		t.Fatalf("timeout path should leave in-flight todo running for resume, got %+v", run.Todos[0])
	}
}

func TestDrainAndFinalize_DrainsResultWithinGraceWindow(t *testing.T) {
	d := NewDriver(&fakeRunner{}, nil, nil, Config{DrainGraceWindow: 50 * time.Millisecond})
	run := &Run{
		ID:        "drv-grace-drain",
		Task:      "task",
		Status:    RunRunning,
		CreatedAt: time.Now(),
		Todos: []Todo{
			{ID: "T1", Title: "slow", Detail: "wait", Status: TodoRunning, StartedAt: time.Now(), Attempts: 1},
		},
	}
	results := make(chan todoOutcome, 1)
	go func() {
		time.Sleep(5 * time.Millisecond)
		results <- todoOutcome{
			Idx:    0,
			TodoID: "T1",
			Resp: ExecuteTodoResponse{
				Summary:    "done",
				ToolCalls:  1,
				DurationMs: 5,
			},
			Started: run.Todos[0].StartedAt,
			Ended:   time.Now(),
			Attempt: 1,
		}
	}()
	d.drainAndFinalize(context.Background(), run, results, 1, RunStopped, "ctx cancelled")
	if run.Status != RunStopped {
		t.Fatalf("expected RunStopped after drain, got %s", run.Status)
	}
	if run.Todos[0].Status != TodoDone {
		t.Fatalf("expected in-flight result to be applied within grace window, got %+v", run.Todos[0])
	}
	if run.Todos[0].Brief != "done" {
		t.Fatalf("expected drained summary applied to brief, got %+v", run.Todos[0])
	}
}

func TestDriverPanicInExecuteTodoBecomesBlockedOutcome(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"panic","detail":"boom"}]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			panic("boom")
		},
	}
	d := NewDriver(runner, nil, nil, Config{Retries: 0})
	run, _ := d.Run(context.Background(), "panic task")
	if len(run.Todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(run.Todos))
	}
	if run.Todos[0].Status != TodoBlocked {
		t.Fatalf("expected blocked todo after panic, got %s", run.Todos[0].Status)
	}
	if !strings.Contains(run.Todos[0].Error, "todo panic") {
		t.Fatalf("expected panic text in todo error, got %q", run.Todos[0].Error)
	}
}

func TestDriverRunPreparedPanicPersistsFailedRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			panic("planner exploded")
		},
	}
	store := newTestStore(t)
	d := NewDriver(runner, store, nil, Config{})

	run, err := d.RunPrepared(context.Background(), &Run{ID: "panic-run", Task: "test panic persistence"})
	if err == nil || !strings.Contains(err.Error(), "drive run panic") {
		t.Fatalf("expected recovered panic error, got %v", err)
	}
	if run == nil || run.Status != RunFailed {
		t.Fatalf("expected failed run returned after panic, got %#v", run)
	}
	if !strings.Contains(run.Reason, "planner exploded") {
		t.Fatalf("expected panic reason to be recorded, got %q", run.Reason)
	}
	persisted, lerr := store.Load("panic-run")
	if lerr != nil {
		t.Fatalf("load persisted run: %v", lerr)
	}
	if persisted == nil || persisted.Status != RunFailed {
		t.Fatalf("expected failed run persisted after panic, got %#v", persisted)
	}
	if persisted.EndedAt.IsZero() {
		t.Fatalf("expected persisted terminal timestamp, got %#v", persisted)
	}
}

func TestPlannerRejectsCycles(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"A","title":"a","detail":"a","depends_on":["B"]},
				{"id":"B","title":"b","detail":"b","depends_on":["A"]}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err == nil {
		t.Fatal("expected error on cyclic plan")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
	if run.Status != RunFailed {
		t.Fatalf("expected RunFailed, got %s", run.Status)
	}
}

func TestPlannerRejectsUnknownDep(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"a","detail":"a","depends_on":["GHOST"]}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	_, err := d.Run(context.Background(), "task")
	if err == nil || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("expected unknown-dep error, got: %v", err)
	}
}

func TestPlannerStripsCodeFences(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return "```json\n{\"todos\":[{\"id\":\"T1\",\"title\":\"a\",\"detail\":\"a\"}]}\n```", nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("fenced JSON should parse, got: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
}

func TestPlannerParsesRichTaskMetadata(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{
					"id":"T1",
					"title":"Audit auth boundary",
					"detail":"Inspect auth middleware and verify token validation.",
					"provider_tag":"review",
					"worker_class":"security",
					"read_only": true,
					"skills":["audit","debug","audit"],
					"allowed_tools":["read_file","grep_codebase","read_file"],
					"labels":["auth","critical"],
					"verification":"deep",
					"confidence":0.91
				}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "audit auth")
	if err != nil {
		t.Fatalf("run should succeed, got %v", err)
	}
	if len(run.Todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(run.Todos))
	}
	got := run.Todos[0]
	if got.WorkerClass != "security" {
		t.Fatalf("worker_class not parsed: %+v", got)
	}
	if got.Verification != "deep" {
		t.Fatalf("verification not parsed: %+v", got)
	}
	if !got.ReadOnly {
		t.Fatalf("read_only not parsed: %+v", got)
	}
	if got.Confidence != 0.91 {
		t.Fatalf("confidence not parsed: %+v", got)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "audit" || got.Skills[1] != "debug" {
		t.Fatalf("skills not normalized: %+v", got.Skills)
	}
	if len(got.AllowedTools) != 2 {
		t.Fatalf("allowed_tools not normalized: %+v", got.AllowedTools)
	}
}

func TestDriverDispatchesExecutionHintsToRunner(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{
					"id":"T1",
					"title":"Review token flow",
					"detail":"Inspect token parsing and expiry checks.",
					"provider_tag":"review",
					"worker_class":"reviewer",
					"skills":["review","audit"],
					"allowed_tools":["read_file","grep_codebase"],
					"labels":["auth"],
					"verification":"required"
				}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "review auth")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 ExecuteTodo call, got %d", len(runner.Calls))
	}
	call := runner.Calls[0]
	if call.Role != "code_reviewer" {
		t.Fatalf("expected reviewer role, got %+v", call)
	}
	if len(call.Skills) != 2 || call.Skills[0] != "review" || call.Skills[1] != "audit" {
		t.Fatalf("skills missing from ExecuteTodoRequest: %+v", call)
	}
	if len(call.AllowedTools) != 2 || call.AllowedTools[0] != "read_file" {
		t.Fatalf("allowed_tools missing from ExecuteTodoRequest: %+v", call)
	}
	if call.Verification != "required" {
		t.Fatalf("verification missing from ExecuteTodoRequest: %+v", call)
	}
	if call.MaxSteps != 7 {
		t.Fatalf("expected review-oriented worker to receive bounded step budget, got %+v", call)
	}
}

func TestDriverPublishesFallbackReasonsOnTodoDone(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"Review token flow","detail":"Inspect token parsing."}]}`, nil
		},
		ExecFunc: func(_ ExecuteTodoRequest) (ExecuteTodoResponse, error) {
			return ExecuteTodoResponse{
				Summary:         "ok",
				Attempts:        2,
				FallbackUsed:    true,
				FallbackFrom:    "anthropic-review",
				FallbackChain:   []string{"anthropic-review", "openai-fast"},
				FallbackReasons: []string{"provider timeout"},
				Provider:        "openai-fast",
				Model:           "gpt-5.4-mini",
			}, nil
		},
	}
	var events []string
	var payloads []map[string]any
	var mu sync.Mutex
	d := NewDriver(runner, nil, captureEventPayloads(&payloads, &events, &mu), Config{})

	run, err := d.Run(context.Background(), "review auth")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected RunDone, got %s", run.Status)
	}
	var donePayload map[string]any
	for _, payload := range payloads {
		if payload["_type"] == EventTodoDone {
			donePayload = payload
			break
		}
	}
	if donePayload == nil {
		t.Fatalf("expected %s payload, got %#v", EventTodoDone, payloads)
	}
	if got, _ := donePayload["fallback_reasons"].([]string); !reflect.DeepEqual(got, []string{"provider timeout"}) {
		t.Fatalf("expected fallback reasons in todo done payload, got %+v", donePayload["fallback_reasons"])
	}
}

func TestExecutorStepBudgetForWorkerLanes(t *testing.T) {
	tests := []struct {
		name string
		todo Todo
		want int
	}{
		{name: "discovery", todo: Todo{WorkerClass: "researcher"}, want: 6},
		{name: "review", todo: Todo{WorkerClass: "reviewer"}, want: 7},
		{name: "verify", todo: Todo{WorkerClass: "tester", Kind: "verify"}, want: 8},
		{name: "deep verify", todo: Todo{WorkerClass: "security", Verification: "deep"}, want: 10},
		{name: "wide code", todo: Todo{WorkerClass: "coder", FileScope: []string{"a.go", "b.go", "c.go"}}, want: 14},
		{name: "default code", todo: Todo{WorkerClass: "coder", FileScope: []string{"a.go"}}, want: 12},
	}
	for _, tc := range tests {
		if got := executorStepBudgetFor(tc.todo); got != tc.want {
			t.Fatalf("%s: want %d, got %d", tc.name, tc.want, got)
		}
	}
}

func TestSynthesizeVerificationTodo_AddsSupervisorVerifier(t *testing.T) {
	todos := []Todo{
		{
			ID:           "T1",
			Title:        "Patch auth refresh",
			Detail:       "Update refresh flow and add token guard.",
			ProviderTag:  "code",
			WorkerClass:  "coder",
			FileScope:    []string{"internal/auth/service.go"},
			Verification: "required",
		},
		{
			ID:           "T2",
			Title:        "Review auth boundary",
			Detail:       "Inspect authz rules and token validation.",
			ProviderTag:  "review",
			WorkerClass:  "security",
			FileScope:    []string{"internal/auth/middleware.go"},
			Skills:       []string{"audit"},
			Verification: "deep",
		},
	}
	got := synthesizeVerificationTodo(todos)
	if got == nil {
		t.Fatal("expected synthesized verifier")
	}
	if got.Origin != "supervisor" || got.Kind != "verify" {
		t.Fatalf("unexpected synthesized todo metadata: %+v", got)
	}
	if got.ProviderTag != "review" || got.WorkerClass != "security" {
		t.Fatalf("deep/security verification should pick review/security: %+v", got)
	}
	if len(got.DependsOn) != 2 || got.DependsOn[0] != "T1" || got.DependsOn[1] != "T2" {
		t.Fatalf("unexpected verifier deps: %+v", got.DependsOn)
	}
	if !containsStringFold(got.Skills, "audit") {
		t.Fatalf("expected audit skill in synthesized verifier: %+v", got.Skills)
	}
}

func TestSynthesizeVerificationTodo_SkipsWhenVerifierAlreadyExists(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Patch auth refresh", WorkerClass: "coder", Verification: "required"},
		{ID: "T2", Title: "Run verification pass", WorkerClass: "tester", ProviderTag: "test", Kind: "verify", Verification: "required"},
	}
	if got := synthesizeVerificationTodo(todos); got != nil {
		t.Fatalf("expected nil when verifier already exists, got %+v", got)
	}
}

func TestDriverAutoVerify_AppendsAndRunsVerifierLast(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"Patch auth refresh","detail":"Update refresh flow.","provider_tag":"code","worker_class":"coder","verification":"required"},
				{"id":"T2","title":"Document follow-up","detail":"Write a short note.","provider_tag":"plan","worker_class":"planner","verification":"light"}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{AutoVerify: true})
	run, err := d.Run(context.Background(), "ship auth fix")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Todos) != 3 {
		t.Fatalf("expected verifier todo appended, got %d todos", len(run.Todos))
	}
	last := run.Todos[2]
	if last.Origin != "supervisor" || last.Kind != "verify" {
		t.Fatalf("expected appended supervisor verify todo, got %+v", last)
	}
	if len(runner.Calls) != 3 || runner.Calls[2].TodoID != last.ID {
		t.Fatalf("expected verifier to run last, calls=%+v", runner.Calls)
	}
	if run.Plan == nil || run.Plan.VerificationID != last.ID {
		t.Fatalf("expected run plan snapshot with verification id, got %+v", run.Plan)
	}
	if len(run.Plan.Layers) == 0 {
		t.Fatalf("expected plan layers to be recorded, got %+v", run.Plan)
	}
}

func TestApplySupervisorPlan_StoresLayeredSnapshot(t *testing.T) {
	run := &Run{
		ID:     "drv-1",
		Task:   "ship auth fix",
		Status: RunPlanning,
		Todos: []Todo{
			{ID: "T1", Title: "survey", WorkerClass: "researcher", Verification: "light", Status: TodoPending},
			{ID: "T2", Title: "patch", DependsOn: []string{"T1"}, WorkerClass: "coder", Verification: "required", Status: TodoPending},
			{ID: "T3", Title: "review", DependsOn: []string{"T2"}, WorkerClass: "reviewer", Verification: "required", Status: TodoPending},
		},
	}
	applySupervisorPlan(run, false, true, 3)
	if run.Plan == nil {
		t.Fatal("expected plan snapshot to be stored")
	}
	if len(run.Plan.Layers) < 3 {
		t.Fatalf("expected layered plan, got %+v", run.Plan)
	}
	if run.Plan.MaxParallel != 3 {
		t.Fatalf("expected max_parallel in plan snapshot, got %+v", run.Plan)
	}
	if run.Plan.WorkerCounts["coder"] != 1 {
		t.Fatalf("expected worker counts captured, got %+v", run.Plan)
	}
	if run.Plan.LaneCaps["code"] != 3 {
		t.Fatalf("expected lane caps captured, got %+v", run.Plan)
	}
	if len(run.Plan.LaneOrder) == 0 || run.Plan.LaneOrder[0] != "discovery" {
		t.Fatalf("expected lane order captured, got %+v", run.Plan)
	}
}

func TestApplySupervisorPlan_AutoSurveyPrependsDiscoveryTask(t *testing.T) {
	run := &Run{
		ID:     "drv-survey",
		Task:   "ship auth fix",
		Status: RunPlanning,
		Todos: []Todo{
			{ID: "T1", Title: "patch", WorkerClass: "coder", Verification: "required", Status: TodoPending},
			{ID: "T2", Title: "review", DependsOn: []string{"T1"}, WorkerClass: "reviewer", Verification: "required", Status: TodoPending},
		},
	}
	applySupervisorPlan(run, true, false, 2)
	if len(run.Todos) != 3 {
		t.Fatalf("expected survey task to be prepended, got %d todos", len(run.Todos))
	}
	if run.Todos[0].ID != "S1" || run.Todos[0].WorkerClass != "researcher" {
		t.Fatalf("unexpected survey todo: %+v", run.Todos[0])
	}
	if run.Plan == nil || run.Plan.SurveyID != "S1" {
		t.Fatalf("expected survey id in plan snapshot, got %+v", run.Plan)
	}
	if len(run.Todos[1].DependsOn) == 0 || run.Todos[1].DependsOn[0] != "S1" {
		t.Fatalf("expected original root to depend on synthesized survey task: %+v", run.Todos[1])
	}
}

func TestApplySupervisorPlan_PreservesExistingTodoRuntimeState(t *testing.T) {
	startedAt := time.Now().Add(-2 * time.Minute).UTC()
	run := &Run{
		ID:     "drv-merge",
		Task:   "ship auth fix",
		Status: RunRunning,
		Todos: []Todo{
			{
				ID:           "T1",
				Origin:       "worker",
				Kind:         "analysis",
				Title:        "Patch auth refresh",
				Detail:       "Update refresh flow.",
				WorkerClass:  "coder",
				Verification: "required",
				Status:       TodoRunning,
				Brief:        "in progress",
				Error:        "transient lint failure",
				Attempts:     2,
				StartedAt:    startedAt,
			},
		},
	}

	applySupervisorPlan(run, false, true, 2)
	if len(run.Todos) != 2 {
		t.Fatalf("expected original todo plus synthesized verifier, got %+v", run.Todos)
	}
	got := run.Todos[0]
	if got.ID != "T1" {
		t.Fatalf("expected original todo to remain first, got %+v", run.Todos)
	}
	if got.Origin != "worker" || got.Kind != "analysis" {
		t.Fatalf("expected runtime origin/kind to be preserved, got %+v", got)
	}
	if got.Status != TodoRunning || got.Brief != "in progress" || got.Attempts != 2 {
		t.Fatalf("expected runtime status fields preserved, got %+v", got)
	}
	if !got.StartedAt.Equal(startedAt) {
		t.Fatalf("expected StartedAt preserved, got %+v", got)
	}
	if run.Plan == nil || run.Plan.VerificationID == "" {
		t.Fatalf("expected synthesized verifier in plan snapshot, got %+v", run.Plan)
	}
}

func TestDriverAutoSurvey_PrependsAndRunsSurveyFirst(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"Patch auth refresh","detail":"Update refresh flow.","provider_tag":"code","worker_class":"coder","verification":"required"},
				{"id":"T2","title":"Review refresh logic","detail":"Inspect behavior changes.","provider_tag":"review","worker_class":"reviewer","verification":"required","depends_on":["T1"]}
			]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{AutoSurvey: true})
	run, err := d.Run(context.Background(), "ship auth fix")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Todos) != 3 {
		t.Fatalf("expected survey todo appended, got %d todos", len(run.Todos))
	}
	if run.Todos[0].ID != "S1" {
		t.Fatalf("expected survey todo first, got %+v", run.Todos)
	}
	if len(runner.Calls) == 0 || runner.Calls[0].TodoID != "S1" {
		t.Fatalf("expected survey to run first, got calls=%+v", runner.Calls)
	}
}

func containsStringFold(items []string, needle string) bool {
	for _, item := range items {
		if strings.EqualFold(item, needle) {
			return true
		}
	}
	return false
}

func TestDriverRunPreparedRejectsNilContext(t *testing.T) {
	runner := &fakeRunner{}
	d := NewDriver(runner, nil, nil, Config{})
	//nolint:staticcheck // intentional nil to verify rejection
	run, err := d.RunPrepared(nil, &Run{ID: "r1", Task: "test"})
	if err == nil {
		t.Fatal("expected nil-context error")
	}
	if run != nil {
		t.Fatalf("expected nil run on error, got %#v", run)
	}
	if !strings.Contains(err.Error(), "context must not be nil") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDriverResumeRejectsNilContext(t *testing.T) {
	d := NewDriver(&fakeRunner{}, nil, nil, Config{})
	//nolint:staticcheck // intentional nil to verify rejection
	run, err := d.Resume(nil, "run-1")
	if err == nil {
		t.Fatal("expected nil-context error")
	}
	if run != nil {
		t.Fatalf("expected nil run on error, got %#v", run)
	}
	if !strings.Contains(err.Error(), "context must not be nil") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDriverResumeRejectsAlreadyActiveRun(t *testing.T) {
	runner := &fakeRunner{}
	store := newTestStore(t)
	run := &Run{
		ID:        "drv-active",
		Task:      "active task",
		Status:    RunRunning,
		CreatedAt: time.Now(),
		Todos:     []Todo{{ID: "T1", Title: "one", Status: TodoRunning}},
	}
	if err := store.Save(run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	_, cancel := context.WithCancel(context.Background())
	register(run.ID, run.Task, cancel)
	defer unregister(run.ID)
	defer cancel()

	d := NewDriver(runner, store, nil, Config{})
	got, err := d.Resume(context.Background(), run.ID)
	if err == nil {
		t.Fatal("expected active-run error")
	}
	if got == nil || got.ID != run.ID {
		t.Fatalf("expected loaded run in error path, got %#v", got)
	}
	if !strings.Contains(err.Error(), "already active") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDriverPersistFailurePublishesWarningEvent(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[{"id":"T1","title":"one","detail":"do it"}]}`, nil
		},
	}
	store := newTestStore(t)
	if err := store.db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	var events []string
	var mu sync.Mutex
	d := NewDriver(runner, store, captureEvents(&events, &mu), Config{})

	run, err := d.Run(context.Background(), "task with broken persistence")
	if err != nil {
		t.Fatalf("run should continue despite persist failure, got: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("expected run to complete despite persist failure, got %s", run.Status)
	}
	found := false
	for _, ev := range events {
		if ev == EventRunWarning {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s event in %v", EventRunWarning, events)
	}
}

// When the planner returns more TODOs than MaxTodos, the driver should
// truncate and surface the cap as a RUN WARNING — not as a plan
// failure. Consumers (TUI/CLI) that gate on drive:plan:failed as a
// terminal signal were tripping on what was really a "heads-up, some
// TODOs got dropped" message.
func TestDriverTruncateMaxTodosEmitsWarningNotPlanFailed(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"one","detail":"."},
				{"id":"T2","title":"two","detail":"."},
				{"id":"T3","title":"three","detail":"."},
				{"id":"T4","title":"four","detail":"."}
			]}`, nil
		},
	}
	var events []string
	var mu sync.Mutex
	d := NewDriver(runner, nil, captureEvents(&events, &mu), Config{MaxTodos: 2, MaxParallel: 1})
	run, err := d.Run(context.Background(), "too many todos")
	if err != nil {
		t.Fatalf("truncation should not fail the run: %v", err)
	}
	if run.Status != RunDone {
		t.Fatalf("run should complete despite truncation, got %s", run.Status)
	}
	if len(run.Todos) != 2 {
		t.Fatalf("MaxTodos=2 should cap todos to 2, got %d", len(run.Todos))
	}
	sawWarning := false
	for _, ev := range events {
		if ev == EventPlanFailed {
			t.Fatalf("truncation must not emit EventPlanFailed; events=%v", events)
		}
		if ev == EventRunWarning {
			sawWarning = true
		}
	}
	if !sawWarning {
		t.Fatalf("truncation should emit EventRunWarning; events=%v", events)
	}
}

func TestPlannerPicksFirstValidEnvelopeFromMultipleJSONObjects(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"thoughts":"draft"} {"todos":[{"id":"T1","title":"read","detail":"inspect files"}]}`, nil
		},
	}
	d := NewDriver(runner, nil, nil, Config{})
	run, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("expected second JSON object to parse, got %v", err)
	}
	if len(run.Todos) != 1 || run.Todos[0].ID != "T1" {
		t.Fatalf("unexpected todos: %+v", run.Todos)
	}
}

func safeIdx(s []string, i int) string {
	if i < 0 || i >= len(s) {
		return "<missing>"
	}
	return s[i]
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := bbolt.Open(dir+"/drive.db", 0o600, nil)
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}
