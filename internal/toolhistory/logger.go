package toolhistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Bus subscription bridge (subscribePayload + payloadFromEventValue),
// payload value coercers (strVal/intVal/boolVal/truncate), and the
// atomic file writer (writeFileAtomic + syncDir) live in
// logger_helpers.go.

// ToolCallRecord is the JSONL log entry shape.
type ToolCallRecord struct {
	TS            string         `json:"ts"`
	Type          string         `json:"type"` // "call" or "result"
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	Tool          string         `json:"tool"`
	Step          int            `json:"step,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	Success       bool           `json:"success,omitempty"`
	DurationMs    int            `json:"duration_ms,omitempty"`
	OutputPreview string         `json:"output_preview,omitempty"`
	Files         []string       `json:"files,omitempty"`
	Tokens        int            `json:"tokens,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// Logger records tool calls and results to a daily JSONL file.
type Logger struct {
	dir        string
	flushEvery int
	idleDur    time.Duration
	mu         sync.Mutex
	buf        []ToolCallRecord
	lastFlush  time.Time
	stopCh     chan struct{}
	subCall    func()
	subResult  func()
}

var pathKeys = []string{"path", "file", "target", "paths", "dir", "repo", "source", "destination"}

// extractFiles walks params for path-like values.
func extractFiles(params map[string]any) []string {
	var out []string
	for _, k := range pathKeys {
		if v, ok := params[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			} else if arr, ok := v.([]any); ok {
				for _, e := range arr {
					if s, ok := e.(string); ok && s != "" {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}

// Init wires the logger to the engine event bus. The bus parameter is
// typed as any to avoid importing engine — the caller (engine.go) passes
// e.EventBus which has SubscribeFunc(string, func(engine.Event)).
func Init(bus any, artifactsDir string) (*Logger, error) {
	if bus == nil || artifactsDir == "" {
		return nil, nil
	}
	dir := filepath.Join(artifactsDir, "toolcalls")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Logger{
		dir:        dir,
		flushEvery: 10,
		idleDur:    5 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}
	l.subCall = subscribePayload(bus, "tool:call", l.onCall)
	l.subResult = subscribePayload(bus, "tool:result", l.onResult)
	go l.periodicFlush()
	return l, nil
}

func (l *Logger) onCall(payload any) {
	p, _ := payload.(map[string]any)
	rec := ToolCallRecord{
		TS:       time.Now().UTC().Format(time.RFC3339),
		Type:     "call",
		Provider: strVal(p, "provider"),
		Model:    strVal(p, "model"),
		Tool:     strVal(p, "tool"),
		Step:     intVal(p, "step"),
	}
	if p2, ok := payload.(map[string]any); ok {
		if p3, ok := p2["params"].(map[string]any); ok {
			rec.Params = p3
			rec.Files = extractFiles(p3)
		}
	}
	l.push(rec)
}

func (l *Logger) onResult(payload any) {
	p, _ := payload.(map[string]any)
	rec := ToolCallRecord{
		TS:            time.Now().UTC().Format(time.RFC3339),
		Type:          "result",
		Provider:      strVal(p, "provider"),
		Model:         strVal(p, "model"),
		Tool:          strVal(p, "tool"),
		Step:          intVal(p, "step"),
		Success:       boolVal(p, "success"),
		DurationMs:    intVal(p, "durationMs"),
		OutputPreview: truncate(strVal(p, "output_preview"), 200),
		Tokens:        intVal(p, "output_tokens"),
	}
	if p2, ok := payload.(map[string]any); ok {
		if p3, ok := p2["params"].(map[string]any); ok {
			rec.Params = p3
			rec.Files = extractFiles(p3)
		}
		if err := strVal(p2, "error"); err != "" {
			rec.Error = err
		}
	}
	l.push(rec)
}

func (l *Logger) push(rec ToolCallRecord) {
	l.mu.Lock()
	l.buf = append(l.buf, rec)
	needFlush := len(l.buf) >= l.flushEvery
	l.mu.Unlock()
	if needFlush {
		l.flush()
	}
}

func (l *Logger) flush() {
	l.mu.Lock()
	if len(l.buf) == 0 {
		l.mu.Unlock()
		return
	}
	recs := l.buf
	l.buf = nil
	l.lastFlush = time.Now()
	l.mu.Unlock()

	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(l.dir, date+".jsonl")
	var lines []string
	for _, r := range recs {
		if b, err := json.Marshal(r); err == nil {
			lines = append(lines, string(b))
		}
	}
	var existing []byte
	if raw, err := os.ReadFile(path); err == nil {
		existing = raw
	}
	full := existing
	for _, ln := range lines {
		full = append(full, ln...)
		full = append(full, '\n')
	}
	_ = writeFileAtomic(path, full, "toolhistory")
}

func (l *Logger) periodicFlush() {
	ticker := time.NewTicker(l.idleDur)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			dirty := len(l.buf) > 0 && time.Since(l.lastFlush) >= l.idleDur
			l.mu.Unlock()
			if dirty {
				l.flush()
			}
		case <-l.stopCh:
			l.flush()
			return
		}
	}
}

// Close stops the logger and flushes pending records.
func (l *Logger) Close() {
	if l.subCall != nil {
		l.subCall()
	}
	if l.subResult != nil {
		l.subResult()
	}
	close(l.stopCh)
}
