// Tests for the MCP-side drive bridge. These cover the static surface
// (descriptors are well-formed, names are stable, schemas validate) and
// the dispatch contracts (missing run_id is a tool-level error not a
// transport error, list/active emit [] not null, status of a missing
// run is a tool-level error). End-to-end "actually run a planner" is
// not exercised here — that lives in internal/drive's driver tests
// against fakeRunner. The point of this layer's tests is the wire
// shape an IDE host will see.

package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// TestDriveMCPHandlerExposesExpectedTools pins the catalog. If a tool
// is renamed or removed the descriptor list changes — that's a public
// API break for IDE hosts, so it MUST surface as a test failure.
func TestDriveMCPHandlerExposesExpectedTools(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	tools := h.Tools()
	want := []string{
		"dfmc_drive_start",
		"dfmc_drive_status",
		"dfmc_drive_active",
		"dfmc_drive_list",
		"dfmc_drive_stop",
		"dfmc_drive_resume",
	}
	if len(tools) != len(want) {
		t.Fatalf("expected %d tools, got %d", len(want), len(tools))
	}
	for i, tool := range tools {
		if tool.Name != want[i] {
			t.Errorf("tool[%d]: want %q got %q", i, want[i], tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description — IDE hosts render this", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil input schema — host argument pickers need it", tool.Name)
		}
		if got := tool.InputSchema["type"]; got != "object" {
			t.Errorf("tool %q schema.type = %v, want \"object\"", tool.Name, got)
		}
	}
}

// TestDriveMCPHandlerHandlesPrefix gates routing in the parent bridge
// (engineMCPBridge.Call). A bug here would silently send drive calls
// through engine.CallTool which would fail with "unknown tool" instead
// of dispatching properly.
func TestDriveMCPHandlerHandlesPrefix(t *testing.T) {
	h := &driveMCPHandler{}
	if !h.Handles("dfmc_drive_start") {
		t.Error("Handles must accept dfmc_drive_start")
	}
	if !h.Handles("dfmc_drive_status") {
		t.Error("Handles must accept dfmc_drive_status")
	}
	if h.Handles("read_file") {
		t.Error("Handles must NOT claim regular tools")
	}
	if h.Handles("drive_start") {
		t.Error("Handles must require the dfmc_ prefix to avoid collisions")
	}
	if h.Handles("") {
		t.Error("Handles must not match empty name")
	}
}

// TestDriveMCPCallStartRejectsMissingTask: passing no task surfaces a
// tool-level error (IsError:true with a hint), NOT a transport error.
// This matches the missingParamError pattern used everywhere else.
func TestDriveMCPCallStartRejectsMissingTask(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	res, err := h.Call(context.Background(), "dfmc_drive_start", []byte(`{}`))
	if err != nil {
		t.Fatalf("Call returned transport error %v; want tool-level error", err)
	}
	if !res.IsError {
		t.Fatal("missing task must return IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "task is required") {
		t.Errorf("error text must mention task; got %q", res.Content[0].Text)
	}
	// The hint must include a literal example so the model can
	// self-correct on the next call (matches missingParamError style).
	if !strings.Contains(res.Content[0].Text, `{"task":`) {
		t.Errorf("error text must include canonical example; got %q", res.Content[0].Text)
	}
}

// TestDriveMCPCallStartRejectsBlankTask: whitespace-only task is just
// as bad as missing — same recovery hint.
func TestDriveMCPCallStartRejectsBlankTask(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_drive_start", []byte(`{"task":"   "}`))
	if !res.IsError {
		t.Fatal("blank task must return IsError:true")
	}
}

// TestDriveMCPCallStatusRequiresRunID: status with no run_id surfaces
// the canonical "run_id is required" hint with the example shape.
func TestDriveMCPCallStatusRequiresRunID(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_drive_status", []byte(`{}`))
	if !res.IsError {
		t.Fatal("missing run_id must return IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "run_id is required") {
		t.Errorf("hint must mention run_id; got %q", res.Content[0].Text)
	}
}

// TestDriveMCPCallStatusMissingRunErrors: status of a non-existent ID
// returns a tool-level error so the host can show "no such run" — NOT
// a 200 with empty body which would be confusing.
func TestDriveMCPCallStatusMissingRunErrors(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_drive_status", []byte(`{"run_id":"drv-does-not-exist"}`))
	if !res.IsError {
		t.Fatal("missing run must return IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "not found") {
		t.Errorf("error text must say 'not found'; got %q", res.Content[0].Text)
	}
}

// TestDriveMCPCallActiveEmptyReturnsArray: empty active list serializes
// as JSON [] not null, mirroring the HTTP /drive/active contract so
// hosts can iterate without nil-checking.
func TestDriveMCPCallActiveEmptyReturnsArray(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	res, err := h.Call(context.Background(), "dfmc_drive_active", nil)
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("active call must succeed; got error: %s", res.Content[0].Text)
	}
	body := strings.TrimSpace(res.Content[0].Text)
	if body == "null" {
		t.Fatal("empty active list must serialize as [] not null")
	}
	if !strings.HasPrefix(body, "[") {
		t.Errorf("expected JSON array, got %q", body)
	}
}

// TestDriveMCPCallListAcceptsNullArguments: MCP allows tools/call with
// arguments=null when there are no required fields. decodeOrEmpty must
// treat that the same as {} — anything else breaks well-behaved hosts.
func TestDriveMCPCallListAcceptsNullArguments(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	for _, args := range [][]byte{nil, []byte(""), []byte("null"), []byte(`{}`)} {
		res, err := h.Call(context.Background(), "dfmc_drive_list", args)
		if err != nil {
			t.Fatalf("args %q: transport error %v", string(args), err)
		}
		if res.IsError {
			t.Fatalf("args %q: unexpected tool error %s", string(args), res.Content[0].Text)
		}
		body := strings.TrimSpace(res.Content[0].Text)
		if !strings.HasPrefix(body, "[") {
			t.Errorf("args %q: expected JSON array, got %q", string(args), body)
		}
	}
}

// TestDriveMCPCallStopOnNonActiveRunErrors: stop of an ID that isn't
// active in this process surfaces a tool-level error pointing the host
// at status — same hint as the HTTP layer.
func TestDriveMCPCallStopOnNonActiveRunErrors(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_drive_stop", []byte(`{"run_id":"drv-not-active"}`))
	if !res.IsError {
		t.Fatal("stop of non-active run must return IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "no active drive run") {
		t.Errorf("hint must say 'no active drive run'; got %q", res.Content[0].Text)
	}
}

// TestDriveMCPCallResumeOnTerminalRunRefuses: resuming a Done/Failed
// run is a host bug (resume should only be used on stopped/in-progress
// runs). We refuse with a tool error so the host doesn't silently
// kick off a no-op.
func TestDriveMCPCallResumeOnTerminalRunRefuses(t *testing.T) {
	eng := newCLITestEngine(t)
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	seedID := "drv-mcp-terminal"
	if err := store.Save(&drive.Run{ID: seedID, Task: "x", Status: drive.RunDone}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &driveMCPHandler{eng: eng}
	res, _ := h.Call(context.Background(), "dfmc_drive_resume", []byte(`{"run_id":"`+seedID+`"}`))
	if !res.IsError {
		t.Fatal("resume of terminal run must return IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "already terminal") {
		t.Errorf("hint must say 'already terminal'; got %q", res.Content[0].Text)
	}
}

// TestDriveMCPCallStatusReturnsSeededRun: round-trip — seed a run via
// the underlying store, fetch it back through the MCP tool, verify
// the JSON shape preserves ID/task/status/todos. Catches any field
// drift between the wire shape and drive.Run.
func TestDriveMCPCallStatusReturnsSeededRun(t *testing.T) {
	eng := newCLITestEngine(t)
	store, _ := drive.NewStore(eng.Storage.DB())
	seedID := "drv-mcp-show"
	seed := &drive.Run{
		ID:     seedID,
		Task:   "round-trip task",
		Status: drive.RunDone,
		Todos: []drive.Todo{
			{ID: "T1", Title: "first", Status: drive.TodoDone, Brief: "did the thing"},
		},
	}
	if err := store.Save(seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	h := &driveMCPHandler{eng: eng}
	res, err := h.Call(context.Background(), "dfmc_drive_status", []byte(`{"run_id":"`+seedID+`"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("status of seeded run must succeed; got %s", res.Content[0].Text)
	}
	var got drive.Run
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatalf("decode result: %v\nbody: %s", err, res.Content[0].Text)
	}
	if got.ID != seedID || got.Task != "round-trip task" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Todos) != 1 || got.Todos[0].Brief != "did the thing" {
		t.Errorf("todos round-trip failed: %+v", got.Todos)
	}
}

// TestEngineMCPBridgeMergesDriveTools: the parent bridge's List() must
// concatenate regular backend tools with the synthetic drive tools.
// A regression here would break tools/list for IDE hosts, hiding the
// drive surface entirely.
func TestEngineMCPBridgeMergesDriveTools(t *testing.T) {
	eng := newCLITestEngine(t)
	bridge := &engineMCPBridge{eng: eng, drive: &driveMCPHandler{eng: eng}}
	tools := bridge.List()
	if len(tools) == 0 {
		t.Fatal("bridge.List returned no tools")
	}
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		names[t.Name] = true
	}
	for _, want := range []string{"dfmc_drive_start", "dfmc_drive_status", "dfmc_drive_active"} {
		if !names[want] {
			t.Errorf("bridge.List missing %q", want)
		}
	}
	// A regular tool must still be present — drive merge must not
	// replace the registry.
	if !names["read_file"] {
		t.Error("bridge.List must still expose regular tools (read_file missing)")
	}
}

// TestEngineMCPBridgeRoutesDriveCallsToHandler: a Call to a
// dfmc_drive_* name must reach driveMCPHandler.Call, NOT engine.CallTool.
// Probe via the missing-task error which only the drive handler emits.
func TestEngineMCPBridgeRoutesDriveCallsToHandler(t *testing.T) {
	eng := newCLITestEngine(t)
	bridge := &engineMCPBridge{eng: eng, drive: &driveMCPHandler{eng: eng}}
	res, err := bridge.Call(context.Background(), "dfmc_drive_start", []byte(`{}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error from drive handler routing")
	}
	if !strings.Contains(res.Content[0].Text, "task is required") {
		t.Errorf("expected drive handler error text; got %q", res.Content[0].Text)
	}
}
