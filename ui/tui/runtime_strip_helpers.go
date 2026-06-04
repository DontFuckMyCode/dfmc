package tui

// runtime_strip_helpers.go — small lookup helpers and badge builders
// used by runtime_strip.go. Stateless: no Model receivers, no
// package-level state. Splits out the per-badge style ramps and the
// "first useful line" filters so the renderer in runtime_strip.go
// stays a thin assembly layer.

import (
	"fmt"
	"strings"
)

// liveLoopTokensBadge renders the "loop ~47k/250k" indicator when the
// agent loop is active. Style escalates with proximity to the per-turn
// budget cap so the user can see when a force-compact is about to fire:
//
//	<70% → subtle (quiet status counter)
//	70-90% → info (compaction is approaching)
//	>=90% → warn (compaction or budget park imminent)
//
// Returns "" when the loop isn't running (LiveLoopTokens==0) so the
// strip stays clean between turns. When LiveLoopBudgetCap is zero we
// still render the count alone (some configs disable the budget) so
// the user always sees the live working set during a turn.
func liveLoopTokensBadge(vm runtimeViewModel) string {
	if vm.LiveLoopTokens <= 0 {
		return ""
	}
	if vm.LiveLoopBudgetCap <= 0 {
		return subtleStyle.Render(fmt.Sprintf("loop ~%s", formatTokenCount(vm.LiveLoopTokens)))
	}
	pct := (vm.LiveLoopTokens * 100) / vm.LiveLoopBudgetCap
	label := fmt.Sprintf("loop ~%s/%s",
		formatTokenCount(vm.LiveLoopTokens),
		formatTokenCount(vm.LiveLoopBudgetCap),
	)
	switch {
	case pct >= 90:
		return warnStyle.Render(label)
	case pct >= 70:
		return infoStyle.Render(label)
	default:
		return subtleStyle.Render(label)
	}
}

// compactsThisTurnBadge renders "compacts ×N · -Mk" when the engine has
// run at least one auto-compact during the current turn. Surfaces a
// budget-thrashing turn so the user can spot one without watching the
// activity feed event-by-event. Style escalates: 1 = info nudge,
// 2-3 = info bumped, 4+ = warn (the loop is barely keeping up and a
// scope refinement is probably warranted).
//
// Returns "" when the turn hasn't compacted yet (CompactsThisTurn == 0)
// so the strip stays quiet on healthy short turns.
func compactsThisTurnBadge(vm runtimeViewModel) string {
	if vm.CompactsThisTurn <= 0 {
		return ""
	}
	label := fmt.Sprintf("compacts ×%d", vm.CompactsThisTurn)
	if vm.CompactReclaimedThisTurn > 0 {
		label = fmt.Sprintf("compacts ×%d · -%s reclaimed",
			vm.CompactsThisTurn,
			formatTokenCount(vm.CompactReclaimedThisTurn),
		)
	}
	switch {
	case vm.CompactsThisTurn >= 4:
		return warnStyle.Render(label)
	default:
		return infoStyle.Render(label)
	}
}

// autoResumeBadge renders a one-token "auto · S/SCeil" badge when the
// autonomous wrapper has accumulated work across resumes. Style ramps
// from neutral → info → warn as proximity to the ceiling grows: under
// 50% reads as a quiet status counter, 50-80% bumps to info to invite
// attention, ≥80% switches to warn so the user can refine scope before
// the engine refuses. Empty string when no auto-resume has fired
// (StepCeiling==0) so the badge stays hidden in the common case.
func autoResumeBadge(vm runtimeViewModel) string {
	if vm.StepCeiling <= 0 || vm.CumulativeSteps <= 0 {
		return ""
	}
	stepPct := (vm.CumulativeSteps * 100) / vm.StepCeiling
	tokPct := 0
	if vm.TokenCeiling > 0 && vm.CumulativeTokens > 0 {
		tokPct = (vm.CumulativeTokens * 100) / vm.TokenCeiling
	}
	hottest := stepPct
	if tokPct > hottest {
		hottest = tokPct
	}
	label := fmt.Sprintf("auto %d/%d", vm.CumulativeSteps, vm.StepCeiling)
	if vm.TokenCeiling > 0 {
		label = fmt.Sprintf("auto %d/%d · %s/%s",
			vm.CumulativeSteps, vm.StepCeiling,
			formatTokenCount(vm.CumulativeTokens),
			formatTokenCount(vm.TokenCeiling),
		)
	}
	switch {
	case hottest >= 80:
		return warnStyle.Render(label)
	case hottest >= 50:
		return infoStyle.Render(label)
	default:
		return subtleStyle.Render(label)
	}
}

func runtimeWindowUsage(vm runtimeViewModel) (int, int) {
	if vm.ContextPayload.WindowTokens > 0 {
		if vm.ContextPayload.MaxContext <= 0 {
			return vm.ContextPayload.WindowTokens, -1
		}
		return vm.ContextPayload.WindowTokens, vm.ContextPayload.FreeTokens
	}
	used := vm.ContextWindowTokens
	if used <= 0 {
		used = vm.ContextSystemTokens + vm.ContextHistoryTokens + vm.ContextTokens
	}
	if used <= 0 {
		used = vm.ContextTokens
	}
	if used <= 0 {
		return 0, 0
	}
	if vm.MaxContext <= 0 {
		return used, -1
	}
	return used, vm.MaxContext - used
}

func runtimeContextPressureActions(vm runtimeViewModel) []string {
	used, _ := runtimeWindowUsage(vm)
	if used <= 0 || vm.MaxContext <= 0 {
		return nil
	}
	pct := int((int64(used) * 100) / int64(vm.MaxContext))
	// Hint copy reflects what actually frees engine context (NOT the
	// TUI-only /compact slash command — that only collapses visible
	// transcript lines, the engine's running working set is untouched).
	// Real reducers: narrow @file mentions, drop pinned files, /chat
	// new for a fresh conversation, or wait for auto-compact to fire.
	switch {
	case pct >= 100:
		return []string{
			fmt.Sprintf("context over window: %s/%s", compactMetric(used), compactMetric(vm.MaxContext)),
			"/conv new or narrow @files / drop pins",
		}
	case pct >= 90:
		return []string{
			fmt.Sprintf("context critical: %d%% full", pct),
			"/conv new for fresh window · /context to inspect budget",
		}
	case pct >= 70:
		return []string{
			fmt.Sprintf("context high: %d%% full", pct),
			"/context budget — narrow @files before the next big ask",
		}
	default:
		return nil
	}
}

func runtimeStateStyle(style string) func(...string) string {
	switch style {
	case "accent":
		return accentStyle.Bold(true).Render
	case "info":
		return infoStyle.Bold(true).Render
	case "warn":
		return warnStyle.Bold(true).Render
	case "fail":
		return failStyle.Bold(true).Render
	default:
		return okStyle.Bold(true).Render
	}
}

func runtimeGitLabel(vm runtimeViewModel) string {
	if strings.TrimSpace(vm.Branch) == "" && !vm.Dirty && vm.Inserted == 0 && vm.Deleted == 0 {
		return ""
	}
	branch := strings.TrimSpace(vm.Branch)
	if branch == "" {
		branch = "worktree"
	}
	if vm.Dirty {
		branch += "*"
	}
	branch = truncateStr(branch, 22)
	return fmt.Sprintf("git %s +%d -%d", branch, vm.Inserted, vm.Deleted)
}

func firstUsefulSubagentLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "idle") || strings.EqualFold(line, "recent:") {
			continue
		}
		line = strings.TrimPrefix(line, "Subagent ")
		return line
	}
	return ""
}

func firstUsefulTaskLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "plan ") {
			continue
		}
		return line
	}
	return ""
}

func firstUsefulRuntimeLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
