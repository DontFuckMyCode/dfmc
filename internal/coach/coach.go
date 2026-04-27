// Package coach is a tiny rule-based observer that turns a completed agent
// turn into short user-facing notes. It should stay cheap and deterministic:
// no network I/O, no hidden side effects, and no dependency on provider state.
package coach

import (
	"fmt"
	"strings"
)

type Severity string

const (
	SeverityInfo      Severity = "info"
	SeverityWarn      Severity = "warn"
	SeverityCelebrate Severity = "celebrate"
)

type Note struct {
	Text     string   `json:"text"`
	Severity Severity `json:"severity"`
	Origin   string   `json:"origin,omitempty"`
	Action   string   `json:"action,omitempty"`
}

type Snapshot struct {
	Question        string
	Answer          string
	ToolSteps       int
	ToolsUsed       []string
	FailedTools     []string
	Mutations       []string
	Parked          bool
	ParkReason      string
	Provider        string
	Model           string
	TokensUsed      int
	ElapsedMs       int64
	TrajectoryHints []string

	ContextFiles          int
	ContextSources        map[string]int
	QueryIdentifiers      int
	QueryIdentifierNames  []string
	UsefulQueryIdentifier string
	QuestionHasFileMarker bool

	ValidationHint string
	TightenHint    string
	RetrievalHint  string
}

type Observer interface {
	Observe(Snapshot) []Note
}

type RuleObserver struct {
	MaxNotes int
}

func NewRuleObserver() *RuleObserver { return &RuleObserver{MaxNotes: 3} }

func (r *RuleObserver) Observe(s Snapshot) []Note {
	max := r.MaxNotes
	if max <= 0 {
		max = 3
	}
	out := make([]Note, 0, max)
	push := func(n Note) bool {
		n.Text = strings.TrimSpace(n.Text)
		if n.Text == "" {
			return false
		}
		out = append(out, n)
		return len(out) >= max
	}

	if s.Parked {
		switch s.ParkReason {
		case "budget_exhausted":
			if push(Note{
				Text:     "Loop parked - token budget exhausted. The request was likely too broad for a single turn. Try /split to break it into focused subtasks, or /continue with a narrower follow-up.",
				Severity: SeverityWarn,
				Origin:   "parked_budget",
			}) {
				return out
			}
		default:
			if push(Note{
				Text:     "Loop parked - hit its step cap. Type /continue to resume, optionally with a note to focus the next pass.",
				Severity: SeverityWarn,
				Origin:   "parked_loop",
			}) {
				return out
			}
		}
	}

	if len(s.Mutations) > 0 && !answerMentionsValidation(s.Answer) {
		paths := strings.Join(trimPaths(s.Mutations, 3), ", ")
		text := fmt.Sprintf("Files mutated (%s) but the answer didn't mention a test/build/vet run.", paths)
		if hint := strings.TrimSpace(s.ValidationHint); hint != "" {
			text += " Next step: " + hint + "."
		} else {
			text += " Double-check before shipping."
		}
		if push(Note{
			Text:     text,
			Severity: SeverityWarn,
			Origin:   "mutation_unvalidated",
			Action:   strings.TrimSpace(s.ValidationHint),
		}) {
			return out
		}
	}

	if len(s.FailedTools) >= 2 {
		if push(Note{
			Text:     fmt.Sprintf("%d tool call(s) failed this turn. Worth reading the error messages directly before asking for another pass.", len(s.FailedTools)),
			Severity: SeverityWarn,
			Origin:   "repeated_failures",
		}) {
			return out
		}
	}

	if s.TokensUsed > 20000 {
		text := fmt.Sprintf("Heavy turn (~%dk tokens).", s.TokensUsed/1000)
		if hint := strings.TrimSpace(s.TightenHint); hint != "" {
			text += " Tighter next pass: " + hint + "."
		} else {
			text += " If you want a tighter next pass, try adding --provider offline or narrowing the question with a [[file:path]] marker."
		}
		if push(Note{
			Text:     text,
			Severity: SeverityInfo,
			Origin:   "heavy_turn",
			Action:   strings.TrimSpace(s.TightenHint),
		}) {
			return out
		}
	}

	if s.ToolSteps == 0 && containsPseudoToolCall(s.Answer) {
		if push(Note{
			Text:     "Model emitted a text-format tool call instead of using native tool-calling. Provider accepted the tools but this model didn't use them - try a different model on the same provider, or check the endpoint.",
			Severity: SeverityWarn,
			Origin:   "pseudo_tool_call",
		}) {
			return out
		}
	}

	if s.ToolSteps == 0 && strings.Contains(s.Answer, "?") && looksActionable(s.Question) {
		if push(Note{
			Text:     "The model answered without using any tools. If you expected a code change, ask again with a more explicit action verb (edit, run, add).",
			Severity: SeverityInfo,
			Origin:   "no_action_taken",
		}) {
			return out
		}
	}

	if strings.TrimSpace(s.UsefulQueryIdentifier) != "" &&
		s.QueryIdentifiers > 0 && s.ContextFiles > 0 && s.ContextSources != nil &&
		s.ContextSources["symbol-match"] == 0 && s.ContextSources["marker"] == 0 {
		text := "Retrieval didn't resolve any query identifier to a codemap symbol."
		if s.QuestionHasFileMarker {
			text = "Retrieval didn't resolve the requested symbol even with the current [[file:...]] marker."
		}
		if hint := strings.TrimSpace(s.RetrievalHint); hint != "" {
			text += " Next step: " + hint + "."
		} else {
			text += " If a specific function/type is in scope, reference it with [[file:path]] or rename-exact so the graph can seed from it."
		}
		if push(Note{
			Text:     text,
			Severity: SeverityInfo,
			Origin:   "retrieval_symbol_miss",
			Action:   strings.TrimSpace(s.RetrievalHint),
		}) {
			return out
		}
	}

	if s.ContextFiles > 0 && s.ContextSources != nil && s.ContextSources["hotspot"] == s.ContextFiles {
		if push(Note{
			Text:     "Context came entirely from graph hotspots - the query didn't match any specific file. Add a [[file:path]] marker or a distinctive symbol name to focus the next pass.",
			Severity: SeverityInfo,
			Origin:   "retrieval_hotspot_only",
		}) {
			return out
		}
	}

	if len(s.ToolsUsed) > 0 && len(s.FailedTools) == 0 && !s.Parked && s.TokensUsed > 0 && s.TokensUsed < 8000 {
		if push(Note{
			Text:     "Clean pass - tools used, no failures, tight token spend.",
			Severity: SeverityCelebrate,
			Origin:   "clean_pass",
		}) {
			return out
		}
	}

	return out
}

func answerMentionsValidation(answer string) bool {
	l := strings.ToLower(answer)
	keys := []string{
		"go test", "go vet", "go build", "npm test", "pnpm test", "yarn test",
		"pytest", "cargo test", "cargo check", "tsc", "eslint", "biome",
		"run the test", "ran the test", "validated", "verified", "confirmed", "smoke test",
	}
	for _, k := range keys {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

func containsPseudoToolCall(answer string) bool {
	l := strings.ToLower(answer)
	for _, marker := range []string{
		"[tool_call]", "[/tool_call]",
		"<tool_call>", "</tool_call>",
		"[tool_batch_call]", "[/tool_batch_call]",
	} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

func looksActionable(question string) bool {
	l := strings.ToLower(question)
	keys := []string{
		"fix", "add", "implement", "edit", "refactor", "migrate", "remove",
		"delete", "rename", "update", "create", "build", "write", "generate",
		"wire up", "hook up", "wire", "append", "insert", "patch", "apply",
	}
	for _, k := range keys {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

func trimPaths(paths []string, max int) []string {
	if len(paths) <= max {
		return paths
	}
	out := append([]string{}, paths[:max]...)
	out = append(out, fmt.Sprintf("+%d more", len(paths)-max))
	return out
}
