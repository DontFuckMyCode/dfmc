package tui

// describe_workflow_summary.go — per-turn recap card emitted at
// agent:loop:final and the small TODO counter that feeds it.
// Companion siblings:
//
//   - describe_workflow.go        workflowTodos / summarize / format
//                                 helpers + recent activity / timeline
//                                 / latest plan summary
//   - describe_workflow_stats.go  /stats card + autonomy* health rows
//   - describe_workflow_panels.go /workflow, /todos, /subagents,
//                                 /queue describe panels
//
// buildTurnSummary surfaces what the user actually wants to know
// after a long unattended run: how long did it take, how many tool
// calls, what files did it touch, did it validate, did the coach
// intervene, how close to the cumulative ceiling. Each row only
// appears when the value is non-zero so the card adapts to what
// actually happened.

import (
	"fmt"
	"strings"
	"time"
)

// todoCountsForSummary returns (total, done, pending) over the active
// todo_write snapshot at the moment the turn finalises. Done counts
// completed/done; pending counts pending/blocked/skipped/waiting/
// verifying/external_review (mirrors the live render path so the
// summary number matches what the user has been seeing in /workflow).
// Doing/in-progress is excluded from "pending" so an explicit ABC
// status doesn't get rolled into a "still queued" bucket. Returns
// zeros when the engine or tools registry is nil (early init / tests).
func todoCountsForSummary(m Model) (total, done, pending int) {
	if m.eng == nil || m.eng.Tools == nil {
		return 0, 0, 0
	}
	for _, it := range m.eng.Tools.TodoSnapshot() {
		total++
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			done++
		case "pending", "blocked", "skipped", "waiting", "verifying", "external_review":
			pending++
		}
	}
	return total, done, pending
}

// buildTurnSummary renders a multi-line recap of an agent turn that
// finished. Returns "" for trivial turns (zero edits, no validation,
// no coach activity) — the assistant answer itself is the report in
// that case and a card would be noise.
//
// Output shape:
//
//	▸ Turn summary
//	  duration:    2m 14s
//	  tool calls:  12 round(s)
//	  files:       edited 5 (a.go, b.go, c.go, +2 more)
//	  validation:  3 passes ran (last unverified count: 0)
//	  coach:       2 intervention(s) — see scrollback for detail
//	  ceiling:     78/600 cumulative steps used (13%)
//	  compacts:    4 cycles (reclaimed 142.0k tok)
//	  cache:       3 hits · saved that many round-trips
//	  errors:      8 tool failures (recovered to final answer)
func buildTurnSummary(s agentLoopState, todoTotal, todoDone, todoPending int) string {
	hasEdits := len(s.turnEditedFiles) > 0
	hasValidation := s.turnValidationPasses > 0
	hasCoach := s.turnCoachInterventions > 0
	hasCeiling := s.stepCeiling > 0 && s.cumulativeSteps > 0
	hasTodos := todoTotal > 0
	hasCompacts := s.compactsThisTurn > 0
	hasCache := s.cacheHitsThisTurn > 0
	hasErrors := s.toolErrorsThisTurn > 0

	if !hasEdits && !hasValidation && !hasCoach && !hasCeiling && !hasTodos &&
		!hasCompacts && !hasCache && !hasErrors && s.toolRounds == 0 {
		return ""
	}

	lines := []string{"▸ Turn summary"}

	if !s.turnStartedAt.IsZero() {
		dur := time.Since(s.turnStartedAt).Round(time.Second)
		lines = append(lines, fmt.Sprintf("  duration:    %s", dur))
	}
	if s.toolRounds > 0 {
		lines = append(lines, fmt.Sprintf("  tool calls:  %d round(s)", s.toolRounds))
	}
	if hasEdits {
		preview := s.turnEditedFiles
		tail := ""
		if len(preview) > 3 {
			preview = preview[:3]
			tail = fmt.Sprintf(", +%d more", len(s.turnEditedFiles)-3)
		}
		lines = append(lines, fmt.Sprintf("  files:       edited %d (%s%s)",
			len(s.turnEditedFiles), strings.Join(preview, ", "), tail))
	}
	if hasValidation || hasEdits {
		// Validation row appears whenever there was either a validation
		// pass OR an edit — an edit-only row without a validation row
		// is itself a signal the user should see ("edited 5, ran 0").
		passWord := "passes"
		if s.turnValidationPasses == 1 {
			passWord = "pass"
		}
		stillUnverified := len(s.unvalidatedEdits)
		lines = append(lines, fmt.Sprintf("  validation:  %d %s ran (still unverified: %d)",
			s.turnValidationPasses, passWord, stillUnverified))
	}
	if hasCoach {
		word := "interventions"
		if s.turnCoachInterventions == 1 {
			word = "intervention"
		}
		lines = append(lines, fmt.Sprintf("  coach:       %d %s — see scrollback for detail",
			s.turnCoachInterventions, word))
	}
	if hasCeiling {
		pct := (s.cumulativeSteps * 100) / s.stepCeiling
		lines = append(lines, fmt.Sprintf("  ceiling:     %d/%d cumulative steps used (%d%%)",
			s.cumulativeSteps, s.stepCeiling, pct))
	}
	if hasTodos {
		// Plan progress, surfaced when the agent used todo_write to
		// shard its work. Mention pending separately so the user can
		// tell "all done" from "halfway, more to go" at a glance.
		pendingHint := ""
		if todoPending > 0 {
			pendingHint = fmt.Sprintf(" · %d still pending", todoPending)
		}
		lines = append(lines, fmt.Sprintf("  todos:       %d of %d done%s",
			todoDone, todoTotal, pendingHint))
	}
	if hasCompacts {
		// Auto-compact pressure for this turn — surfaces a budget-thrashing
		// turn even after the live runtime badge clears at agent:loop:final.
		// Without this row, "we compacted 4 times" disappears the moment the
		// loop ends and the user has no record of how heavy the turn was.
		reclaimHint := ""
		if s.compactReclaimedTurn > 0 {
			reclaimHint = fmt.Sprintf(" (reclaimed %s tok)", compactMetric(s.compactReclaimedTurn))
		}
		cyc := "cycles"
		if s.compactsThisTurn == 1 {
			cyc = "cycle"
		}
		lines = append(lines, fmt.Sprintf("  compacts:    %d %s%s",
			s.compactsThisTurn, cyc, reclaimHint))
	}
	if hasCache {
		// Silent token savings — cache hits don't fire tool:call/tool:result
		// so they're invisible in the chip strip. Adding the row to the
		// turn summary preserves the signal in scrollback.
		hits := "hits"
		if s.cacheHitsThisTurn == 1 {
			hits = "hit"
		}
		lines = append(lines, fmt.Sprintf("  cache:       %d %s · saved that many round-trips",
			s.cacheHitsThisTurn, hits))
	}
	if hasErrors {
		// Per-turn fragility: how many tool calls failed (retried,
		// timed out, denied) before the loop reached a final answer.
		// Recovery is silent — chips scroll, the activity feed buries
		// individual rows. Without this row, a turn that limped through
		// 8 retries reads identically to a clean turn in scrollback.
		count := "tool failures"
		if s.toolErrorsThisTurn == 1 {
			count = "tool failure"
		}
		lines = append(lines, fmt.Sprintf("  errors:      %d %s (recovered to final answer)",
			s.toolErrorsThisTurn, count))
	}

	if len(lines) == 1 {
		// Only the header — nothing meaningful happened, drop the card.
		return ""
	}
	return strings.Join(lines, "\n")
}
