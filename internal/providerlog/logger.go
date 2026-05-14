// Package providerlog persists every provider:complete event to a
// daily-rotated JSONL file under <data-dir>/provider_calls/. It captures
// the user→assistant turn signal that the in-memory event bus broadcasts
// — provider name, model, input/output token counts, and the
// conversation hook (user prompt + assistant text snippet) — so a
// session that crashes, gets compacted, or rolls past the visible
// transcript still leaves a durable audit trail.
//
// Sibling of internal/toolhistory: same shape (bus → buffered append →
// daily JSONL), different event channel. The two are intentionally
// separate files so a tool-call burst doesn't crowd out the
// conversation-level rollup, and so a future viewer can correlate them
// by timestamp without parsing through unrelated entries.
//
// Persistence target: ~/.dfmc/data/provider_calls/{YYYY-MM-DD}.jsonl
// when the engine is wired with a config DataDir; daily rotation keeps
// any single file under a few MB even for heavy days.
package providerlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record is the JSONL log entry shape. Keep field names stable —
// downstream viewers and external tooling parse this directly.
type Record struct {
	TS               string `json:"ts"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int    `json:"input_tokens,omitempty"`
	OutputTokens     int    `json:"output_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	UserPreview      string `json:"user_preview,omitempty"`
	AssistantText    string `json:"assistant_text,omitempty"`
	AssistantPreview string `json:"assistant_preview,omitempty"`
	Source           string `json:"source,omitempty"` // "ask" / "agent_loop" / "stream"
	DurationMs       int    `json:"duration_ms,omitempty"`
	Error            string `json:"error,omitempty"`
}

// Logger records provider:complete events to a daily JSONL file.
// Buffered behind a mutex; flushes on every record (provider calls are
// rare enough vs tool calls that batching adds latency without saving
// meaningful I/O).
type Logger struct {
	dir          string
	mu           sync.Mutex
	previewLimit int
}

// New constructs a logger that writes to <artifactsDir>/provider_calls/.
// The engine wires the event subscription separately to avoid an import
// cycle (engine imports providerlog; the bus's Event type lives in
// engine). Callers feed events in via Record(payload).
//
// Returns nil,nil for an empty path so callers can drop the logger
// entirely when persistence isn't configured.
func New(artifactsDir string) (*Logger, error) {
	if artifactsDir == "" {
		return nil, nil
	}
	dir := filepath.Join(artifactsDir, "provider_calls")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Logger{
		dir:          dir,
		previewLimit: 240,
	}, nil
}

// Record consumes one provider:complete event payload and persists it.
// Safe on nil receiver and unrecognised shapes; meaningless events are
// silently dropped.
func (l *Logger) Record(payload any) {
	if l == nil {
		return
	}
	p, _ := payload.(map[string]any)
	if p == nil {
		return
	}
	rec := Record{
		TS:               time.Now().UTC().Format(time.RFC3339Nano),
		Provider:         strVal(p, "provider"),
		Model:            strVal(p, "model"),
		InputTokens:      intVal(p, "input_tokens"),
		OutputTokens:     intVal(p, "output_tokens"),
		TotalTokens:      intVal(p, "total_tokens"),
		Source:           strVal(p, "source"),
		DurationMs:       intVal(p, "duration_ms"),
		UserPreview:      truncate(strVal(p, "user_preview"), l.previewLimit),
		AssistantPreview: truncate(strVal(p, "assistant_preview"), l.previewLimit),
		AssistantText:    strVal(p, "assistant_text"),
		Error:            strVal(p, "error"),
	}
	if rec.TotalTokens == 0 {
		rec.TotalTokens = intVal(p, "tokens")
	}
	if rec.Provider == "" && rec.Model == "" && rec.TotalTokens == 0 {
		// Nothing useful — silently skip rather than write a noise row.
		return
	}
	l.write(rec)
}

func (l *Logger) write(rec Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(l.dir, date+".jsonl")
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(line)
	_, _ = f.Write([]byte("\n"))
	_ = f.Close()
}

// Close is a no-op kept for API symmetry with toolhistory. The
// subscription is owned by the engine (which calls Record directly),
// so there's nothing for the logger itself to release.
func (l *Logger) Close() error { return nil }

// Tail returns the last n records from today's log file, newest last.
// Returns nil for an unset logger or a missing file. Errors are
// swallowed (best-effort viewer surface, not an audit endpoint).
func (l *Logger) Tail(n int) []Record {
	if l == nil || n <= 0 {
		return nil
	}
	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(l.dir, date+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var all []Record
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		all = append(all, rec)
	}
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}

// Dir returns the directory provider call logs are written to. Empty
// when the logger isn't initialized — callers can use this to surface
// "where do I find the archive?" in /status.
func (l *Logger) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

func splitLines(data []byte) [][]byte {
	out := make([][]byte, 0)
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

func strVal(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func intVal(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
