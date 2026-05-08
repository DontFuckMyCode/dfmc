package engine

// agent_coach_emit.go glues the completed native-tool turn to the rule-based
// user-facing coach. The goal is to turn vague post-hoc nags into concrete
// next steps such as an actual validation command or a narrower follow-up.

import (
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/coach"
	dfmccontext "github.com/dontfuckmycode/dfmc/internal/context"
)

const coachHighTokenThreshold = 40000

var coachProjectTypeCache sync.Map

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

func (e *Engine) emitCoachNotes(question string, completion nativeToolCompletion) {
	if e == nil || e.EventBus == nil {
		return
	}
	obs := e.coachObserver()
	if obs == nil {
		return
	}
	snap := e.buildCoachSnapshot(question, completion)
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
				"action":   n.Action,
				"provider": completion.Provider,
				"model":    completion.Model,
			},
		})
	}
}

func (e *Engine) buildCoachSnapshot(question string, completion nativeToolCompletion) coach.Snapshot {
	toolsUsed := make([]string, 0, len(completion.ToolTraces))
	failed := make([]string, 0, len(completion.ToolTraces))
	mutations := make([]string, 0, len(completion.ToolTraces))
	queryIdentifiers := dfmccontext.ExtractQueryIdentifiers(question)
	usefulIdentifier := firstCoachIdentifier(queryIdentifiers)

	for _, t := range completion.ToolTraces {
		name := t.Call.Name
		args := t.Call.Input
		if t.Call.Name == "tool_call" || t.Call.Name == "tool_batch_call" {
			if inner := extractBridgedInnerName(t.Call.Input); inner != "" {
				name = inner
			}
			if inner := extractBridgedInnerArgs(t.Call.Input); inner != nil {
				args = inner
			}
		}
		toolsUsed = append(toolsUsed, name)
		if t.Err != "" {
			failed = append(failed, name)
			continue
		}
		switch name {
		case "edit_file", "write_file", "apply_patch":
			path := normalizeCoachPath(stringArg(args, "path"))
			if path == "" {
				path = normalizeCoachPath(stringArg(args, "file"))
			}
			if path != "" {
				mutations = append(mutations, path)
			}
		}
	}

	sources := map[string]int{}
	for _, c := range completion.Context {
		if c.Source != "" {
			sources[c.Source]++
		}
	}

	return coach.Snapshot{
		Question:              question,
		Answer:                completion.Answer,
		ToolSteps:             len(completion.ToolTraces),
		ToolsUsed:             toolsUsed,
		FailedTools:           failed,
		Mutations:             mutations,
		Parked:                completion.Parked,
		ParkReason:            string(completion.ParkedReason),
		Provider:              completion.Provider,
		Model:                 completion.Model,
		TokensUsed:            completion.TokenCount,
		ContextFiles:          len(completion.Context),
		ContextSources:        sources,
		QueryIdentifiers:      len(queryIdentifiers),
		QueryIdentifierNames:  append([]string(nil), queryIdentifiers...),
		UsefulQueryIdentifier: usefulIdentifier,
		QuestionHasFileMarker: coachQuestionHasFileMarker(question),
		ValidationHint:        recommendCoachValidationHint(e.projectRootForCoach(), completion.Context, mutations),
		TightenHint:           recommendCoachTightenHint(question, completion.Context, mutations, completion.TokenCount, queryIdentifiers),
		RetrievalHint:         recommendCoachRetrievalHint(question, completion.Context, usefulIdentifier),
	}
}

func (e *Engine) projectRootForCoach() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.ProjectRoot)
}

// Hint recommenders, identifier/path classification, project-type
// detection, and small shell/string helpers live in
// agent_coach_emit_hints.go.
