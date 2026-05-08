package tui

// chat_timeline_builder.go — entry-point payload-driven timeline /
// card-line builders. Each function takes a payload map, optional
// params map, and returns one or more rendered lines for the chip
// strip and chat transcript. The split:
//
//   toolCall*Lines / toolResult*Lines  — multi-line cards for
//                                        running and finished tools.
//
// Single-line card headers per tool kind, plus per-payload helpers
// (impact, changed-files, target, outcome, diff, range, command),
// live in chat_timeline_builder_cards.go. Batch fan-out preview lines
// for tool_batch_call live in chat_timeline_builder_batch.go.
//
// Pure formatters (truncation, status labels, plural suffixes, chat-
// event Detail strings) live in chat_timeline_format.go. Model-receiver
// methods that own transcript mutation live in chat_event_timeline.go.

import (
	"fmt"
	"strings"
)

func toolCallTimelineLines(toolName string, payload map[string]any, paramsPreview string) []string {
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	params := payloadMap(payload, "params")
	lines := []string{}
	if target := toolTimelineTarget(toolName, payload, params); target != "" {
		lines = append(lines, "target: "+target)
	}
	switch toolName {
	case "read_file":
		lines = append(lines, toolCallCardLine(toolName))
		if r := readRangeTimelineLine(payload, params); r != "" {
			lines = append(lines, r)
		}
	case "grep_codebase", "semantic_search", "ast_query", "glob", "list_dir":
		lines = append(lines, toolCallCardLine(toolName))
	case "run_command":
		lines = append(lines, toolCallCardLine(toolName))
		if cmd := commandTimelineLine(params, payload); cmd != "" {
			lines = append(lines, "command: "+cmd)
		}
		if dir := firstTimelineString(payload, params, "dir", "cwd", "working_dir"); dir != "" {
			lines = append(lines, "cwd: "+dir)
		}
	case "write_file":
		lines = append(lines, "card: WRITE | content hidden | diff after result")
		mode := "create"
		if firstTimelineBool(payload, params, "overwrite", "overwrote_existing") {
			mode = "overwrite"
		}
		content := firstTimelineString(nil, params, "content")
		parts := []string{mode, "content hidden"}
		if linesCount := pasteLineCount(content); linesCount > 0 {
			parts = append(parts, fmt.Sprintf("%d lines", linesCount))
		}
		if content != "" {
			parts = append(parts, fmt.Sprintf("%d bytes", len([]byte(content))))
		}
		lines = append(lines, "mode: "+strings.Join(parts, " | "))
	case "edit_file", "apply_patch":
		lines = append(lines, mutationCallCardLine(toolName))
		if diff := diffTimelineLine(payload); diff != "" {
			lines = append(lines, "diff: "+diff)
		}
		if toolName == "apply_patch" {
			lines = append(lines, "review: unified patch hidden here; open /diff or Patch tab for the real hunk view")
		}
	default:
		if params := timelineEventParamsField(paramsPreview); params != "" && len(lines) == 0 {
			lines = append(lines, "target: "+params)
		}
	}
	return compactTimelineLines(lines, 4)
}

func toolResultTimelineLines(toolName string, payload map[string]any, preview string, success bool, compressionPct int) []string {
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	lines := []string{}
	if target := toolTimelineTarget(toolName, payload, payloadMap(payload, "params")); target != "" {
		lines = append(lines, "target: "+target)
	}
	switch toolName {
	case "read_file":
		lines = append(lines, toolResultCardLine(toolName, success))
		if detail := readChatDetail(payload); detail != "" {
			lines = append(lines, "returned: "+detail)
		}
	case "grep_codebase", "semantic_search", "ast_query", "glob", "list_dir":
		lines = append(lines, toolResultCardLine(toolName, success))
		if preview = strings.TrimSpace(preview); preview != "" {
			lines = append(lines, "output: "+timelineEventFieldLimit(preview, 220))
		}
	case "write_file":
		lines = append(lines, mutationResultCardLine(toolName, success))
		if impact := mutationImpactTimelineLine(payload); impact != "" {
			lines = append(lines, "impact: "+impact)
		}
		if files := changedFilesTimelineLine(payload); files != "" {
			lines = append(lines, "files: "+files)
		}
		if diff := diffTimelineLine(payload); diff != "" {
			lines = append(lines, "diff: "+diff)
		}
		if outcome := toolOutcomeTimelineLine(toolName, payload); outcome != "" {
			lines = append(lines, "outcome: "+outcome)
		}
		if bytes := payloadInt(payload, "written_bytes", 0); bytes > 0 {
			lines = append(lines, fmt.Sprintf("payload: wrote %d bytes; file content hidden", bytes))
		} else {
			lines = append(lines, "payload: file content hidden")
		}
		lines = append(lines, "review: /diff shows the actual workspace change")
	case "edit_file", "apply_patch":
		lines = append(lines, mutationResultCardLine(toolName, success))
		if impact := mutationImpactTimelineLine(payload); impact != "" {
			lines = append(lines, "impact: "+impact)
		}
		if files := changedFilesTimelineLine(payload); files != "" {
			lines = append(lines, "files: "+files)
		}
		if diff := diffTimelineLine(payload); diff != "" {
			lines = append(lines, "diff: "+diff)
		}
		if hunks := payloadIntAny(payload, 0, "hunks_applied", "hunks"); hunks > 0 {
			lines = append(lines, fmt.Sprintf("summary: %d hunk%s applied", hunks, pluralSuffix(hunks)))
		}
		if outcome := toolOutcomeTimelineLine(toolName, payload); outcome != "" {
			lines = append(lines, "outcome: "+outcome)
		}
		lines = append(lines, "review: /diff or Patch tab shows side-by-side changes")
	case "run_command":
		lines = append(lines, toolResultCardLine(toolName, success))
		if preview = strings.TrimSpace(preview); preview != "" {
			lines = append(lines, "output: "+timelineEventFieldLimit(preview, 220))
		}
	default:
		if preview = strings.TrimSpace(preview); preview != "" {
			lines = append(lines, "output: "+timelineEventFieldLimit(preview, 220))
		}
	}
	if !success {
		if errText := payloadString(payload, "error", ""); errText != "" {
			lines = append(lines, "error: "+timelineEventFieldLimit(errText, 220))
		}
		lines = append(lines, failureNextActionLine(toolName))
	} else if !isMutationTimelineTool(toolName) {
		if outcome := toolOutcomeTimelineLine(toolName, payload); outcome != "" {
			lines = append(lines, "outcome: "+outcome)
		}
	}
	if success && isMutationTimelineTool(toolName) {
		lines = append(lines, "verify: inspect diff, then run focused tests")
	}
	if saved := payloadInt(payload, "compression_saved_chars", 0); saved > 0 {
		if compressionPct > 0 {
			lines = append(lines, fmt.Sprintf("summary: display compressed by %s chars (%d%%)", compactMetric(saved), compressionPct))
		} else {
			lines = append(lines, "summary: display compressed by "+compactMetric(saved)+" chars")
		}
	}
	return compactTimelineLines(lines, toolResultTimelineLineLimit(toolName))
}

func toolResultTimelineLineLimit(toolName string) int {
	if isMutationTimelineTool(toolName) {
		return 12
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "run_command", "grep_codebase", "semantic_search", "ast_query", "glob", "list_dir":
		return 7
	}
	return 5
}
