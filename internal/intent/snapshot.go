package intent

import (
	"fmt"
	"strings"
	"time"
)

// Snapshot is the compact view of engine state the intent classifier
// reasons over. Built once per user submit by the engine wiring (so
// this package stays decoupled from Engine internals — easier to test,
// easier to swap the data source). Only the fields that actually help
// the classifier disambiguate live here; everything else is deliberately
// omitted to keep the prompt small and the latency budget tight.
type Snapshot struct {
	// Parked: when true the previous agent loop was capped or budget-gated
	// and is waiting for the user to resume. The presence of parked state
	// is the strongest signal toward IntentResume — a bare "devam et" /
	// "continue" / "go on" without parked state is much weaker.
	Parked           bool
	ParkedSummary    string // human-readable, e.g. "parked at step 7 — refactor tui.go"
	ParkedStep       int
	ParkedToolName   string    // last tool the parked loop ran
	ParkedAt         time.Time // wall-clock; "stale park" hints at IntentNew
	CumulativeSteps  int       // across all resume cycles, rough effort gauge
	CumulativeTokens int

	// Provider/model the engine is currently configured to use for the
	// main turn. Surfaced to the classifier so it can mention "you're on
	// Opus" in EnrichedRequest when the user says "use the smart one".
	Provider string
	Model    string

	// LastAssistant is the most recent assistant text in the active
	// conversation, truncated to a few hundred characters. Lets the
	// classifier resolve "fix it" / "do that for the others" by
	// referencing the prior turn's actual content.
	LastAssistant string

	// RecentToolNames lists up to ~5 recent tool calls from the active
	// conversation, newest first. "read_file, grep, edit_file" is enough
	// signal for the classifier to know whether the user is mid-task.
	RecentToolNames []string

	// UserTurnCount is the total number of user turns in the active
	// conversation. Distinguishes "first message of session" (more
	// likely IntentNew) from "deep into a thread" (resume more likely).
	UserTurnCount int
}

// Render returns the snapshot serialized into a compact, model-friendly
// block that fits inside a system or user message. Truncated to maxChars
// runes (caller passes the config value); when 0 the default 2000 is
// used. Never returns an empty string — at minimum returns "(no engine
// state)" so the prompt template stays well-formed.
func (s Snapshot) Render(maxChars int) string {
	if maxChars <= 0 {
		maxChars = 2000
	}
	var b strings.Builder
	if s.Parked {
		b.WriteString("PARKED_AGENT: yes\n")
		if s.ParkedSummary != "" {
			b.WriteString("  summary: ")
			b.WriteString(s.ParkedSummary)
			b.WriteByte('\n')
		}
		if s.ParkedStep > 0 {
			fmt.Fprintf(&b, "  step: %d (cumulative: %d)\n", s.ParkedStep, s.CumulativeSteps)
		}
		if s.ParkedToolName != "" {
			b.WriteString("  last_tool: ")
			b.WriteString(s.ParkedToolName)
			b.WriteByte('\n')
		}
		if !s.ParkedAt.IsZero() {
			age := time.Since(s.ParkedAt).Round(time.Second)
			fmt.Fprintf(&b, "  parked_age: %s\n", age)
		}
	} else {
		b.WriteString("PARKED_AGENT: no\n")
	}
	if s.Provider != "" || s.Model != "" {
		fmt.Fprintf(&b, "ACTIVE_MODEL: %s/%s\n", s.Provider, s.Model)
	}
	fmt.Fprintf(&b, "USER_TURNS: %d\n", s.UserTurnCount)
	if len(s.RecentToolNames) > 0 {
		b.WriteString("RECENT_TOOLS: ")
		b.WriteString(strings.Join(s.RecentToolNames, ", "))
		b.WriteByte('\n')
	}
	if s.LastAssistant != "" {
		b.WriteString("LAST_ASSISTANT:\n")
		b.WriteString(indent(s.LastAssistant, "  "))
		b.WriteByte('\n')
	}
	out := b.String()
	if len([]byte(out)) > maxChars {
		r := []rune(out)
		out = string(r[:maxChars]) + "\n  ...(truncated)"
	}
	if strings.TrimSpace(out) == "" {
		return "(no engine state)"
	}
	return out
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
