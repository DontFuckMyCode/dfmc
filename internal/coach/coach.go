// Package coach — background "tiny-touches" agent that reads a completed
// agent turn and surfaces short, user-facing commentary. Distinct from the
// trajectory hints in internal/context (those feed the MODEL between tool
// rounds); coach notes land in the USER's chat view.
//
// Scope: rule-based today, LLM-ready interface for tomorrow. The rules run
// in microseconds with no network I/O, so enabling the coach costs nothing
// until a remote analyzer is plugged in.
package coach

import (
	"fmt"
	"strings"
)

// Severity tags a coach note so the TUI can style it appropriately.
type Severity string

const (
	// SeverityInfo — neutral nudge, render dim/italic.
	SeverityInfo Severity = "info"
	// SeverityWarn — the model did something borderline (skipped
	// validation, drifted from the question, etc.). Render amber.
	SeverityWarn Severity = "warn"
	// SeverityCelebrate — the model handled something well. Render
	// subtle green. Used sparingly.
	SeverityCelebrate Severity = "celebrate"
)

// Note is a single coach remark. Short — typically under 120 chars.
type Note struct {
	Text     string   `json:"text"`
	Severity Severity `json:"severity"`
	// Origin names the rule that produced the note — useful for telemetry
	// and for suppressing noisy rules at the UI level.
	Origin string `json:"origin,omitempty"`
}

// Snapshot is the observable state of a just-finished agent turn. All
// fields are optional; rules check what they care about.
type Snapshot struct {
	Question     string
	Answer       string
	ToolSteps    int
	ToolsUsed    []string // names of tools the model invoked this turn
	FailedTools  []string // subset of ToolsUsed where the call errored
	Mutations    []string // paths the model wrote/edited this turn
	Parked       bool     // true when the loop hit step cap
	Provider     string
	Model        string
	TokensUsed   int
	ElapsedMs    int64
	TrajectoryHints []string // anything already injected between rounds

	// Retrieval-quality signals. Populated from the ContextChunks that were
	// fed to the model this turn. Rules use these to flag weak retrieval so
	// the user can either tighten the query or extend the graph.
	ContextFiles     int            // total chunks in the prompt
	ContextSources   map[string]int // ChunkSource* → count
	QueryIdentifiers int            // how many symbol-like tokens the query had
}

// Observer analyzes a snapshot and emits 0-N notes. Implementations must
// be cheap and side-effect-free — the engine calls Observe synchronously
// on the publish path and drops notes onto the event bus from there.
type Observer interface {
	Observe(Snapshot) []Note
}

// RuleObserver is the default offline Observer. Rules are deliberately
// small and explainable; each one corresponds to a single observable
// signal so the origin field is self-documenting.
type RuleObserver struct {
	// MaxNotes caps output. Zero means "use default (3)".
	MaxNotes int
}

// NewRuleObserver returns a RuleObserver with sensible defaults.
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

	// Parked loops: surface louder since the user has to decide /continue.
	if s.Parked {
		if push(Note{
			Text:     "Loop parked — hit its step cap. Type /continue to resume, optionally with a note to focus the next pass.",
			Severity: SeverityWarn,
			Origin:   "parked_loop",
		}) {
			return out
		}
	}

	// Mutations without validation: the answer doesn't mention running
	// tests/build after writing files. Weak heuristic — we look for the
	// absence of common validation verbs in the reply.
	if len(s.Mutations) > 0 && !answerMentionsValidation(s.Answer) {
		paths := strings.Join(trimPaths(s.Mutations, 3), ", ")
		if push(Note{
			Text:     fmt.Sprintf("Files mutated (%s) but the answer didn't mention a test/build/vet run — double-check before shipping.", paths),
			Severity: SeverityWarn,
			Origin:   "mutation_unvalidated",
		}) {
			return out
		}
	}

	// Repeated failures: >2 tools failed in the same turn → the model
	// is flailing.
	if len(s.FailedTools) >= 2 {
		if push(Note{
			Text:     fmt.Sprintf("%d tool call(s) failed this turn. Worth reading the error messages directly before asking for another pass.", len(s.FailedTools)),
			Severity: SeverityWarn,
			Origin:   "repeated_failures",
		}) {
			return out
		}
	}

	// Heavy turns: tokens > 20k means the model ran wide. Info-level.
	if s.TokensUsed > 20000 {
		if push(Note{
			Text:     fmt.Sprintf("Heavy turn (~%dk tokens). If you want a tighter next pass, try adding --provider offline or narrowing the question with a [[file:path]] marker.", s.TokensUsed/1000),
			Severity: SeverityInfo,
			Origin:   "heavy_turn",
		}) {
			return out
		}
	}

	// Pseudo-tool-call in answer text: the provider declared tool support
	// but the model fabricated a text-format tool call (e.g. `[TOOL_CALL]`
	// blocks) instead of using the structured channel. Signals that the
	// selected model doesn't actually honor native tool-calling — the
	// request round-tripped but the model replied in prose shaped like a
	// tool call. Actionable for the user (switch model or profile).
	if s.ToolSteps == 0 && containsPseudoToolCall(s.Answer) {
		if push(Note{
			Text:     "Model emitted a text-format tool call instead of using native tool-calling. Provider accepted the tools but this model didn't use them — try a different model on the same provider, or check the endpoint.",
			Severity: SeverityWarn,
			Origin:   "pseudo_tool_call",
		}) {
			return out
		}
	}

	// Long zero-tool answers with explicit questions: the model may have
	// decided to chat when an action was expected. Info-level.
	if s.ToolSteps == 0 && strings.Contains(s.Answer, "?") && looksActionable(s.Question) {
		if push(Note{
			Text:     "The model answered without using any tools. If you expected a code change, ask again with a more explicit action verb (edit, run, add).",
			Severity: SeverityInfo,
			Origin:   "no_action_taken",
		}) {
			return out
		}
	}

	// Weak retrieval: the query looked symbol-like (contained identifiers)
	// but none of the context chunks were tagged symbol-match — i.e. the
	// codemap had no node matching those identifiers. Either the codemap
	// didn't index the relevant file (new file, unsupported language) or
	// the user used an approximate name. Actionable: suggest narrowing.
	if s.QueryIdentifiers > 0 && s.ContextFiles > 0 && s.ContextSources != nil &&
		s.ContextSources["symbol-match"] == 0 && s.ContextSources["marker"] == 0 {
		if push(Note{
			Text:     "Retrieval didn't resolve any query identifier to a codemap symbol. If a specific function/type is in scope, reference it with [[file:path]] or rename-exact so the graph can seed from it.",
			Severity: SeverityInfo,
			Origin:   "retrieval_symbol_miss",
		}) {
			return out
		}
	}

	// All-hotspot retrieval: the context is filled purely by centrality
	// because nothing else ranked — very vague query. Lower-priority than
	// the symbol miss, fires only when there was no text match either.
	if s.ContextFiles > 0 && s.ContextSources != nil &&
		s.ContextSources["hotspot"] == s.ContextFiles {
		if push(Note{
			Text:     "Context came entirely from graph hotspots — the query didn't match any specific file. Add a [[file:path]] marker or a distinctive symbol name to focus the next pass.",
			Severity: SeverityInfo,
			Origin:   "retrieval_hotspot_only",
		}) {
			return out
		}
	}

	// Clean, efficient turn: short answer, tools used, no failures, no
	// mutation-unvalidated flag. Very light celebration, once.
	if len(s.ToolsUsed) > 0 && len(s.FailedTools) == 0 && !s.Parked && s.TokensUsed > 0 && s.TokensUsed < 8000 {
		if push(Note{
			Text:     "Clean pass — tools used, no failures, tight token spend.",
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
	// Any hit signals the model self-validated or told the user to.
	keys := []string{"go test", "go vet", "go build", "npm test", "pnpm test", "yarn test",
		"pytest", "cargo test", "cargo check", "tsc", "eslint", "biome", "run the test",
		"ran the test", "validated", "verified", "confirmed", "smoke test"}
	for _, k := range keys {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// containsPseudoToolCall looks for markup the model sometimes emits when
// it roleplays a tool call in free text instead of using structured
// tool_use/tool_calls channels. The markers are distinctive enough that
// false positives are rare in normal prose.
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
	keys := []string{"fix", "add", "implement", "edit", "refactor", "migrate", "remove",
		"delete", "rename", "update", "create", "build", "write", "generate",
		"wire up", "hook up", "wire", "ekle", "sil", "düzelt", "yaz"}
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
