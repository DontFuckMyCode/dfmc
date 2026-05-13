package tui

// tool_call_log.go — rolling buffer of every tool:call / tool:result /
// tool:error / tool:denied event with full detail for the Ctrl+Alt+T Tool
// Status panel. The chat transcript gets temporary live rows only; the
// full params/results/errors/reasoning live here so the panel can show
// a scrollable detailed history after the run finishes.

import (
	"strings"
	"time"
)

const toolCallLogCap = 200

// toolCallLogEntry captures one tool lifecycle event (call + eventual
// result) so the Ctrl+Alt+T panel can render a detailed timeline.
type toolCallLogEntry struct {
	ToolName    string
	Status      string // "running", "ok", "failed", "denied", "timeout"
	StartedAt   time.Time
	FinishedAt  time.Time
	DurationMs  int
	Step        int
	Reason      string
	Params      string
	Result      string
	Error       string
	OutputChars int
	Tokens      int
	IsBatch     bool
	BatchOK     int
	BatchFail   int
	BatchTotal  int
}

// toolCallLogState holds the rolling buffer. Newest entries are at the
// end so the panel renders newest-first by walking backwards.
type toolCallLogState struct {
	entries []toolCallLogEntry
}

// appendEntry adds a new entry, trimming the oldest when the cap is hit.
func (s *toolCallLogState) appendEntry(e toolCallLogEntry) {
	if s.entries == nil {
		s.entries = make([]toolCallLogEntry, 0, toolCallLogCap)
	}
	s.entries = append(s.entries, e)
	if len(s.entries) > toolCallLogCap {
		s.entries = s.entries[len(s.entries)-toolCallLogCap:]
	}
}

// updateLastRunning finds the most recent "running" entry for the given
// tool+step and updates it with terminal data. Returns true when found.
func (s *toolCallLogState) updateLastRunning(toolName string, step int, update func(*toolCallLogEntry)) bool {
	if s.entries == nil {
		return false
	}
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := &s.entries[i]
		if e.Status != "running" {
			continue
		}
		if !strings.EqualFold(e.ToolName, toolName) {
			continue
		}
		if step != 0 && e.Step != 0 && e.Step != step {
			continue
		}
		update(e)
		return true
	}
	return false
}

func (m *Model) pushToolCallLogEntry(e toolCallLogEntry) {
	m.toolCallLog.appendEntry(e)
}

func (m *Model) finalizeToolCallLogEntry(toolName string, step int, update func(*toolCallLogEntry)) {
	if !m.toolCallLog.updateLastRunning(toolName, step, update) {
		// No running entry found — create a terminal entry directly.
		e := toolCallLogEntry{
			ToolName:   toolName,
			Step:       step,
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
		}
		update(&e)
		m.toolCallLog.appendEntry(e)
	}
}
