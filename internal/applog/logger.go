package applog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	DataDir string
}

type Logger struct {
	mu        sync.Mutex
	path      string
	component string
	operation string
}

func New(cfg Config) (*Logger, error) {
	base := cfg.DataDir
	if base == "" {
		base = "."
	}
	dir := filepath.Join(base, "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Logger{path: filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl")}, nil
}

func (l *Logger) WithComponent(component string) *Logger {
	if l == nil {
		return nil
	}
	cp := *l
	cp.component = component
	return &cp
}

func (l *Logger) WithOperation(operation string) *Logger {
	if l == nil {
		return nil
	}
	cp := *l
	cp.operation = operation
	return &cp
}

func (l *Logger) Info(message string) {
	l.write("info", message, nil, nil)
}

func (l *Logger) Warn(message string, fields map[string]any) {
	l.write("warn", message, nil, fields)
}

func (l *Logger) Error(message string, err error) {
	l.write("error", message, err, nil)
}

func (l *Logger) Close() error {
	return nil
}

func (l *Logger) write(level, message string, err error, fields map[string]any) {
	if l == nil || l.path == "" {
		return
	}
	record := map[string]any{
		"ts":      time.Now().Format(time.RFC3339Nano),
		"level":   level,
		"message": message,
	}
	if l.component != "" {
		record["component"] = l.component
	}
	if l.operation != "" {
		record["operation"] = l.operation
	}
	if err != nil {
		record["error"] = err.Error()
	}
	for k, v := range fields {
		record[k] = v
	}
	data, jsonErr := json.Marshal(record)
	if jsonErr != nil {
		return
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	f, openErr := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if openErr != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(data)
}
