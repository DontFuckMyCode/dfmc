package tui

// activity.go — the Activity panel is a timestamped firehose of engine
// events. Other panels (Status, Chat footer, stats) show curated state;
// Activity shows *everything* so the user can trust what the agent is
// actually doing.
//
// Shape: a ring buffer of activityEntry, plus a scroll offset and a
// follow-tail toggle. Writes go through recordActivityEvent, which is
// called from handleEngineEvent on every event — the only filtering is
// truncation of giant payload strings.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// maxActivityEntries caps memory use; at ~200 bytes per entry this is
// ~0.5 MiB, comfortably small for an always-on panel.
const maxActivityEntries = 2000

type activityKind string

const (
	activityKindInfo   activityKind = "info"
	activityKindAgent  activityKind = "agent"
	activityKindTool   activityKind = "tool"
	activityKindStream activityKind = "stream"
	activityKindError  activityKind = "error"
	activityKindCtx    activityKind = "context"
	activityKindIndex  activityKind = "index"
)

type activityEntry struct {
	At      time.Time
	Kind    activityKind
	EventID string
	Text    string
}

func (m *Model) recordActivityEvent(ev engine.Event) {
	kind, text := classifyActivity(ev)
	if text == "" {
		text = strings.TrimSpace(ev.Type)
	}
	if text == "" {
		return
	}
	entry := activityEntry{
		At:      time.Now(),
		Kind:    kind,
		EventID: strings.TrimSpace(ev.Type),
		Text:    truncateActivityText(text, 200),
	}
	// Dedupe consecutive identical events — streaming deltas can flood the
	// feed otherwise. We only dedupe when the event id and text both match.
	if n := len(m.activity.entries); n > 0 {
		last := m.activity.entries[n-1]
		if last.EventID == entry.EventID && last.Text == entry.Text {
			return
		}
	}
	m.activity.entries = append(m.activity.entries, entry)
	if len(m.activity.entries) > maxActivityEntries {
		drop := len(m.activity.entries) - maxActivityEntries
		m.activity.entries = m.activity.entries[drop:]
	}
	// Follow-tail: when the user is pinned to the bottom (the default) the
	// view should scroll as new entries arrive. Any manual scroll unsets
	// activityFollow, pinning the view until the user presses G or c.
	if m.activity.follow {
		m.activity.scroll = 0
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
			text = fmt.Sprintf("tool call · %s (step %d)", name, step)
		} else {
			text = "tool call · " + name
		}
	case "tool:result":
		name := payloadString(payload, "tool", "tool")
		dur := payloadInt(payload, "duration_ms", 0)
		text = fmt.Sprintf("tool done · %s (%dms)", name, dur)
	case "tool:error":
		name := payloadString(payload, "tool", "tool")
		err := payloadString(payload, "error", "")
		text = fmt.Sprintf("tool failed · %s %s", name, err)
		kind = activityKindError
	case "agent:loop:start":
		prov := payloadString(payload, "provider", "")
		model := payloadString(payload, "model", "")
		max := payloadInt(payload, "max_tool_steps", 0)
		text = fmt.Sprintf("agent start · %s/%s max=%d", prov, model, max)
	case "agent:loop:thinking":
		step := payloadInt(payload, "step", 0)
		max := payloadInt(payload, "max_tool_steps", 0)
		text = fmt.Sprintf("agent thinking · %d/%d", step, max)
	case "agent:loop:end":
		reason := payloadString(payload, "reason", "done")
		text = "agent end · " + reason
	case "agent:loop:error":
		text = "agent error · " + payloadString(payload, "error", "")
		kind = activityKindError
	case "context:lifecycle:compacted":
		before := payloadInt(payload, "tokens_before", 0)
		after := payloadInt(payload, "tokens_after", 0)
		text = fmt.Sprintf("context compacted · %d → %d tok", before, after)
	case "context:lifecycle:handoff":
		text = "context handoff"
	case "index:start":
		text = "index start"
	case "index:done":
		files := payloadInt(payload, "files", 0)
		text = fmt.Sprintf("index done · %d files", files)
	case "index:error":
		text = "index error · " + payloadString(payload, "error", "")
		kind = activityKindError
	case "engine:initializing", "engine:ready", "engine:serving", "engine:shutdown", "engine:stopped":
		text = strings.TrimPrefix(t, "engine:")
	case "stream:delta":
		// Too noisy to log verbatim; the dedupe pass already squashes runs.
		text = "stream delta"
	case "stream:start":
		text = "stream start"
	case "stream:done":
		text = "stream done"
	default:
		if s, ok := ev.Payload.(string); ok && s != "" {
			text = t + " · " + s
		}
	}
	return kind, text
}

func truncateActivityText(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// kindIcon returns a coloured pictograph for the severity/category slot.
// The icons stay single-cell so column alignment holds under all fonts.
func kindIcon(kind activityKind) string {
	switch kind {
	case activityKindError:
		return warnStyle.Render("✗")
	case activityKindTool:
		return accentStyle.Render("◌")
	case activityKindAgent:
		return accentStyle.Render("◉")
	case activityKindStream:
		return infoStyle.Render("⇢")
	case activityKindCtx:
		return infoStyle.Render("◈")
	case activityKindIndex:
		return subtleStyle.Render("▤")
	default:
		return subtleStyle.Render("·")
	}
}

// formatActivityLine renders a single entry into a fixed-layout row:
//
//	HH:MM:SS  ● kind  text
func formatActivityLine(entry activityEntry, width int) string {
	ts := entry.At.Format("15:04:05")
	icon := kindIcon(entry.Kind)
	head := subtleStyle.Render(ts) + " " + icon + " " + entry.Text
	if width > 0 {
		return truncateSingleLine(head, width)
	}
	return head
}

func (m Model) renderActivityView(width int) string {
	width = clampInt(width, 24, 1000)
	lines := []string{
		sectionHeader("⚡", "Activity"),
		subtleStyle.Render("j/k scroll · g/G top/bottom · c clear · p "+followHint(m.activity.follow)),
		renderDivider(width - 2),
	}

	if len(m.activity.entries) == 0 {
		lines = append(lines, "",
			subtleStyle.Render("No events yet."),
			subtleStyle.Render("Agent calls, tool use, context compaction, and index runs stream in here live."),
		)
		return strings.Join(lines, "\n")
	}

	// Window: show the tail minus activityScroll. Scroll=0 means follow-tail.
	// We render last (viewport) lines, clipped to the available height in
	// the caller via fitPanelContentHeight.
	visible := m.activity.entries
	if m.activity.scroll > 0 && m.activity.scroll < len(m.activity.entries) {
		visible = m.activity.entries[:len(m.activity.entries)-m.activity.scroll]
	}

	for _, entry := range visible {
		lines = append(lines, formatActivityLine(entry, width-2))
	}

	// Footer summary: totals by kind.
	counts := map[activityKind]int{}
	for _, e := range m.activity.entries {
		counts[e.Kind]++
	}
	summary := fmt.Sprintf("%d events · tool=%d agent=%d err=%d ctx=%d",
		len(m.activity.entries),
		counts[activityKindTool],
		counts[activityKindAgent],
		counts[activityKindError],
		counts[activityKindCtx],
	)
	lines = append(lines, "", subtleStyle.Render(summary))
	if !m.activity.follow {
		lines = append(lines, warnStyle.Render("paused · press G to jump to tail and resume follow"))
	}
	return strings.Join(lines, "\n")
}

func followHint(follow bool) string {
	if follow {
		return "pause follow"
	}
	return "resume follow"
}

// handleActivityKey drives navigation for the Activity tab. Returns the
// model unchanged when the key doesn't match a known binding, so the
// outer dispatcher can still fall through.
func (m Model) handleActivityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.activity.entries)
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.activity.scroll >= step {
			m.activity.scroll -= step
		} else {
			m.activity.scroll = 0
		}
		m.activity.follow = m.activity.scroll == 0
	case "k", "up":
		if m.activity.scroll+step < total {
			m.activity.scroll += step
			m.activity.follow = false
		}
	case "pgdown":
		if m.activity.scroll >= pageStep {
			m.activity.scroll -= pageStep
		} else {
			m.activity.scroll = 0
		}
		m.activity.follow = m.activity.scroll == 0
	case "pgup":
		if m.activity.scroll+pageStep <= total {
			m.activity.scroll += pageStep
		} else {
			m.activity.scroll = total
		}
		m.activity.follow = false
	case "g":
		// g = jump to oldest (top of buffer).
		if total > 0 {
			m.activity.scroll = total - 1
		}
		m.activity.follow = false
	case "G":
		m.activity.scroll = 0
		m.activity.follow = true
	case "c":
		m.activity.entries = nil
		m.activity.scroll = 0
		m.activity.follow = true
	case "p":
		m.activity.follow = !m.activity.follow
		if m.activity.follow {
			m.activity.scroll = 0
		}
	}
	return m, nil
}

// small utility to keep the file self-contained.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// lipgloss import kept explicit even when unused directly so future edits
// have the namespace ready for inline colour tweaks.
var _ = lipgloss.NewStyle
