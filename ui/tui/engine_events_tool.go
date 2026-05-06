package tui

// Tool branch of the engine-event router. Owns the four tool:* cases
// (call/result/error/reasoning). Extracted from engine_events.go so
// the chip-ribbon choreography + batch-fanout preview logic lives in
// one place. engine_events.go dispatches prefix-matched events here.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) handleToolEvent(eventType string, event engine.Event, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
	case "tool:call":
		m.agentLoop.active = true
		m.agentLoop.phase = "tool-call"
		toolName := payloadString(payload, "tool", "tool")
		step := payloadInt(payload, "step", 0)
		m.agentLoop.lastTool = toolName
		m.agentLoop.lastStatus = "running"
		m.agentLoop.lastDuration = 0
		if step > 0 {
			m.agentLoop.step = step
		}
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds > 0 {
			m.agentLoop.toolRounds = rounds
		}
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		paramsPreview := payloadString(payload, "params_preview", "")
		reason := payloadString(payload, "reason", "")
		displayName := displayToolName(toolName, payload)
		callDetail := toolCallChatDetail(payload, step, paramsPreview)
		runningLog := []string(nil)
		if strings.EqualFold(strings.TrimSpace(toolName), "tool_batch_call") {
			runningLog = batchToolCallPreviewLines(payload)
			if len(runningLog) > 0 {
				callDetail = fmt.Sprintf("%d planned calls", len(runningLog))
			}
		}
		// Verb carries the action line (e.g. "Read foo.go (lines N-M)")
		// separately from Preview so the result-side merge can keep both
		// on the finished chip's two-line shape — Preview becomes the
		// result excerpt, Verb stays the params action.
		toolCallChip := toolChip{
			Name:   toolName,
			Status: "running",
			Step:   step,
			Verb:   paramsPreview,
		}
		m.pushToolChip(toolCallChip)
		m.pushStreamingMessageToolChip(toolCallChip)
		m.upsertStreamingChatEvent(chatEventLine{
			Key:           toolChatEventKey(toolName, step),
			Kind:          "tool",
			Status:        "running",
			Title:         toolName,
			Detail:        callDetail,
			ToolName:      displayName,
			ParamsPreview: paramsPreview,
			Reason:        reason,
			Step:          step,
			RunningLog:    runningLog,
			DetailLines:   toolCallTimelineLines(toolName, payload, paramsPreview),
		})
		m.telemetry.activeToolCount++
		if step > 0 {
			line = fmt.Sprintf("Agent tool call: %s (step %d)", toolName, step)
		} else {
			line = fmt.Sprintf("Agent tool call: %s", toolName)
		}
		if paramsPreview != "" {
			line += " " + paramsPreview
		}
	case "tool:result":
		m.agentLoop.active = true
		m.agentLoop.phase = "tool-result"
		toolName := payloadString(payload, "tool", "tool")
		duration := payloadInt(payload, "durationMs", 0)
		success := payloadBool(payload, "success", true)
		status := "ok"
		if !success {
			status = "failed"
		}
		m.agentLoop.lastTool = toolName
		m.agentLoop.lastStatus = status
		m.agentLoop.lastDuration = duration
		preview := payloadString(payload, "output_preview", "")
		if preview != "" {
			m.agentLoop.lastOutput = preview
		}
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoop.step = step
			if step > m.agentLoop.toolRounds {
				m.agentLoop.toolRounds = step
			}
		}
		// Clear the stuck-loop badge once the model recovers — a single
		// successful tool call means the trajectory layer's "switch
		// tactic" hint landed and the agent broke out of the failure
		// loop. The badge re-arms on the next agent:coach:stuck event.
		if success && m.agentLoop.stuckTool != "" {
			m.agentLoop.stuckClearedAt = step
			m.agentLoop.stuckTool = ""
			m.agentLoop.stuckCount = 0
			m.agentLoop.stuckErrClass = ""
		}
		// Mutation/validation tracking. Successful edits accumulate on
		// agentLoop.unvalidatedEdits; a successful build/test/vet run
		// clears the slate. The runtime strip surfaces the count as a
		// "unverified: N edits" badge so a long autonomous run that
		// keeps editing without ever validating becomes visually obvious.
		if success {
			m = m.trackMutationOrValidation(toolName, payload, step)
		}
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		displayName := displayToolName(toolName, payload)
		chipPreview := preview
		if chipPreview == "" && !success {
			chipPreview = payloadString(payload, "error", "")
		}
		var batchInner []string
		batchCount := payloadInt(payload, "batch_count", 0)
		if batchCount > 0 {
			batchParallel := payloadInt(payload, "batch_parallel", 0)
			batchOK := payloadInt(payload, "batch_ok", 0)
			batchFail := payloadInt(payload, "batch_fail", 0)
			parts := []string{fmt.Sprintf("%d calls", batchCount)}
			if batchParallel > 0 {
				parts = append(parts, fmt.Sprintf("%d parallel", batchParallel))
			}
			parts = append(parts, fmt.Sprintf("%d ok", batchOK))
			if batchFail > 0 {
				parts = append(parts, fmt.Sprintf("%d fail", batchFail))
			}
			chipPreview = strings.Join(parts, " · ")
			// Per-call breakdown emitted by batchFanoutSummary so the
			// user sees WHAT each batched call did, not just the count.
			batchInner = payloadStringSlice(payload, "batch_inner")
		}
		savedChars := payloadInt(payload, "compression_saved_chars", 0)
		rawChars := payloadInt(payload, "output_chars", 0)
		payloadChars := payloadInt(payload, "payload_chars", 0)
		compressionPct := 0
		if ratio, ok := payload["compression_ratio"].(float64); ok && ratio >= 0 && ratio <= 1 {
			compressionPct = int((1 - ratio) * 100)
		} else if rawChars > 0 && savedChars > 0 {
			compressionPct = int((int64(savedChars) * 100) / int64(rawChars))
		}
		if savedChars > 0 && rawChars > 0 {
			m.telemetry.compressionSavedChars += savedChars
			m.telemetry.compressionRawChars += rawChars
		}
		finishedChip := toolChip{
			Name:            toolName,
			Status:          status,
			Step:            step,
			DurationMs:      duration,
			Preview:         chipPreview,
			OutputTokens:    payloadInt(payload, "output_tokens", 0),
			Truncated:       payloadBool(payload, "truncated", false),
			CompressedChars: payloadChars,
			SavedChars:      savedChars,
			CompressionPct:  compressionPct,
			InnerLines:      batchInner,
		}
		m.finishToolChip(finishedChip)
		m.finishStreamingMessageToolChip(finishedChip)
		resultDetail := toolResultChatDetail(payload, preview, success, compressionPct)
		if batchCount > 0 {
			resultDetail = batchResultSummaryDetail(payload, resultDetail)
		}
		if strings.EqualFold(strings.TrimSpace(toolName), "tool_batch_call") && batchToolCallNameSummary(payload) == "" {
			displayName = ""
		}
		if isMetaToolName(toolName) && strings.EqualFold(displayName, toolName) {
			displayName = ""
		}
		m.upsertStreamingChatEvent(chatEventLine{
			Key:         toolChatEventKey(toolName, step),
			Kind:        "tool",
			Status:      status,
			Title:       toolName,
			Detail:      resultDetail,
			Duration:    duration,
			ToolName:    displayName,
			Reason:      payloadString(payload, "reason", ""),
			Step:        step,
			RunningLog:  batchInner,
			DetailLines: toolResultTimelineLines(toolName, payload, preview, success, compressionPct),
		})
		if m.telemetry.activeToolCount > 0 {
			m.telemetry.activeToolCount--
		}
		if duration > 0 {
			line = fmt.Sprintf("Agent tool result: %s (%s, %dms)", toolName, status, duration)
		} else {
			line = fmt.Sprintf("Agent tool result: %s (%s)", toolName, status)
		}
		if success && strings.EqualFold(strings.TrimSpace(toolName), "todo_write") {
			m.autoActivateStatsPanelMode(statsPanelModeTodos, "todos")
		}
		if preview != "" {
			line += " -> " + preview
		} else if !success {
			if errText := payloadString(payload, "error", ""); errText != "" {
				line += " -> " + truncateSingleLine(errText, 96)
			}
		}
	case "tool:error":
		switch p := event.Payload.(type) {
		case string:
			line = "Tool error: " + strings.TrimSpace(p)
		default:
			_ = p
			line = "Tool error occurred."
		}
	case "tool:timeout":
		// Distinct line from tool:error so an operator scanning the feed
		// sees the gate firing without having to read tool error text.
		// Both events fire — tool:error carries the model-facing message,
		// tool:timeout carries the structural fact.
		toolName := payloadString(payload, "name", "tool")
		limitMs := payloadInt(payload, "limit_ms", 0)
		if limitMs > 0 {
			line = fmt.Sprintf("Tool timeout: %s exceeded %dms cap.", toolName, limitMs)
		} else {
			line = fmt.Sprintf("Tool timeout: %s.", toolName)
		}
	case "tool:reasoning":
		// Self-narration: backfill the most recent running chip for
		// this tool with the model's `_reason` text. Fires AFTER
		// tool:call (which created the chip) and BEFORE tool:result
		// (which finalises it), so the running chip catches it; if
		// timing is racy and the chip already finished, we still write
		// to it so the finished card shows the why.
		toolName := payloadString(payload, "tool", "")
		reason := payloadString(payload, "reason", "")
		if toolName != "" && reason != "" {
			m.attachReasonToLastChip(toolName, reason)
			m.attachReasonToStreamingChatEvent(toolName, reason)
			line = fmt.Sprintf("%s · %s", toolName, truncateForLine(reason, 90))
		}
	}
	return m, line
}

// trackMutationOrValidation updates the unvalidated-edits ledger from
// a successful tool:result. Edits append (de-duped) to the slice;
// build/test/vet runs clear it. Caller must already have checked
// success=true so we don't have to re-validate here.
//
// `payload` carries `changed_files` (engine populates for edit_file/
// write_file/apply_patch via nativeToolEventMetadata) and `command`
// (populated for run_command). Tools that touch neither are silently
// ignored — the ledger only changes on signal events.
func (m Model) trackMutationOrValidation(toolName string, payload map[string]any, step int) Model {
	switch toolName {
	case "edit_file", "write_file", "apply_patch":
		paths := payloadStringSlice(payload, "changed_files")
		if len(paths) == 0 {
			return m
		}
		if len(m.agentLoop.unvalidatedEdits) == 0 {
			m.agentLoop.unvalidatedSinceStep = step
		}
		seen := make(map[string]struct{}, len(m.agentLoop.unvalidatedEdits))
		for _, p := range m.agentLoop.unvalidatedEdits {
			seen[p] = struct{}{}
		}
		for _, p := range paths {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			m.agentLoop.unvalidatedEdits = append(m.agentLoop.unvalidatedEdits, p)
			seen[p] = struct{}{}
		}
	case "run_command":
		cmd := strings.TrimSpace(payloadString(payload, "command", ""))
		if cmd == "" {
			return m
		}
		if isValidationCommand(cmd) {
			m.agentLoop.unvalidatedEdits = nil
			m.agentLoop.unvalidatedSinceStep = 0
		}
	}
	return m
}

// isValidationCommand recognises shell commands that constitute a
// validation pass — running one of these clears the unvalidated-edits
// ledger because the model has at least attempted to verify its work.
// We match on the leading token so flag variants ("go test ./...",
// "go test -race -count=1 ./internal/engine/...") all count.
//
// The list mirrors coach.answerMentionsValidation in spirit but
// matches command syntax instead of free-form prose. Keep them in
// rough sync — both surfaces ask the same question ("did the agent
// actually validate this?") just at different layers.
func isValidationCommand(cmd string) bool {
	cmd = strings.TrimSpace(strings.ToLower(cmd))
	if cmd == "" {
		return false
	}
	// Validation prefixes — first one or two tokens. Order matters: more
	// specific multi-word prefixes (e.g. "go test") come before the
	// single-word match so "go run" doesn't accidentally count.
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
