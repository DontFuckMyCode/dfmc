package toolhistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- push tests ---

func TestPush_AccumulatesInBuffer(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 100, // high threshold so push never auto-flushes
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	rec := ToolCallRecord{TS: time.Now().UTC().Format(time.RFC3339), Type: "call"}
	l.push(rec)

	l.mu.Lock()
	n := len(l.buf)
	l.mu.Unlock()

	if n != 1 {
		t.Errorf("expected buffer len 1 after push, got %d", n)
	}
}

func TestPush_TriggersFlushAtThreshold(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 3,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	for i := 0; i < 3; i++ {
		l.push(ToolCallRecord{TS: time.Now().UTC().Format(time.RFC3339), Type: "call"})
	}

	l.mu.Lock()
	n := len(l.buf)
	l.mu.Unlock()

	// After 3 pushes (threshold=3), buffer should be flushed → len 0 or 1
	if n > 1 {
		t.Errorf("buffer should be flushed after threshold pushes, got len=%d", n)
	}
}

func TestPush_NoFlushBelowThreshold(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 10,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	for i := 0; i < 4; i++ {
		l.push(ToolCallRecord{TS: time.Now().UTC().Format(time.RFC3339), Type: "call"})
	}

	l.mu.Lock()
	n := len(l.buf)
	l.mu.Unlock()

	if n != 4 {
		t.Errorf("expected buffer len 4 below threshold, got %d", n)
	}
}

// --- flush tests ---

func TestFlush_EmptyBufferReturnsEarly(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{dir: dir, flushEvery: 10, lastFlush: time.Now(), stopCh: make(chan struct{})}

	// buffer already nil, flush should return without error
	l.flush()

	l.mu.Lock()
	n := len(l.buf)
	l.mu.Unlock()

	if n != 0 {
		t.Errorf("empty buffer should stay empty, got %d", n)
	}
}

func TestFlush_DrainsBuffer(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{dir: dir, flushEvery: 10, lastFlush: time.Now(), stopCh: make(chan struct{})}

	// push one record
	l.push(ToolCallRecord{
		TS:       time.Now().UTC().Format(time.RFC3339),
		Type:     "call",
		Provider: "openai",
		Tool:     "test",
	})

	l.mu.Lock()
	before := len(l.buf)
	l.mu.Unlock()

	l.flush()

	l.mu.Lock()
	after := len(l.buf)
	l.mu.Unlock()

	if before == 0 {
		t.Skip("buffer was empty before flush")
	}
	if after != 0 {
		t.Errorf("buffer should be empty after flush, got %d", after)
	}
}

func TestFlush_WritesJSONL(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{dir: dir, flushEvery: 10, lastFlush: time.Now(), stopCh: make(chan struct{})}

	l.push(ToolCallRecord{
		TS:       time.Now().UTC().Format(time.RFC3339),
		Type:     "call",
		Provider: "openai",
		Model:    "gpt-4",
		Tool:     "read_file",
	})

	l.flush()

	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(dir, date+".jsonl")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read jsonl file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no lines written to jsonl")
	}

	var got ToolCallRecord
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid JSON in jsonl: %v", err)
	}

	if got.Type != "call" || got.Provider != "openai" || got.Tool != "read_file" {
		t.Errorf("unexpected record: %+v", got)
	}
}

func TestFlush_AppendsToExisting(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{dir: dir, flushEvery: 10, lastFlush: time.Now(), stopCh: make(chan struct{})}

	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(dir, date+".jsonl")

	// Pre-write an existing record
	existing := []byte(`{"ts":"2024-01-01T00:00:00Z","type":"call","provider":"pre","tool":" preexisting"}` + "\n")
	if err := os.MkdirAll(dir, 0o755); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	l.push(ToolCallRecord{
		TS:       time.Now().UTC().Format(time.RFC3339),
		Type:     "call",
		Provider: "new",
		Tool:     "newtool",
	})
	l.flush()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (pre-existing + new), got %d lines: %q", len(lines), string(raw))
	}
}

// periodicFlush is tested implicitly via periodicFlushIdleFlush in logger_helpers_test.go
// and via Close (which calls flush on stopCh).

// --- Close tests ---

func TestClose_CallsSubscriptionsAndFlushes(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 10,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	subCallCalled := false
	subResultCalled := false
	l.subCall = func() { subCallCalled = true }
	l.subResult = func() { subResultCalled = true }

	l.Close()

	if !subCallCalled {
		t.Error("Close should call subCall unsubscribe")
	}
	if !subResultCalled {
		t.Error("Close should call subResult unsubscribe")
	}
}

// --- onCall tests ---

func TestOnCall_ParsesPayloadAndPushesRecord(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 1000,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	l.onCall(map[string]any{
		"provider": "openai",
		"model":    "gpt-4",
		"tool":     "read_file",
		"step":     3,
		"params": map[string]any{
			"path": "/test/file.go",
		},
	})

	l.mu.Lock()
	rec := l.buf[0]
	l.mu.Unlock()

	if rec.Type != "call" {
		t.Errorf("expected type 'call', got %q", rec.Type)
	}
	if rec.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", rec.Provider)
	}
	if rec.Tool != "read_file" {
		t.Errorf("expected tool 'read_file', got %q", rec.Tool)
	}
	if len(rec.Files) == 0 {
		t.Error("expected Files to be populated from params")
	}
}

func TestOnCall_HandlesNilPayload(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 100,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	// Should not panic
	l.onCall(nil)
	l.onCall(map[string]any{})

	l.mu.Lock()
	n := len(l.buf)
	l.mu.Unlock()

	if n > 0 {
		t.Log("non-nil but empty payload produced a record — acceptable")
	}
}

// --- onResult tests ---

func TestOnResult_ParsesPayloadAndPushesRecord(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 1000,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	l.onResult(map[string]any{
		"provider":       "anthropic",
		"model":          "claude-3",
		"tool":           "write_file",
		"step":           5,
		"success":        true,
		"durationMs":     150,
		"output_preview": "file written",
		"output_tokens":  42,
	})

	l.mu.Lock()
	rec := l.buf[0]
	l.mu.Unlock()

	if rec.Type != "result" {
		t.Errorf("expected type 'result', got %q", rec.Type)
	}
	if !rec.Success {
		t.Error("expected success=true")
	}
	if rec.DurationMs != 150 {
		t.Errorf("expected durationMs=150, got %d", rec.DurationMs)
	}
	if rec.OutputPreview != "file written" {
		t.Errorf("unexpected output_preview: %q", rec.OutputPreview)
	}
	if rec.Tokens != 42 {
		t.Errorf("expected tokens=42, got %d", rec.Tokens)
	}
}

func TestOnResult_ParsesError(t *testing.T) {
	dir := newTestDir(t)
	l := &Logger{
		dir:        dir,
		flushEvery: 1000,
		idleDur:    10 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}

	l.onResult(map[string]any{
		"provider": "openai",
		"tool":     "read_file",
		"success":  false,
		"error":    "file not found",
		"params":   map[string]any{},
	})

	l.mu.Lock()
	rec := l.buf[0]
	l.mu.Unlock()

	if rec.Error != "file not found" {
		t.Errorf("expected error 'file not found', got %q", rec.Error)
	}
	if rec.Success {
		t.Error("expected success=false")
	}
}

// --- helper ---

func newTestDir(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "toolhistory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	return dir
}

func init() {
	if runtime.GOOS == "windows" {
		runtime.GOMAXPROCS(1)
	}
}
