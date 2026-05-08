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

	// Grep-thrash: many grep_codebase calls without mutating or
	// resolving any symbol. The model is searching for needles
	// without using the cheaper layered tools the read-stack
	// recommends. Common when the user asked "where is X" and the
	// model didn't try find_symbol / codemap first.
	//
	// Threshold: 5+ grep calls. Tighter would catch every
	// medium-complexity research turn; looser misses the actual
	// thrashing case (10+ greps for one finding).
	if greps := countTool(s.ToolsUsed, "grep_codebase"); greps >= 5 && len(s.Mutations) == 0 && !s.Parked {
		if push(Note{
			Text:     fmt.Sprintf("%d grep calls this turn with no edit. If you know the symbol name, find_symbol returns the body in one call. For a project outline, codemap is the cheaper survey.", greps),
			Severity: SeverityInfo,
			Origin:   "grep_thrash",
		}) {
			return out
		}
	}

	// Tool flood: 30+ tool calls in a single turn is almost always a
	// planning failure — either the request was too broad or the
	// model is iterating instead of thinking. Distinct from the
	// "parked" rules above, which only fire when the loop hit a
	// hard cap; this fires on completed turns that just went wide.
	if s.ToolSteps >= 30 && !s.Parked {
		if push(Note{
			Text:     fmt.Sprintf("Wide turn: %d tool calls. If the same kind of work repeats, the next pass benefits from /split into focused subtasks (or a tighter [[file:...]] marker so retrieval seeds the right region).", s.ToolSteps),
			Severity: SeverityInfo,
			Origin:   "tool_flood",
		}) {
			return out
		}
	}

	// Mutations without ever checking git state. The model edited
	// files but never ran git_status / git_diff this turn, so the
	// answer can't tell the user how this turn's changes compose
	// with whatever was already in the working tree (e.g. a stash
	// of unrelated WIP). Worth a heads-up so the user runs
	// `git status` themselves before assuming the mutations stand
	// alone.
	if len(s.Mutations) > 0 &&
		countTool(s.ToolsUsed, "git_status") == 0 &&
		countTool(s.ToolsUsed, "git_diff") == 0 {
		if push(Note{
			Text:     "Mutations landed without a git_status / git_diff this turn. Worth running `git status` before treating the changes as standalone — pre-existing WIP in the tree can mix with this turn's edits.",
			Severity: SeverityInfo,
			Origin:   "mutation_blind",
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
		"ekleme", "silme", "duzeltme", "yazma",
	}
	for _, k := range keys {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// countTool returns how many times `name` appears in the (call-order)
// tools-used slice. Used by the rules above to distinguish "the
// model used grep once" from "the model thrashed grep ten times".
// Empty/whitespace tool names are ignored — defensive against any
// future Snapshot path that pushes blank entries.
func countTool(used []string, name string) int {
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return 0
	}
	n := 0
	for _, u := range used {
		if strings.EqualFold(strings.TrimSpace(u), target) {
			n++
		}
	}
	return n
}

func trimPaths(paths []string, max int) []string {
	if len(paths) <= max {
		return paths
	}
	out := append([]string{}, paths[:max]...)
	out = append(out, fmt.Sprintf("+%d more", len(paths)-max))
	return out
}
