// Package context — trajectory.go is the rule engine for the trajectory
// coach. Companion siblings:
//
//	trajectory_format.go  — public types (TraceEntry / TrajectoryOutput)
//	                        and FormatTrajectoryHints renderer.
//	trajectory_detect.go  — repeated-call/failure/unvalidated-edit
//	                        detectors and string canonicalizers.
//	trajectory_helpers.go — round summary + confidence + path
//	                        abbrev + shell→tool mapping + small
//	                        string utilities every rule reaches for.
//
// Design notes:
//   - Hints are *micro-touches* — 1-2 short sentences per hint, max 2
//     hints per turn. The user called these "minik dokunuslar" and
//     asked for post-execution-shaped prompts. Too many hints becomes
//     noise.
//   - Hints are stateless per-turn: the caller is expected to track
//     which hint text it already injected (via recentHints) so we
//     don't repeat ourselves round after round.
//   - All rules prefer observable facts (tool name, arg values, output
//     size, error text) over interpretation. We never hallucinate.
package context

import (
	"fmt"
	"strings"
)

// TraceEntry + EffectiveTool + TrajectoryOutput live in
// trajectory_format.go.

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

// FormatTrajectoryHints lives in trajectory_format.go.
