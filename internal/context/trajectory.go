// Package context — trajectory.go derives short "dynamic" hints from the
// running agent's tool-call history. These hints are injected between loop
// rounds so the model sees lightweight, evidence-based coaching that
// reflects what the run actually did, not just the initial system prompt.
//
// Design notes:
//   - Hints are *micro-touches* — 1-2 short sentences per hint, max 2 hints
//     per turn. The user called these "minik dokunuslar" and asked for
//     post-execution-shaped prompts. Too many hints becomes noise.
//   - Hints are stateless per-turn: the caller is expected to track which
//     hint text it already injected (via recentHints) so we don't repeat
//     ourselves round after round.
//   - All rules prefer observable facts (tool name, arg values, output
//     size, error text) over interpretation. We never hallucinate.
package context

import (
	"fmt"
	"strings"
)

// TraceEntry is a trimmed view of one tool-call + result pair. The caller
// populates only what it can cheaply see from the agent loop — we keep the
// surface narrow on purpose.
type TraceEntry struct {
	Tool          string         // e.g., "edit_file", "tool_call" (+Inner for bridged)
	Inner         string         // backend tool name when Tool=="tool_call"; else ""
	Args          map[string]any // provider-reported input
	OutputPreview string         // first ~400 chars of Result.Output
	OutputChars   int            // full byte length of Output
	Ok            bool           // true when Err is empty
	Err           string         // tool error text when Ok==false
	Step          int            // loop step when the call occurred
}

// EffectiveTool returns the user-facing tool name — for bridged calls we
// surface the backend tool (tool_call("grep_codebase") → "grep_codebase").
func (t TraceEntry) EffectiveTool() string {
	if strings.TrimSpace(t.Inner) != "" {
		return t.Inner
	}
	return t.Tool
}

// TrajectoryOutput bundles the trajectory hints with metadata about the round.
type TrajectoryOutput struct {
	Hints         []string // up to 2 short coaching lines
	RoundSummary  string   // one-line recap of the round
	OpenQuestions []string // unresolved issues for the next round
	Confidence    float64  // 0-1; low triggers expanded retrieval on next round

	// Stuck* fields are populated when Rule 0 (repeated-failure) fires.
	// They give downstream surfaces (TUI chip, web activity feed, metrics)
	// a structured way to render the pattern without grepping the hint
	// text. Empty StuckTool means no stuck pattern this round.
	StuckTool      string
	StuckCount     int
	StuckErrSample string

	// Unverified* fields surface the running unvalidated-edits count,
	// computed every round (not just when Rule 2 escalates). Engine
	// publishes a separate agent:coach:unverified event when the count
	// crosses the escalation threshold so the TUI / web feed can
	// correlate the always-visible "unverified: N" badge with a
	// matching warn notice in the chat scrollback. UnverifiedCount==0
	// means no current streak.
	UnverifiedCount     int
	UnverifiedPaths     []string
	UnverifiedEscalated bool // true when Rule 2 fired its directive form
}

// TrajectoryHints returns up to 2 short coaching lines derived from the
// most recent round of tool calls. `fresh` is the slice of traces from the
// *current* loop step; `all` is the running history including fresh; both
// may be empty. `recent` is a short de-dup window of hints already injected
// in this conversation — rules skip if they'd re-emit an already-seen hint.
//
// RoundSummary is a one-line recap of what the round accomplished.
// OpenQuestions lists unresolved issues the next round should address.
// Confidence is 0-1: low confidence (< 0.5) triggers expanded retrieval.
func TrajectoryHints(fresh, all []TraceEntry, recent []string) *TrajectoryOutput {
	if len(fresh) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, h := range recent {
		seen[strings.TrimSpace(h)] = struct{}{}
	}
	out := make([]string, 0, 2)
	push := func(line string) bool {
		line = strings.TrimSpace(line)
		if line == "" {
			return false
		}
		if _, dup := seen[line]; dup {
			return false
		}
		seen[line] = struct{}{}
		out = append(out, line)
		return len(out) >= 2
	}

	// Build round summary from fresh traces.
	roundSummary := buildRoundSummary(fresh)

	// Collect open questions.
	var openQuestions []string

	// Detect stuck pattern up-front so the structured fields can ride
	// every return — even when Rule 0's TEXT was deduped out (already
	// emitted earlier in the run), the engine should still receive the
	// signal for a fresh `agent:coach:stuck` event so the TUI surfaces
	// the count even on subsequent rounds.
	stuck := detectRepeatedFailures(all)
	// Unvalidated mutations across the whole history. We compute it
	// once up-front so build() can attach it to every return — Rule 2
	// is the only place the *escalated* directive hint fires, but the
	// COUNT is structural data the engine should always surface.
	unverifiedCount, unverifiedPaths := countUnvalidatedMutations(all)
	unverifiedEscalated := false
	build := func() *TrajectoryOutput {
		o := &TrajectoryOutput{
			Hints:         out,
			RoundSummary:  roundSummary,
			OpenQuestions: openQuestions,
			Confidence:    computeConfidence(fresh),
		}
		if stuck.tool != "" {
			o.StuckTool = stuck.tool
			o.StuckCount = stuck.count
			o.StuckErrSample = stuck.errSample
		}
		if unverifiedCount > 0 {
			o.UnverifiedCount = unverifiedCount
			o.UnverifiedPaths = append([]string(nil), unverifiedPaths...)
			o.UnverifiedEscalated = unverifiedEscalated
		}
		return o
	}

	// Rule 0: same effective tool failed N times across recent rounds.
	// This is the stuck-in-a-loop pattern — model keeps guessing paths
	// with read_file, or keeps re-running a command with the same broken
	// syntax. Single-shot Rule 1 below catches one failure per turn but
	// never escalates when the FAILURE keeps repeating across rounds, so
	// a long autonomous run can silently burn dozens of steps re-trying
	// the same approach. Fires before Rule 1 so the stronger pattern
	// hint takes precedence over the generic one.
	if stuck.tool != "" {
		hint := fmt.Sprintf(
			"%s has failed %d times across recent rounds with the same kind of error (%s). Stop retrying — switch tactic: confirm the input differently (e.g. `glob` to locate a file before `read_file`, `grep_codebase` to find the right symbol before guessing a path), or pick a different tool entirely.",
			stuck.tool, stuck.count, stuck.errSample,
		)
		openQuestions = append(openQuestions, fmt.Sprintf("%s stuck — %d consecutive failures", stuck.tool, stuck.count))
		if push(hint) {
			return build()
		}
	}

	// Rule 1: any failed tool this round → retry-safely hint. Highest
	// priority because silent retries burn budget fast.
	for _, t := range fresh {
		if t.Ok {
			continue
		}
		et := t.EffectiveTool()
		brief := firstLine(t.Err)
		if brief == "" {
			brief = "unknown error"
		}
		msg := fmt.Sprintf("Prior call %s failed (%s). Don't retry with the same inputs — read the error, adjust arguments, or pick a different tool.", et, brief)
		openQuestions = append(openQuestions, fmt.Sprintf("%s failed: %s", et, brief))
		if push(msg) {
			return build()
		}
		break // one failure hint per turn
	}

	// Rule 2: wrote/edited a file → remind about validation. Wording
	// escalates with the running unvalidated-edit count — a single edit
	// gets the gentle "validate this" nudge; three or more across recent
	// rounds (without a build/test pass clearing the streak) gets a
	// directive "you're shipping unverified work, run a test NOW".
	// Only fires on a fresh mutation in the current round so it doesn't
	// spam between successful turns.
	freshMutated := false
	freshPath := ""
	for i := len(fresh) - 1; i >= 0; i-- {
		t := fresh[i]
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file", "write_file", "apply_patch":
			freshMutated = true
			freshPath = strings.TrimSpace(argAsString(t.Args, "path"))
			if freshPath == "" {
				freshPath = strings.TrimSpace(argAsString(t.Args, "file"))
			}
		}
		// Only consider the most recent successful trace.
		if t.Ok {
			break
		}
	}
	if freshMutated {
		var hint string
		switch {
		case unverifiedCount >= 3:
			unverifiedEscalated = true
			tail := ""
			if len(unverifiedPaths) > 0 {
				preview := unverifiedPaths
				if len(preview) > 4 {
					preview = preview[:4]
					tail = fmt.Sprintf(" (%s, +%d more)", strings.Join(preview, ", "), len(unverifiedPaths)-4)
				} else {
					tail = " (" + strings.Join(preview, ", ") + ")"
				}
			}
			hint = fmt.Sprintf(
				"You've mutated %d files%s without running a single build/test/vet. The risk of compounding regression is real — STOP editing and run the smallest validation that exercises these changes (e.g. `go test ./<changed-package>/...`, `go vet ./...`, the project's test command). Resume edits only after you have a clean signal.",
				unverifiedCount, tail,
			)
			openQuestions = append(openQuestions, fmt.Sprintf("%d unvalidated mutations queued — verification is overdue", unverifiedCount))
		default:
			path := freshPath
			if path == "" {
				path = "the file you just changed"
			}
			hint = "Just mutated " + path + ". Validate with the smallest targeted check (build/vet/test that touches it) before declaring done — don't trust edits on faith."
		}
		if push(hint) {
			return build()
		}
	}

	// Rule 3: large search result → narrow before widening.
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		if t.EffectiveTool() != "grep_codebase" {
			continue
		}
		if t.OutputChars < 4000 && !strings.Contains(t.OutputPreview, "truncated") {
			continue
		}
		hint := "grep_codebase returned a lot. Narrow with a tighter regex or `glob` filter before expanding — wide scans waste the context budget."
		if push(hint) {
			return build()
		}
	}

	// Rule 4: repeated calls to the same tool with similar args → consolidate.
	if dup := detectRepeatedCalls(all); dup != "" {
		hint := "You've called " + dup + " several times on similar inputs. Consolidate via tool_batch_call, or rethink whether another tool would answer the question in one shot."
		if push(hint) {
			return build()
		}
	}

	// Rule 5: shell did the wrong job. run_command used for things that
	// have a dedicated tool.
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		if t.EffectiveTool() != "run_command" {
			continue
		}
		cmd := strings.TrimSpace(argAsString(t.Args, "command"))
		if cmd == "" {
			continue
		}
		if alt := preferDedicatedTool(cmd); alt != "" {
			hint := "run_command was used for a task with a dedicated tool: prefer " + alt + " next time — it's safer and the output is structured."
			if push(hint) {
				return build()
			}
		}
	}

	return build()
}

// buildRoundSummary produces a one-line recap of the round's activity.
func buildRoundSummary(fresh []TraceEntry) string {
	if len(fresh) == 0 {
		return ""
	}
	var actions []string
	searched := false
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file":
			actions = append(actions, "edited "+abbrevPath(argAsString(t.Args, "path")))
		case "write_file":
			actions = append(actions, "wrote "+abbrevPath(argAsString(t.Args, "path")))
		case "apply_patch":
			actions = append(actions, "applied patch")
		case "grep_codebase":
			searched = true
		case "read_file":
			actions = append(actions, "read "+abbrevPath(argAsString(t.Args, "path")))
		case "codemap":
			actions = append(actions, "explored codemap")
		case "run_command":
			actions = append(actions, "ran command")
		}
	}
	if len(actions) == 0 {
		if searched {
			return "searched codebase"
		}
		return "no significant file activity"
	}
	if len(actions) > 3 {
		actions = actions[:3]
		actions = append(actions, "...")
	}
	return strings.Join(actions, ", ")
}

// computeConfidence returns 0-1 based on how many tools succeeded and whether
// there are unresolved errors or large search results.
func computeConfidence(fresh []TraceEntry) float64 {
	if len(fresh) == 0 {
		return 0.5
	}
	ok := 0
	for _, t := range fresh {
		if t.Ok {
			ok++
		}
	}
	rate := float64(ok) / float64(len(fresh))
	// Deduct for failures.
	if rate < 1.0 {
		rate -= 0.1
	}
	if rate < 0 {
		rate = 0
	}
	return rate
}

// abbrevPath returns the last path component, trimmed.
func abbrevPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// detectRepeatedCalls returns the name of a tool that was called 3+ times
// in the last ~6 traces with overlapping argument values. Empty string
// when nothing looks repetitive.
func detectRepeatedCalls(all []TraceEntry) string {
	if len(all) < 3 {
		return ""
	}
	window := all
	if len(window) > 6 {
		window = window[len(window)-6:]
	}
	counts := map[string]int{}
	argSeen := map[string]map[string]struct{}{}
	for _, t := range window {
		if !t.Ok {
			continue
		}
		name := t.EffectiveTool()
		counts[name]++
		if argSeen[name] == nil {
			argSeen[name] = map[string]struct{}{}
		}
		argSeen[name][canonicalArgFingerprint(t.Args)] = struct{}{}
	}
	for name, n := range counts {
		if n < 3 {
			continue
		}
		// Only flag when there's argument overlap (same fingerprint ≥ twice).
		unique := len(argSeen[name])
		if unique <= n-1 {
			return name
		}
	}
	return ""
}

// repeatedFailure summarizes a "same tool keeps failing" pattern detected
// across the running history window. errSample is the first failure's
// error fingerprint (first ~80 chars) — naming the error class helps the
// model recognise the pattern instead of just being told "stop".
type repeatedFailure struct {
	tool      string
	count     int
	errSample string
}

// detectRepeatedFailures returns the effective tool name that failed 3+
// times across the last ~8 traces with similar error fingerprints. The
// window is wider than detectRepeatedCalls (6) because failure loops
// often interleave a recovery attempt or two, and we want to catch the
// pattern before it has burned 10+ rounds. Empty when no such pattern.
//
// "Similar error fingerprint" means the first ~30 chars of the error
// message (lowercased, whitespace-collapsed) are identical for at least
// 2 of the failures — this catches genuine "same-cause" loops without
// false-positiving on legitimate retries that hit different errors.
func detectRepeatedFailures(all []TraceEntry) repeatedFailure {
	if len(all) < 3 {
		return repeatedFailure{}
	}
	window := all
	if len(window) > 8 {
		window = window[len(window)-8:]
	}
	type bucket struct {
		count    int
		errs     map[string]int
		firstErr string
	}
	buckets := map[string]*bucket{}
	for _, t := range window {
		if t.Ok {
			continue
		}
		name := t.EffectiveTool()
		if name == "" {
			continue
		}
		b, ok := buckets[name]
		if !ok {
			b = &bucket{errs: map[string]int{}}
			buckets[name] = b
		}
		b.count++
		fp := errorFingerprint(t.Err)
		b.errs[fp]++
		if b.firstErr == "" {
			b.firstErr = firstLine(t.Err)
		}
	}
	for name, b := range buckets {
		if b.count < 3 {
			continue
		}
		// At least one fingerprint must repeat — otherwise the failures
		// are unrelated and a "stop retrying" hint would be wrong advice.
		repeats := false
		for _, n := range b.errs {
			if n >= 2 {
				repeats = true
				break
			}
		}
		if !repeats {
			continue
		}
		sample := b.firstErr
		if sample == "" {
			sample = "unknown error"
		}
		return repeatedFailure{tool: name, count: b.count, errSample: sample}
	}
	return repeatedFailure{}
}

// countUnvalidatedMutations walks the running trace history and returns
// the number of distinct successful edits since the last successful
// validation command (build/test/vet/etc), plus the per-file path list
// (de-duped, in first-seen order). Walking history-wide rather than the
// single round is exactly what makes the rule state-aware: a model
// editing one file per round across 5 rounds will see count=5 even
// though each round's `fresh` only has one mutation.
//
// "Validation command" is recognised by the leading verb of the
// run_command call's `command` arg — kept in rough sync with the TUI's
// isValidationCommand. Any successful run_command matching this set
// resets the count, so the helper effectively returns "edits since the
// most recent good test pass". When no validation has ever happened
// the count covers every successful mutation in the history.
func countUnvalidatedMutations(all []TraceEntry) (int, []string) {
	if len(all) == 0 {
		return 0, nil
	}
	// Walk forward, but reset on each validation. Final count = edits
	// since last validation event (or beginning, whichever later).
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(all))
	for _, t := range all {
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file", "write_file", "apply_patch":
			path := strings.TrimSpace(argAsString(t.Args, "path"))
			if path == "" {
				path = strings.TrimSpace(argAsString(t.Args, "file"))
			}
			// apply_patch may not carry a single path arg; fall back to
			// a synthetic placeholder so the count is still accurate.
			if path == "" {
				path = fmt.Sprintf("(patch@step%d)", t.Step)
			}
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		case "run_command":
			cmd := strings.TrimSpace(argAsString(t.Args, "command"))
			if isValidationVerb(cmd) {
				seen = map[string]struct{}{}
				paths = paths[:0]
			}
		}
	}
	return len(paths), paths
}

// isValidationVerb mirrors the TUI's isValidationCommand. Kept here so
// the trajectory layer is self-contained (no UI imports). Both surfaces
// answer the same question — "did this command count as a verification
// pass?" — so update them together when adding a new test runner.
func isValidationVerb(cmd string) bool {
	cmd = strings.TrimSpace(strings.ToLower(cmd))
	if cmd == "" {
		return false
	}
	multi := []string{
		"go test", "go vet", "go build",
		"npm test", "pnpm test", "yarn test",
		"npm run test", "pnpm run test", "yarn run test",
		"cargo test", "cargo check", "cargo build", "cargo clippy",
	}
	for _, prefix := range multi {
		if cmd == prefix || strings.HasPrefix(cmd, prefix+" ") {
			return true
		}
	}
	single := []string{"pytest", "tsc", "eslint", "biome", "mypy", "ruff", "make"}
	for _, prefix := range single {
		if cmd == prefix || strings.HasPrefix(cmd, prefix+" ") {
			return true
		}
	}
	return false
}

// errorFingerprint normalizes an error message for repeat-detection. We
// keep the leading prefix (the error CLASS — "file not found", "no such
// file", "permission denied") and drop trailing path/value tails that
// vary per call. Lowercase + collapsed whitespace makes "File not Found"
// and "file not  found" hash the same.
func errorFingerprint(err string) string {
	s := strings.ToLower(strings.TrimSpace(err))
	if s == "" {
		return ""
	}
	// Collapse runs of whitespace to single spaces.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// canonicalArgFingerprint returns a stable string for similar-looking args.
// We deliberately strip long values so "read file A lines 1-20" and
// "read file A lines 40-60" count as the same fingerprint when the path is
// the same (repeated file-bouncing is a bad pattern either way).
func canonicalArgFingerprint(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := []string{"path", "file", "pattern", "query", "command", "name"}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(argAsString(args, k))
		if v == "" {
			continue
		}
		if len(v) > 48 {
			v = v[:48]
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "|")
}

// preferDedicatedTool maps shell commands onto better-suited DFMC tools.
// Returns "" when the command is a legitimate shell-only task.
func preferDedicatedTool(cmd string) string {
	first := strings.ToLower(firstToken(cmd))
	switch first {
	case "cat", "head", "tail", "less", "more":
		return "read_file"
	case "grep", "rg", "ack", "ag":
		return "grep_codebase"
	case "find":
		return "glob"
	case "ls", "dir":
		return "list_dir"
	case "sed", "awk":
		return "edit_file or apply_patch"
	case "echo":
		// Only flag echo when it's redirecting to a file.
		if strings.Contains(cmd, ">") {
			return "write_file"
		}
	case "curl", "wget":
		return "web_fetch"
	}
	return ""
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func argAsString(args map[string]any, key string) string {
	if len(args) == 0 || key == "" {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

// FormatTrajectoryHints wraps a TrajectoryOutput into a single system-note block
// suitable for injection as a user message between agent-loop rounds.
// Returns "" when there are no hints. When Confidence < 0.5, also includes
// the round summary and open questions to trigger expanded retrieval.
func FormatTrajectoryHints(out *TrajectoryOutput) string {
	if out == nil || len(out.Hints) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[trajectory coach]\n")
	for _, h := range out.Hints {
		b.WriteString("• ")
		b.WriteString(strings.TrimSpace(h))
		b.WriteByte('\n')
	}
	// When confidence is low, include the round summary so the next retrieval
	// pass does expanded exploration.
	if out.Confidence < 0.5 && strings.TrimSpace(out.RoundSummary) != "" {
		b.WriteString("• round: ")
		b.WriteString(strings.TrimSpace(out.RoundSummary))
		b.WriteByte('\n')
		for _, q := range out.OpenQuestions {
			b.WriteString("  open: ")
			b.WriteString(strings.TrimSpace(q))
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
