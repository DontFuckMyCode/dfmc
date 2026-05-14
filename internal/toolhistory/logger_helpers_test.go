package toolhistory

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// --- strVal / intVal / boolVal ---

func TestStrVal(t *testing.T) {
	m := map[string]any{"key": "value", "missing": 123}
	if got := strVal(m, "key"); got != "value" {
		t.Errorf("strVal(key)=%q, want value", got)
	}
	if got := strVal(m, "missing"); got != "" {
		t.Errorf("strVal(missing)=%q, want empty", got)
	}
	if got := strVal(m, "absent"); got != "" {
		t.Errorf("strVal(absent)=%q, want empty", got)
	}
}

func TestIntVal(t *testing.T) {
	m := map[string]any{
		"float": float64(42),
		"int":   7,
		"str":   "not-an-int",
	}
	if got := intVal(m, "float"); got != 42 {
		t.Errorf("intVal(float)=%d, want 42", got)
	}
	if got := intVal(m, "int"); got != 7 {
		t.Errorf("intVal(int)=%d, want 7", got)
	}
	if got := intVal(m, "str"); got != 0 {
		t.Errorf("intVal(str)=%d, want 0", got)
	}
	if got := intVal(m, "absent"); got != 0 {
		t.Errorf("intVal(absent)=%d, want 0", got)
	}
}

func TestBoolVal(t *testing.T) {
	m := map[string]any{"ok": true, "fail": false, "str": "no"}
	if got := boolVal(m, "ok"); !got {
		t.Error("boolVal(ok) should be true")
	}
	if got := boolVal(m, "fail"); got {
		t.Error("boolVal(fail) should be false")
	}
	if got := boolVal(m, "str"); got {
		t.Error("boolVal(str) should be false")
	}
	if got := boolVal(m, "absent"); got {
		t.Error("boolVal(absent) should be false")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		s, want string
		max     int
	}{
		{"short", "short", 10},
		{"exactly", "exactly", 7},
		{"toolong", "to...", 5},
		{"abc", "...", 2},
		{"", "", 5},
		// truncate caps total length at max; prefix is max-3 chars
		// followed by the 3-char ellipsis. "hello world" at max=5
		// yields the first two chars + "..." → "he...".
		{"hello world", "he...", 5},
	}
	for _, c := range cases {
		got := truncate(c.s, c.max)
		if got != c.want {
			t.Errorf("truncate(%q, %d)=%q, want %q", c.s, c.max, got, c.want)
		}
	}
}

// --- writeFileAtomic ---

func TestWriteFileAtomic_WritesAndRenames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	data := []byte(`{"ts":"2025-01-01T00:00:00Z"}`)

	err := writeFileAtomic(path, data, "th-")
	if err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after write failed: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("file content = %q, want %q", string(got), string(data))
	}
}

func TestWriteFileAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overwrite.jsonl")

	old := []byte(`{"old":true}`)
	new := []byte(`{"new":true}`)

	if err := writeFileAtomic(path, old, "th-"); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := writeFileAtomic(path, new, "th-"); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(new) {
		t.Errorf("after overwrite content = %q, want %q", string(got), string(new))
	}
}

func TestWriteFileAtomic_NonExistentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "dir", "file.txt")
	err := writeFileAtomic(path, []byte("data"), "th-")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

// --- syncDir ---

func TestSyncDir_ValidDir(t *testing.T) {
	dir := t.TempDir()
	if err := syncDir(dir); err != nil {
		t.Fatalf("syncDir failed on valid dir: %v", err)
	}
}

func TestSyncDir_NonexistentDir(t *testing.T) {
	err := syncDir(filepath.Join(t.TempDir(), "gone"))
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

// --- periodicFlush ---

func TestPeriodicFlush_IdleFlush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("syncDir calls os.Chmod which is unavailable on Windows")
	}

	dir := t.TempDir()
	l := &Logger{
		dir:        dir,
		flushEvery: 100,
		idleDur:    20 * time.Millisecond,
		lastFlush:  time.Now().Add(-time.Hour),
		stopCh:     make(chan struct{}),
	}
	l.push(ToolCallRecord{TS: time.Now().UTC().Format(time.RFC3339), Type: "call"})

	l.mu.Lock()
	bufWasNonEmpty := len(l.buf) > 0
	l.mu.Unlock()
	if !bufWasNonEmpty {
		t.Fatal("buffer should have one record before periodicFlush starts")
	}

	go l.periodicFlush()
	time.Sleep(80 * time.Millisecond)
	l.Close()

	l.mu.Lock()
	bufEmpty := len(l.buf) == 0
	l.mu.Unlock()
	if !bufEmpty {
		t.Error("buffer should be empty after Close (which flushes)")
	}

	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(dir, date+".jsonl")
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("periodicFlush should have written jsonl file: %v", err)
	}
}

// --- subscribePayload / payloadFromEventValue ---

type mockBus struct{ called bool }

func (m *mockBus) SubscribeFunc(event string, fn func(any)) func() {
	m.called = true
	fn(map[string]any{"provider": "test"})
	return func() {}
}

func TestSubscribePayload_DirectInterface(t *testing.T) {
	bus := &mockBus{}
	called := false
	fn := func(payload any) { called = true }

	unsubscribe := subscribePayload(bus, "test:event", fn)

	if !bus.called {
		t.Error("bus should have been called via direct interface")
	}
	if !called {
		t.Error("fn should have been called with payload")
	}
	unsubscribe()
}

func TestSubscribePayload_NilBus(t *testing.T) {
	fn := func(payload any) {}
	unsubscribe := subscribePayload(nil, "any", fn)
	unsubscribe()
}

func TestSubscribePayload_NilFn(t *testing.T) {
	bus := &mockBus{}
	unsubscribe := subscribePayload(bus, "any", nil)
	unsubscribe()
}

func TestPayloadFromEventValue(t *testing.T) {
	type payloadCarrier struct{ Payload any }

	// Pointer to struct with Payload field
	v := reflect.ValueOf(&payloadCarrier{Payload: map[string]any{"key": "val"}})
	got := payloadFromEventValue(v)
	if m, ok := got.(map[string]any); !ok || m["key"] != "val" {
		t.Errorf("expected Payload map, got %v", got)
	}

	// Nil pointer
	var nilPtr *payloadCarrier
	if got := payloadFromEventValue(reflect.ValueOf(nilPtr)); got != nil {
		t.Errorf("nil pointer should yield nil, got %v", got)
	}

	// Plain map (no wrapper)
	m := map[string]any{"plain": true}
	if got := payloadFromEventValue(reflect.ValueOf(m)); got == nil {
		t.Error("plain map should be returned as-is")
	}

	// Invalid reflect.Value
	if got := payloadFromEventValue(reflect.Value{}); got != nil {
		t.Errorf("invalid reflect.Value should yield nil, got %v", got)
	}
}
