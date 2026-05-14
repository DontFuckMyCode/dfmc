package tui

// runtime_strip_budget.go — row builders for the "what is the engine
// spending and what should the user do next?" half of the runtime
// strip: context budget breakdown (budget row), per-turn and session
// token/cost (tokens row), recent timeline excerpts (activity row),
// and contextual hint chips like "/continue", "approve or deny", or
// "F7 Activity errors for details" (next row). Companion siblings:
//
//   - runtime_strip.go       renderRuntimeStrip dispatcher + Top
//                            and Now row builders
//   - runtime_strip_work.go  Work / Task / Orchestration / Tool row
//                            builders

import (
	"fmt"
	"strings"
)

func runtimeStripBudgetParts(vm runtimeViewModel) []string {
	parts := []string{}
	payload := vm.ContextPayload
	if task := strings.TrimSpace(vm.ContextTask); task != "" {
		parts = append(parts, "task "+task)
	}
	if vm.ContextFileCount > 0 {
		label := fmt.Sprintf("ctx files %d", vm.ContextFileCount)
		if vm.ContextMaxFiles > 0 {
			label += fmt.Sprintf("/%d", vm.ContextMaxFiles)
		}
		parts = append(parts, label)
	}
	evidenceTokens := payload.EvidenceTokens
	evidenceBudget := payload.EvidenceBudgetTokens
	if evidenceTokens <= 0 {
		evidenceTokens = vm.ContextTokens
	}
	if evidenceBudget <= 0 {
		evidenceBudget = vm.ContextBudgetTokens
	}
	if evidenceBudget > 0 {
		parts = append(parts, fmt.Sprintf("evidence %s/%s", compactMetric(evidenceTokens), compactMetric(evidenceBudget)))
	}
	if used, remaining := runtimeWindowUsage(vm); used > 0 {
		maxContext := payload.MaxContext
		if maxContext <= 0 {
			maxContext = vm.MaxContext
		}
		if maxContext > 0 {
			parts = append(parts, fmt.Sprintf("window %s/%s", compactMetric(used), compactMetric(maxContext)))
			if remaining >= 0 {
				parts = append(parts, "left "+compactMetric(remaining))
			} else {
				parts = append(parts, "over "+compactMetric(-remaining))
			}
		} else {
			parts = append(parts, "window "+compactMetric(used))
		}
	}
	if vm.ContextAvailable > 0 {
		parts = append(parts, "available "+compactMetric(vm.ContextAvailable))
	}
	if vm.ContextMaxPerFile > 0 {
		parts = append(parts, "slice "+compactMetric(vm.ContextMaxPerFile))
	}
	if payload.SystemTokens > 0 || payload.MessageTokens > 0 {
		parts = append(parts, fmt.Sprintf("sys %s hist %s", compactMetric(payload.SystemTokens), compactMetric(payload.MessageTokens)))
	}
	if payload.ResponseReserve > 0 || payload.ToolReserve > 0 {
		parts = append(parts, fmt.Sprintf("resp %s tools %s", compactMetric(payload.ResponseReserve), compactMetric(payload.ToolReserve)))
	}
	if c := strings.TrimSpace(vm.ContextCompression); c != "" {
		parts = append(parts, "zip "+c)
	}
	if file := firstUsefulRuntimeLine(vm.ContextTopFiles); file != "" {
		parts = append(parts, "top "+file)
	}
	if reason := firstUsefulRuntimeLine(vm.ContextReasons); reason != "" {
		parts = append(parts, "why "+reason)
	}
	return parts
}

func runtimeStripTokenParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.LastInputTokens > 0 || vm.LastOutputTokens > 0 || vm.LastTotalTokens > 0 {
		total := vm.LastTotalTokens
		if total <= 0 {
			total = vm.LastInputTokens + vm.LastOutputTokens
		}
		lastSegment := fmt.Sprintf("last in %s out %s total %s",
			compactMetric(vm.LastInputTokens),
			compactMetric(vm.LastOutputTokens),
			compactMetric(total),
		)
		if vm.CostPer1kTokens > 0 && total > 0 {
			lastCost := (float64(total) / 1000) * vm.CostPer1kTokens
			lastSegment += " · " + formatUSDCost(lastCost)
		}
		parts = append(parts, lastSegment)
	}
	sessionTotal := vm.SessionTotalTokens
	if sessionTotal <= 0 {
		sessionTotal = vm.SessionInputTokens + vm.SessionOutputTokens
	}
	if vm.SessionInputTokens > 0 || vm.SessionOutputTokens > 0 || sessionTotal > 0 {
		parts = append(parts, fmt.Sprintf("session in %s out %s total %s", compactMetric(vm.SessionInputTokens), compactMetric(vm.SessionOutputTokens), compactMetric(sessionTotal)))
	}
	if vm.CostPer1kTokens > 0 && sessionTotal > 0 {
		cost := (float64(sessionTotal) / 1000) * vm.CostPer1kTokens
		parts = append(parts, "cost "+formatUSDCost(cost))
	}
	if vm.TranscriptInputTokens > 0 || vm.TranscriptOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("visible in %s out %s", compactMetric(vm.TranscriptInputTokens), compactMetric(vm.TranscriptOutputTokens)))
	}
	if vm.ComposerTokens > 0 {
		parts = append(parts, "composer "+compactMetric(vm.ComposerTokens))
	}
	return parts
}

func runtimeStripActivityParts(vm runtimeViewModel) []string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 2)
	add := func(line string) {
		line = strings.TrimSpace(humanizeWorkflowText(line))
		if line == "" {
			return
		}
		key := strings.ToLower(line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, line)
	}
	for _, line := range vm.WorkflowTimeline {
		add(line)
		if len(parts) >= 2 {
			return parts
		}
	}
	for _, line := range vm.WorkflowRecent {
		add(line)
		if len(parts) >= 2 {
			return parts
		}
	}
	return parts
}

func runtimeStripActionParts(vm runtimeViewModel) []string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 3)
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		key := strings.ToLower(action)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, action)
	}

	state := strings.ToLower(strings.TrimSpace(vm.State))
	status := strings.ToLower(strings.Join([]string{
		vm.WorkflowStatus,
		vm.WorkflowExecution,
		vm.LastStatus,
		strings.Join(vm.WorkflowRecent, " "),
	}, " "))
	switch {
	case state == "needs key" || state == "needs provider":
		add("/providers to pick or configure a model")
		add("/reload after updating env")
	case vm.ApprovalPending:
		add("approve or deny the pending tool")
	case vm.Parked:
		add("/continue to resume")
	case vm.QueuedCount > 0:
		add("/queue to inspect pending prompts")
	case strings.Contains(status, "stalled") || strings.Contains(status, "error") || strings.Contains(status, "failed"):
		add("F7 Activity errors for details")
		add("/retry after fixing the cause")
	case strings.Contains(status, "throttled") || strings.Contains(status, "rate limit"):
		add("wait for retry or switch provider")
	case vm.DriveBlocked > 0:
		add("F5 Workflow to unblock TODOs")
	case vm.Dirty || vm.Inserted > 0 || vm.Deleted > 0:
		add("/diff to inspect workspace changes")
	}
	for _, action := range runtimeContextPressureActions(vm) {
		add(action)
	}
	if len(parts) > 3 {
		return parts[:3]
	}
	return parts
}
