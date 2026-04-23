package tui

// activity.go - the Activity panel is the TUI's mission-control surface:
// a searchable, filterable event timeline with a detail inspector. Other
// panels summarize state; this one answers "what just happened?" without
// making the user leave the terminal.

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// maxActivityEntries caps memory use; at ~300 bytes per entry this still lands
// comfortably under a megabyte while keeping plenty of live history.
const maxActivityEntries = 2000

const activityDefaultRenderHeight = 24

type activityKind string
type activityViewMode string
type activityActionTarget string

const (
	activityKindInfo   activityKind = "info"
	activityKindAgent  activityKind = "agent"
	activityKindTool   activityKind = "tool"
	activityKindStream activityKind = "stream"
	activityKindError  activityKind = "error"
	activityKindCtx    activityKind = "context"
	activityKindIndex  activityKind = "index"
)

const (
	activityViewAll      activityViewMode = "all"
	activityViewTools    activityViewMode = "tools"
	activityViewAgents   activityViewMode = "agents"
	activityViewErrors   activityViewMode = "errors"
	activityViewWorkflow activityViewMode = "workflow"
	activityViewContext  activityViewMode = "context"
)

var activityViewModes = []activityViewMode{
	activityViewAll,
	activityViewTools,
	activityViewAgents,
	activityViewErrors,
	activityViewWorkflow,
	activityViewContext,
}

const (
	activityTargetStatus    activityActionTarget = "status"
	activityTargetFiles     activityActionTarget = "files"
	activityTargetPatch     activityActionTarget = "patch"
	activityTargetTools     activityActionTarget = "tools"
	activityTargetPlans     activityActionTarget = "plans"
	activityTargetContext   activityActionTarget = "context"
	activityTargetCodeMap   activityActionTarget = "codemap"
	activityTargetSecurity  activityActionTarget = "security"
	activityTargetProviders activityActionTarget = "providers"
)

type activityEntry struct {
	At       time.Time
	Kind     activityKind
	EventID  string
	Source   string
	Tool     string
	Path     string
	Provider string
	Query    string
	Text     string
	Details  []string
	Count    int
}

func (m *Model) recordActivityEvent(ev engine.Event) {
	prevVisible := 0
	if !m.activity.follow {
		prevVisible = len(m.filteredActivityEntries())
	}
	kind, text := classifyActivity(ev)
	if text == "" {
		text = strings.TrimSpace(ev.Type)
	}
	if text == "" {
		return
	}
	payload, _ := toStringAnyMap(ev.Payload)
	at := ev.Timestamp
	if at.IsZero() {
		at = time.Now()
	}
	entry := activityEntry{
		At:       at,
		Kind:     kind,
		EventID:  strings.TrimSpace(ev.Type),
		Source:   strings.TrimSpace(ev.Source),
		Tool:     payloadString(payload, "tool", ""),
		Path:     extractActivityPath(ev),
		Provider: extractActivityProvider(ev),
		Query:    extractActivityQuery(ev, text),
		Text:     truncateActivityText(text, 200),
		Details:  buildActivityDetailLines(ev, text),
		Count:    1,
	}

	if n := len(m.activity.entries); n > 0 {
		last := &m.activity.entries[n-1]
		if last.EventID == entry.EventID && last.Text == entry.Text {
			last.Count++
			last.At = entry.At
			if last.Source == "" {
				last.Source = entry.Source
			}
			if len(last.Details) == 0 {
				last.Details = entry.Details
			}
			return
		}
	}

	m.activity.entries = append(m.activity.entries, entry)
	if len(m.activity.entries) > maxActivityEntries {
		drop := len(m.activity.entries) - maxActivityEntries
		m.activity.entries = m.activity.entries[drop:]
	}
	if m.activity.follow {
		m.activity.scroll = 0
	} else {
		// Hold the user's selected event in place only when the active
		// filter/query actually gained visible rows.
		if nextVisible := len(m.filteredActivityEntries()); nextVisible > prevVisible {
			m.activity.scroll += nextVisible - prevVisible
		}
		m.activity.scroll = clampActivityOffset(m.activity.scroll, len(m.filteredActivityEntries()))
	}
}

// classifyActivity maps an engine event onto a short display line +
// coloring category. Unknown events fall through as info/typename.
func classifyActivity(ev engine.Event) (activityKind, string) {
	kind := activityKindInfo
	t := strings.ToLower(strings.TrimSpace(ev.Type))
	payload, _ := toStringAnyMap(ev.Payload)

	switch {
	case strings.HasPrefix(t, "agent:"):
		kind = activityKindAgent
	case strings.HasPrefix(t, "tool:"):
		kind = activityKindTool
	case strings.HasPrefix(t, "stream:"):
		kind = activityKindStream
	case strings.HasPrefix(t, "context:"), strings.HasPrefix(t, "ctx:"):
		kind = activityKindCtx
	case strings.HasPrefix(t, "index:"):
		kind = activityKindIndex
	case strings.Contains(t, "error"), strings.Contains(t, "fail"):
		kind = activityKindError
	}

	text := t
	switch t {
	case "tool:call":
		name := payloadString(payload, "tool", "tool")
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			text = fmt.Sprintf("tool call - %s (step %d)", name, step)
		} else {
			text = "tool call - " + name
		}
	case "tool:result":
		name := payloadString(payload, "tool", "tool")
		dur := payloadIntAny(payload, 0, "duration_ms", "durationMs")
		text = fmt.Sprintf("tool done - %s (%dms)", name, dur)
	case "tool:error":
		name := payloadString(payload, "tool", "tool")
		err := payloadString(payload, "error", "")
		text = fmt.Sprintf("tool failed - %s %s", name, err)
		kind = activityKindError
	case "agent:loop:start":
		prov := payloadString(payload, "provider", "")
		model := payloadString(payload, "model", "")
		protocol := payloadString(payload, "protocol", "")
		baseURL := payloadString(payload, "base_url", "")
		host := ""
		if parsed, err := url.Parse(baseURL); err == nil {
			host = strings.TrimSpace(parsed.Host)
		}
		max := payloadInt(payload, "max_tool_steps", 0)
		text = fmt.Sprintf("agent start - %s/%s", prov, model)
		if protocol != "" {
			text += " " + protocol
		}
		if host != "" {
			text += " " + host
		}
		text += fmt.Sprintf(" max=%d", max)
	case "agent:loop:thinking":
		step := payloadInt(payload, "step", 0)
		max := payloadInt(payload, "max_tool_steps", 0)
		text = fmt.Sprintf("agent thinking - %d/%d", step, max)
	case "agent:autonomy:plan":
		count := payloadInt(payload, "subtask_count", 0)
		confidence := 0.0
		if raw, ok := payload["confidence"].(float64); ok {
			confidence = raw
		}
		mode := "sequential"
		if payloadBool(payload, "parallel", false) {
			mode = "parallel"
		}
		scope := payloadString(payload, "scope", "")
		text = fmt.Sprintf("autonomy preflight - %d subtasks %s %.2f", count, mode, confidence)
		if scope != "" && scope != "top_level" {
			text = fmt.Sprintf("autonomy preflight [%s] - %d subtasks %s %.2f", scope, count, mode, confidence)
		}
	case "agent:autonomy:kickoff":
		toolName := payloadString(payload, "tool", "orchestrate")
		count := payloadInt(payload, "subtask_count", 0)
		confidence := 0.0
		if raw, ok := payload["confidence"].(float64); ok {
			confidence = raw
		}
		text = fmt.Sprintf("autonomy kickoff - %s %d subtasks %.2f", toolName, count, confidence)
	case "agent:loop:end":
		reason := payloadString(payload, "reason", "done")
		text = "agent end - " + reason
	case "agent:loop:error":
		text = "agent error - " + payloadString(payload, "error", "")
		kind = activityKindError
	case "provider:throttle:retry":
		prov := payloadString(payload, "provider", "?")
		attempt := payloadInt(payload, "attempt", 0)
		waitMs := payloadInt(payload, "wait_ms", 0)
		mode := "request"
		if payloadBool(payload, "stream", false) {
			mode = "stream"
		}
		text = fmt.Sprintf("provider throttled - %s %s retry #%d in %dms", prov, mode, attempt, waitMs)
		kind = activityKindError
	case "config:reload:auto":
		path := payloadString(payload, "path", "")
		text = "config auto-reloaded"
		if path != "" {
			text += " - " + truncateSingleLine(path, 96)
		}
	case "config:reload:auto_failed":
		errText := payloadString(payload, "error", "")
		text = "config auto-reload failed"
		if errText != "" {
			text += " - " + truncateSingleLine(errText, 120)
		}
		kind = activityKindError
	case "context:lifecycle:compacted":
		before := payloadIntAny(payload, 0, "before_tokens", "tokens_before")
		after := payloadIntAny(payload, 0, "after_tokens", "tokens_after")
		text = fmt.Sprintf("context compacted - %d -> %d tok", before, after)
	case "context:lifecycle:handoff":
		text = "context handoff"
	case "index:start":
		text = "index start"
	case "index:done":
		files := payloadInt(payload, "files", 0)
		text = fmt.Sprintf("index done - %d files", files)
	case "index:error":
		text = "index error - " + payloadString(payload, "error", "")
		kind = activityKindError
	case "engine:initializing", "engine:ready", "engine:serving", "engine:shutdown", "engine:stopped":
		text = strings.TrimPrefix(t, "engine:")
	case "stream:delta":
		text = "stream delta"
	case "stream:start":
		text = "stream start"
	case "stream:done":
		text = "stream done"
	default:
		if s, ok := ev.Payload.(string); ok && s != "" {
			text = t + " - " + s
		}
	}
	return kind, text
}


