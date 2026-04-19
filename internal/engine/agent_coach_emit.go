package engine

// agent_coach_emit.go glues the completed native-tool turn to the rule-based
// user-facing coach. The goal is to turn vague post-hoc nags into concrete
// next steps such as an actual validation command or a narrower follow-up.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/coach"
	dfmccontext "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/pkg/types"
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

func recommendCoachValidationHint(projectRoot string, contextChunks []types.ContextChunk, mutations []string) string {
	root := strings.TrimSpace(projectRoot)
	firstMutation := firstNonEmptyPath(mutations)
	projectType := detectCoachProjectType(root)

	if projectType == "go" {
		if firstMutation != "" {
			dir := normalizeCoachPath(filepath.Dir(firstMutation))
			if dir != "." && dir != "" {
				return fmt.Sprintf("run `go test %s -count=1`", shellQuoteIfNeeded("./"+strings.TrimPrefix(dir, "./")+"/..."))
			}
		}
		return "run `go test ./... -count=1`"
	}
	if projectType == "rust" {
		return "run `cargo test`"
	}
	if projectType == "node" {
		return "run `npm test`"
	}
	if projectType == "python" {
		if firstMutation != "" && strings.HasSuffix(strings.ToLower(firstMutation), ".py") {
			return fmt.Sprintf("run `pytest %s -q`", shellQuoteIfNeeded(firstMutation))
		}
		return "run `pytest -q`"
	}

	if path := firstContextPath(contextChunks); path != "" {
		return fmt.Sprintf("re-open `[[file:%s]]` and run the smallest relevant verification command", path)
	}
	return ""
}

func recommendCoachTightenHint(question string, contextChunks []types.ContextChunk, mutations []string, tokensUsed int, identifiers []string) string {
	if path := firstNonEmptyPath(mutations); path != "" {
		return narrowCoachReviewHint(path, identifiers)
	}
	if path := bestCoachContextPath(contextChunks); path != "" {
		return narrowCoachReviewHint(path, identifiers)
	}
	if tokensUsed > coachHighTokenThreshold && strings.TrimSpace(question) != "" {
		return "use `/split` to break the task into smaller passes"
	}
	return ""
}

func recommendCoachRetrievalHint(question string, contextChunks []types.ContextChunk, symbol string) string {
	if coachQuestionHasFileMarker(question) {
		if symbol != "" {
			return fmt.Sprintf("retry with the existing [[file:...]] marker plus exact symbol name `%s`", symbol)
		}
		return ""
	}
	if path := bestCoachContextPath(contextChunks); path != "" {
		if symbol != "" {
			return fmt.Sprintf("retry with `review [[file:%s]] %s`", path, symbol)
		}
		return fmt.Sprintf("retry with `review [[file:%s]]` plus the exact symbol name", path)
	}
	return ""
}

func coachQuestionHasFileMarker(question string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(question)), "[[file:")
}

func narrowCoachReviewHint(path string, identifiers []string) string {
	if path == "" {
		return ""
	}
	if symbol := firstCoachIdentifier(identifiers); symbol != "" {
		return fmt.Sprintf("ask `review [[file:%s]] %s`", path, symbol)
	}
	return fmt.Sprintf("ask `review [[file:%s]]`", path)
}

func firstCoachIdentifier(identifiers []string) string {
	skip := map[string]struct{}{
		"review": {}, "explain": {}, "inspect": {}, "check": {}, "look": {},
		"analyze": {}, "analyse": {}, "fix": {}, "update": {}, "add": {},
		"remove": {}, "write": {}, "read": {}, "open": {}, "show": {},
		"file": {}, "path": {}, "line": {}, "lines": {}, "symbol": {},
	}
	for _, ident := range identifiers {
		if ident = strings.TrimSpace(ident); ident == "" {
			continue
		}
		if looksLikeCoachPathToken(ident) {
			continue
		}
		if _, blocked := skip[strings.ToLower(ident)]; blocked {
			continue
		}
		if ident != "" {
			return ident
		}
	}
	return ""
}

func looksLikeCoachPathToken(ident string) bool {
	if strings.ContainsAny(ident, `/\`) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(ident))
	if strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".json") {
		return true
	}
	return false
}

func bestCoachContextPath(chunks []types.ContextChunk) string {
	priorities := []string{"symbol-match", "marker", "query-match", "graph-neighborhood", "hotspot"}
	for _, source := range priorities {
		for _, ch := range chunks {
			if !strings.EqualFold(strings.TrimSpace(ch.Source), source) {
				continue
			}
			if path := normalizeCoachPath(ch.Path); path != "" {
				return path
			}
		}
	}
	return firstContextPath(chunks)
}

func firstNonEmptyPath(paths []string) string {
	for _, path := range paths {
		if path = normalizeCoachPath(path); path != "" {
			return path
		}
	}
	return ""
}

func firstContextPath(chunks []types.ContextChunk) string {
	for _, ch := range chunks {
		if path := normalizeCoachPath(ch.Path); path != "" {
			return path
		}
	}
	return ""
}

func normalizeCoachPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func detectCoachProjectType(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if cached, ok := coachProjectTypeCache.Load(root); ok {
		if projectType, ok := cached.(string); ok {
			return projectType
		}
	}
	projectType := ""
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		projectType = "go"
	case fileExists(filepath.Join(root, "Cargo.toml")):
		projectType = "rust"
	case fileExists(filepath.Join(root, "package.json")):
		projectType = "node"
	case fileExists(filepath.Join(root, "pyproject.toml")) ||
		fileExists(filepath.Join(root, "requirements.txt")) ||
		fileExists(filepath.Join(root, "setup.py")):
		projectType = "python"
	}
	coachProjectTypeCache.Store(root, projectType)
	return projectType
}

func shellQuote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func shellQuoteIfNeeded(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "''"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._/-:", r):
		default:
			return shellQuote(s)
		}
	}
	return s
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
