package engine

// agent_coach_emit.go — glue between the native-tool-loop completion and
// the user-facing coach in internal/coach. The coach analyzes a snapshot
// of the just-finished turn and publishes short commentary on the event
// bus so UIs (TUI chat panel, web SSE clients) can render it inline.
//
// Kept fire-and-forget: the coach call is cheap (rule-based today) and
// must never block the agent loop's return path. If a future Observer
// implementation calls an LLM, it should dispatch asynchronously inside
// its own Observe.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/coach"
	dfmccontext "github.com/dontfuckmycode/dfmc/internal/context"
)

// coachObserver returns the active Observer for this engine, honoring the
// Coach config (Enabled=false → nil, disabling the whole surface). A future
// revision may swap in an LLM-backed Observer behind the same interface.
func (e *Engine) coachObserver() coach.Observer {
	if e == nil {
		return nil
	}
	if e.Config != nil && !e.Config.Coach.Enabled {
		return nil
	}
	max := 0
	if e.Config != nil {
		max = e.Config.Coach.MaxNotes
	}
	if max <= 0 {
		max = 3
	}
	return &coach.RuleObserver{MaxNotes: max}
}

// emitCoachNotes derives coach notes from a completed native-tool turn and
// publishes them via the event bus. Safe to call when EventBus is nil
// (notes are simply dropped).
func (e *Engine) emitCoachNotes(question string, completion nativeToolCompletion) {
	if e == nil || e.EventBus == nil {
		return
	}
	obs := e.coachObserver()
	if obs == nil {
		return
	}
	snap := buildCoachSnapshot(question, completion)
	notes := obs.Observe(snap)
	if len(notes) == 0 {
		return
	}
	for _, n := range notes {
		e.EventBus.Publish(Event{
			Type:   "coach:note",
			Source: "engine",
			Payload: map[string]any{
				"text":     n.Text,
				"severity": string(n.Severity),
				"origin":   n.Origin,
				"provider": completion.Provider,
				"model":    completion.Model,
			},
		})
	}
}

func buildCoachSnapshot(question string, completion nativeToolCompletion) coach.Snapshot {
	tools := make([]string, 0, len(completion.ToolTraces))
	var failed, mutations []string
	for _, t := range completion.ToolTraces {
		name := t.Call.Name
		if t.Call.Name == "tool_call" || t.Call.Name == "tool_batch_call" {
			if inner := extractBridgedInnerName(t.Call.Input); inner != "" {
				name = inner
			}
		}
		tools = append(tools, name)
		if t.Err != "" {
			failed = append(failed, name)
			continue
		}
		switch name {
		case "edit_file", "write_file", "apply_patch":
			args := t.Call.Input
			if t.Call.Name == "tool_call" {
				if inner := extractBridgedInnerArgs(t.Call.Input); inner != nil {
					args = inner
				}
			}
			path := strings.TrimSpace(stringArg(args, "path"))
			if path == "" {
				path = strings.TrimSpace(stringArg(args, "file"))
			}
			if path != "" {
				mutations = append(mutations, path)
			}
		}
	}
	sources := map[string]int{}
	for _, c := range completion.Context {
		if c.Source == "" {
			continue
		}
		sources[c.Source]++
	}
	return coach.Snapshot{
		Question:         question,
		Answer:           completion.Answer,
		ToolSteps:        len(completion.ToolTraces),
		ToolsUsed:        tools,
		FailedTools:      failed,
		Mutations:        mutations,
		Parked:           completion.Parked,
		ParkReason:       string(completion.ParkedReason),
		Provider:         completion.Provider,
		Model:            completion.Model,
		TokensUsed:       completion.TokenCount,
		ContextFiles:     len(completion.Context),
		ContextSources:   sources,
		QueryIdentifiers: len(dfmccontext.ExtractQueryIdentifiers(question)),
	}
}

func stringArg(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
