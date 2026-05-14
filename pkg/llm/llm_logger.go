package llm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogEntry is the JSONL record shape for LLM calls.
type LogEntry struct {
	Timestamp    string         `json:"ts"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	Type         string         `json:"type"` // "request" or "response"
	DurationMs   int            `json:"duration_ms,omitempty"`
	InputTokens  int            `json:"input_tokens,omitempty"`
	OutputTokens int            `json:"output_tokens,omitempty"`
	TotalTokens  int            `json:"total_tokens,omitempty"`
	Success      bool           `json:"success"`
	Error        string         `json:"error,omitempty"`
	Messages     int            `json:"messages,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// Logger writes LLM call records to a daily JSONL file.
type Logger struct {
	dir        string
	flushEvery int
	idleDur    time.Duration
	mu         sync.Mutex
	buf        []LogEntry
	lastFlush  time.Time
	stopCh     chan struct{}
}

// NewLogger returns a Logger that writes to <artifactsDir>/llmcalls/<date>.jsonl.
// Pass an empty string to create a no-op logger.
func NewLogger(artifactsDir string) (*Logger, error) {
	if artifactsDir == "" {
		return &Logger{}, nil // no-op
	}
	dir := filepath.Join(artifactsDir, "llmcalls")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Logger{
		dir:        dir,
		flushEvery: 5,
		idleDur:    5 * time.Second,
		lastFlush:  time.Now(),
		stopCh:     make(chan struct{}),
	}
	go l.periodicFlush()
	return l, nil
}

// WriteLog appends a log entry. Providers should call it twice per LLM call:
// once with req=true (input metadata), once with req=false (output + error).
func (l *Logger) WriteLog(provider, model, logType string, durationMs, inTok, outTok, totalTok int, success bool, errMsg string, meta map[string]any) {
	entry := LogEntry{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Provider:     provider,
		Model:        model,
		Type:         logType,
		DurationMs:   durationMs,
		InputTokens:  inTok,
		OutputTokens: outTok,
		TotalTokens:  totalTok,
		Success:      success,
		Error:        errMsg,
		Metadata:     meta,
	}
	l.push(entry)
}

func (l *Logger) push(entry LogEntry) {
	l.mu.Lock()
	l.buf = append(l.buf, entry)
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

	// O_APPEND write: matches the toolhistory logger's switch away
	// from "read all + atomic rename" which was O(filesize) per
	// flush. For a journal that grows monotonically through a long
	// session, the previous pattern's per-flush cost climbed from kB
	// to tens of MB. Mode 0o600 (was 0o644): prompts and responses
	// carried in these logs are session-sensitive and should not be
	// world-readable on multi-user hosts. A crash mid-write loses one
	// in-flight record at worst — not the whole log.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	_, _ = f.Write(buf.Bytes())
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
	if l == nil || l.stopCh == nil {
		return
	}
	close(l.stopCh)
}
