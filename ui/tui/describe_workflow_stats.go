package tui

// describe_workflow_stats.go — /stats card + the autonomy health
// rows it composes. Companion siblings:
//
//   - describe_workflow.go         workflowTodos / summarize /
//                                  formatWorkflowTodoLines /
//                                  recentWorkflowActivity / Timeline /
//                                  latestWorkflowPlanSummary helpers
//   - describe_workflow_summary.go per-turn buildTurnSummary +
//                                  todoCountsForSummary
//   - describe_workflow_panels.go  /workflow, /todos, /subagents,
//                                  /queue describe panels

import (
	"fmt"
	"strings"
	"time"
)

func (m Model) describeStats() string {
	lines := []string{"▸ Session stats"}

	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = time.Since(m.sessionStart).Round(time.Second)
	}
	lines = append(lines, fmt.Sprintf("  elapsed:     %s", elapsed))
	lines = append(lines, fmt.Sprintf("  messages:    %d transcript line(s)", len(m.chat.transcript)))

	// Token budget. ContextIn carries the last computed budget if a turn
	// has run; otherwise fall back to the provider's MaxContext only.
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	if maxCtx > 0 {
		pct := 0
		if tokens > 0 {
			pct = int(float64(tokens) / float64(maxCtx) * 100)
		}
		lines = append(lines, fmt.Sprintf("  context in:  %s / %s tokens (%d%% of window)",
			formatThousands(tokens), formatThousands(maxCtx), pct))
	} else {
		lines = append(lines, "  context in:  (no provider window info yet)")
	}
	sessionInput, sessionOutput, sessionTotal := m.sessionTokenTotals()
	if sessionInput > 0 || sessionOutput > 0 || sessionTotal > 0 {
		lines = append(lines, fmt.Sprintf("  tokens:      in %s · out %s · total %s",
			formatThousands(sessionInput),
			formatThousands(sessionOutput),
			formatThousands(sessionTotal)))
		if costPer1k := m.currentCostPer1kTokens(); costPer1k > 0 {
			cost := (float64(sessionTotal) / 1000) * costPer1k
			lines = append(lines, fmt.Sprintf("  cost:        approx %s @ %s/1k tokens",
				formatUSDCost(cost),
				formatUSDCost(costPer1k)))
		}
	}

	// Agent loop progress (cumulative across turns).
	if m.agentLoop.toolRounds > 0 || m.agentLoop.step > 0 {
		phase := strings.TrimSpace(m.agentLoop.phase)
		if phase == "" {
			phase = "idle"
		}
		if m.agentLoop.maxToolStep > 0 {
			lines = append(lines, fmt.Sprintf("  agent:       %s · step %d/%d · %d tool round(s)",
				phase, m.agentLoop.step, m.agentLoop.maxToolStep, m.agentLoop.toolRounds))
		} else {
			lines = append(lines, fmt.Sprintf("  agent:       %s · step %d · %d tool round(s)",
				phase, m.agentLoop.step, m.agentLoop.toolRounds))
		}
		if last := strings.TrimSpace(m.agentLoop.lastTool); last != "" {
			lines = append(lines, fmt.Sprintf("  last tool:   %s (%s, %dms)",
				last, blankFallback(m.agentLoop.lastStatus, "?"), m.agentLoop.lastDuration))
		}
	} else {
		lines = append(lines, "  agent:       no tool rounds this session yet")
	}

	// Fan-out live counters.
	if m.telemetry.activeToolCount > 0 || m.telemetry.activeSubagentCount > 0 {
		lines = append(lines, fmt.Sprintf("  in-flight:   %d tool(s), %d subagent(s)", m.telemetry.activeToolCount, m.telemetry.activeSubagentCount))
	}

	// RTK-style compression wins — the headline token-miser metric.
	if m.telemetry.compressionRawChars > 0 {
		saved := m.telemetry.compressionSavedChars
		raw := m.telemetry.compressionRawChars
		pct := 0
		if raw > 0 {
			pct = int(float64(saved) / float64(raw) * 100)
		}
		lines = append(lines, fmt.Sprintf("  rtk savings: %s chars dropped (%d%% of %s raw output)",
			formatThousands(saved), pct, formatThousands(raw)))
	} else {
		lines = append(lines, "  rtk savings: (no tool output yet to compress)")
	}

	// Autonomy health — surfaces the same signals as the runtime strip
	// badges (stuck-loop, unverified edits, cumulative ceiling) but in
	// expanded-text form so a user typing /stats during an hour-long
	// run can read the full picture instead of decoding compact badges.
	// Each line only appears when the signal is active; quiet when
	// everything is healthy.
	if line := autonomyHealthLine(m.agentLoop); line != "" {
		lines = append(lines, line)
	}
	if line := autonomyCeilingLine(m.agentLoop); line != "" {
		lines = append(lines, line)
	}
	if line := autonomyUnverifiedLine(m.agentLoop); line != "" {
		lines = append(lines, line)
	}

	// Recent denials — short summary, full list lives in /approve.
	if m.eng != nil {
		if denials := m.eng.RecentDenials(); len(denials) > 0 {
			lines = append(lines, fmt.Sprintf("  denials:     %d blocked agent tool call(s) — see /approve", len(denials)))
		}

		// Prompt cache split — how much of the rendered system prompt
		// Anthropic can cache. Only visible when a sensible breakdown is
		// available; otherwise silent to keep the card tight.
		lastQuery := ""
		for i := len(m.chat.transcript) - 1; i >= 0; i-- {
			if m.chat.transcript[i].Role.Eq(chatRoleUser) {
				lastQuery = strings.TrimSpace(m.chat.transcript[i].Content)
				break
			}
		}
		rec := m.eng.PromptRecommendation(lastQuery)
		if rec.CacheableTokens+rec.DynamicTokens > 0 {
			lines = append(lines, fmt.Sprintf("  cache split: %d%% stable · %s cacheable / %s dynamic",
				rec.CacheablePercent,
				formatThousands(rec.CacheableTokens),
				formatThousands(rec.DynamicTokens)))
		}
	}

	return strings.Join(lines, "\n")
}

// autonomyHealthLine renders the stuck-loop signal as a /stats row.
// Empty when no current stall — quiet stats card stays quiet, noisy
// stats card surfaces the count + tool + error class so the user has
// full context for the always-visible runtime-strip badge.
func autonomyHealthLine(s agentLoopState) string {
	if strings.TrimSpace(s.stuckTool) == "" || s.stuckCount == 0 {
		return ""
	}
	tail := ""
	if cls := strings.TrimSpace(s.stuckErrClass); cls != "" {
		tail = " · err class: " + cls
	}
	return fmt.Sprintf("  stuck loop:  %s ×%d failures%s — agent has been told to switch tactic",
		s.stuckTool, s.stuckCount, tail)
}

// autonomyCeilingLine renders the cumulative auto-resume ceiling
// proximity. Quiet until the autonomous wrapper has actually fired at
// least once (StepCeiling>0). Includes both step and token windows when
// available, with explicit percent so the user doesn't have to do
// mental math from the badge's compact "240/600 · 920k/2.5M".
func autonomyCeilingLine(s agentLoopState) string {
	if s.stepCeiling <= 0 || s.cumulativeSteps <= 0 {
		return ""
	}
	stepPct := (s.cumulativeSteps * 100) / s.stepCeiling
	tokInfo := ""
	if s.tokenCeiling > 0 && s.cumulativeTokens > 0 {
		tokPct := (s.cumulativeTokens * 100) / s.tokenCeiling
		tokInfo = fmt.Sprintf(" · tokens %s/%s (%d%%)",
			formatThousands(s.cumulativeTokens), formatThousands(s.tokenCeiling), tokPct)
	}
	return fmt.Sprintf("  auto-resume: %d/%d steps (%d%%)%s — engine refuses further resumes when this hits 100%%",
		s.cumulativeSteps, s.stepCeiling, stepPct, tokInfo)
}

// autonomyUnverifiedLine renders the unvalidated-edits ledger. Lists up
// to 3 paths plus a "+N more" tail to match the warn notice. Quiet
// when the ledger is empty.
func autonomyUnverifiedLine(s agentLoopState) string {
	if len(s.unvalidatedEdits) == 0 {
		return ""
	}
	preview := s.unvalidatedEdits
	tail := ""
	if len(preview) > 3 {
		preview = preview[:3]
		tail = fmt.Sprintf(", +%d more", len(s.unvalidatedEdits)-3)
	}
	severity := "edits"
	if len(s.unvalidatedEdits) >= 3 {
		severity = "EDITS — agent has been told to STOP and validate"
	}
	return fmt.Sprintf("  unverified:  %d %s · %s%s",
		len(s.unvalidatedEdits), severity, strings.Join(preview, ", "), tail)
}
