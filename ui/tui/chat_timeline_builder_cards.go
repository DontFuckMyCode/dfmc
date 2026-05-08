package tui

// chat_timeline_builder_cards.go — single-line card headers per tool
// kind plus the per-payload helpers (impact, changed-files, target,
// outcome, diff, range, command) that the tool-call/result builders
// in chat_timeline_builder.go assemble into multi-line cards.
// Companion siblings:
//
//   - chat_timeline_builder.go        toolCallTimelineLines +
//                                     toolResultTimelineLines +
//                                     toolResultTimelineLineLimit
//                                     entry points
//   - chat_timeline_builder_batch.go  batchToolCallPreviewLines fan-
//                                     out preview machinery for
//                                     tool_batch_call payloads

import (
	"fmt"
	"strings"
)

// --- single header card lines per tool kind -----------------------------

func toolCallCardLine(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file":
		return "card: READ | waiting for file slice"
	case "list_dir":
		return "card: LIST | waiting for entries"
	case "glob":
		return "card: GLOB | waiting for paths"
	case "grep_codebase", "semantic_search", "ast_query":
		return "card: SEARCH | waiting for matches"
	case "run_command":
		return "card: RUN | waiting for exit"
	default:
		return ""
	}
}

func toolResultCardLine(toolName string, success bool) string {
	state := "OK"
	if !success {
		state = "FAILED"
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file":
		return "card: READ " + state + " | output summarized"
	case "list_dir":
		return "card: LIST " + state + " | entries summarized"
	case "glob":
		return "card: GLOB " + state + " | paths summarized"
	case "grep_codebase", "semantic_search", "ast_query":
		return "card: SEARCH " + state + " | matches summarized"
	case "run_command":
		return "card: RUN " + state + " | output summarized"
	default:
		return ""
	}
}

func mutationCallCardLine(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write_file":
		return "card: WRITE | content hidden | diff after result"
	case "edit_file":
		return "card: EDIT | strings hidden | diff after result"
	case "apply_patch":
		return "card: PATCH | hunks hidden | diff after result"
	default:
		return ""
	}
}

func mutationResultCardLine(toolName string, success bool) string {
	state := "OK"
	if !success {
		state = "FAILED"
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write_file":
		return "card: WRITE " + state + " | raw content hidden"
	case "edit_file":
		return "card: EDIT " + state + " | raw strings hidden"
	case "apply_patch":
		return "card: PATCH " + state + " | raw hunks hidden"
	default:
		return ""
	}
}

// --- per-payload lines ---------------------------------------------------

func mutationImpactTimelineLine(payload map[string]any) string {
	files := payloadStringSlice(payload, "changed_files")
	added := payloadInt(payload, "added_lines", 0)
	removed := payloadInt(payload, "removed_lines", 0)
	hunks := payloadIntAny(payload, 0, "hunks_applied", "hunks")
	replacements := payloadInt(payload, "replacements", 0)
	parts := []string{}
	switch len(files) {
	case 0:
	case 1:
		parts = append(parts, "1 file")
	default:
		parts = append(parts, fmt.Sprintf("%d files", len(files)))
	}
	if added > 0 || removed > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d lines", added, removed))
	}
	if hunks > 0 {
		parts = append(parts, fmt.Sprintf("%d hunk%s", hunks, pluralSuffix(hunks)))
	}
	if replacements > 0 {
		parts = append(parts, fmt.Sprintf("%d replacement%s", replacements, pluralSuffix(replacements)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

func changedFilesTimelineLine(payload map[string]any) string {
	files := payloadStringSlice(payload, "changed_files")
	if len(files) == 0 {
		return ""
	}
	shown := make([]string, 0, min(len(files), 3))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file != "" {
			shown = append(shown, file)
		}
		if len(shown) == 3 {
			break
		}
	}
	if len(shown) == 0 {
		return ""
	}
	line := strings.Join(shown, ", ")
	if len(files) > len(shown) {
		line += fmt.Sprintf(" (+%d more)", len(files)-len(shown))
	}
	return truncateSingleLine(line, 180)
}

func isMutationTimelineTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write_file", "edit_file", "apply_patch":
		return true
	default:
		return false
	}
}

func toolOutcomeTimelineLine(toolName string, payload map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file":
		return ""
	case "run_command":
		if code := payloadIntAny(payload, -1, "exit_code", "code"); code >= 0 {
			return fmt.Sprintf("exit %d", code)
		}
	case "write_file":
		if bytes := payloadInt(payload, "written_bytes", 0); bytes > 0 {
			return fmt.Sprintf("workspace updated, %d bytes written", bytes)
		}
	case "edit_file", "apply_patch":
		if diff := diffTimelineLine(payload); diff != "" {
			return "changed " + strings.Replace(diff, " | ", " ", 1)
		}
	case "grep_codebase", "semantic_search", "ast_query":
		if n := payloadIntAny(payload, 0, "matches", "result_count", "count"); n > 0 {
			return fmt.Sprintf("%d match%s", n, pluralSuffix(n))
		}
	}
	return ""
}

func failureNextActionLine(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "run_command":
		return "next: inspect command output, fix the cause, then retry"
	case "read_file", "list_dir", "glob":
		return "next: check path/range and retry"
	case "write_file", "edit_file", "apply_patch":
		return "next: open /diff or Patch tab, resolve conflict, then retry"
	default:
		return "next: inspect error details and retry if needed"
	}
}

func toolTimelineTarget(toolName string, payload, params map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "write_file", "edit_file", "apply_patch":
		if files := payloadStringSlice(payload, "changed_files"); len(files) > 0 {
			if len(files) == 1 {
				return files[0]
			}
			return fmt.Sprintf("%d files: %s", len(files), truncateSingleLine(strings.Join(files, ", "), 180))
		}
		if path := firstTimelineString(payload, params, "read_path", "path", "file"); path != "" {
			return path
		}
	case "grep_codebase":
		if pattern := firstTimelineString(payload, params, "pattern"); pattern != "" {
			return `pattern "` + truncateSingleLine(pattern, 120) + `"`
		}
	case "glob":
		if pattern := firstTimelineString(payload, params, "pattern"); pattern != "" {
			return pattern
		}
	case "list_dir":
		if path := firstTimelineString(payload, params, "path", "dir"); path != "" {
			return path
		}
		return "."
	case "run_command":
		return firstTimelineString(payload, params, "dir", "cwd", "working_dir")
	}
	for _, key := range []string{"path", "file", "query", "url", "pattern"} {
		if value := firstTimelineString(payload, params, key); value != "" {
			return truncateSingleLine(value, 180)
		}
	}
	return ""
}

func readRangeTimelineLine(payload, params map[string]any) string {
	start := firstTimelineInt(payload, params, "read_line_start", "line_start")
	end := firstTimelineInt(payload, params, "read_line_end", "line_end")
	if start > 0 && end > 0 {
		return fmt.Sprintf("range: lines %d-%d", start, end)
	}
	return ""
}

func commandTimelineLine(params, payload map[string]any) string {
	cmd := firstTimelineString(payload, params, "command")
	if args := firstTimelineString(payload, params, "args"); args != "" && args != "<nil>" {
		cmd = strings.TrimSpace(cmd + " " + args)
	}
	return truncateSingleLine(cmd, 220)
}

func diffTimelineLine(payload map[string]any) string {
	files := payloadStringSlice(payload, "changed_files")
	added := payloadInt(payload, "added_lines", 0)
	removed := payloadInt(payload, "removed_lines", 0)
	hunks := payloadIntAny(payload, 0, "hunks_applied", "hunks")
	parts := []string{}
	if len(files) == 1 {
		parts = append(parts, files[0])
	} else if len(files) > 1 {
		parts = append(parts, fmt.Sprintf("%d files", len(files)))
	}
	if hunks > 0 {
		parts = append(parts, fmt.Sprintf("%d hunk%s", hunks, pluralSuffix(hunks)))
	}
	if added > 0 || removed > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d lines", added, removed))
	}
	if replacements := payloadInt(payload, "replacements", 0); replacements > 0 {
		parts = append(parts, fmt.Sprintf("%d replacement%s", replacements, pluralSuffix(replacements)))
	}
	if mode := payloadString(payload, "mutation_mode", ""); mode != "" {
		parts = append(parts, mode)
	}
	return strings.Join(parts, " | ")
}
