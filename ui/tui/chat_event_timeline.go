package tui

import (
	"fmt"
	"strings"
	"time"
)

func (m *Model) upsertStreamingChatEvent(ev chatEventLine) {
	ev.Key = strings.TrimSpace(ev.Key)
	ev.Kind = strings.TrimSpace(ev.Kind)
	ev.Status = strings.TrimSpace(ev.Status)
	ev.Title = strings.TrimSpace(ev.Title)
	ev.Detail = strings.TrimSpace(ev.Detail)
	if ev.Title == "" {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	if !m.chat.sending {
		return
	}

	// Tool events: update existing line's EventLines instead of appending a new chatLine.
	// This prevents "TOOL TOOL TOOL" spam when a tool goes running→done/failed.
	if strings.EqualFold(ev.Kind, "tool") {
		m.updateToolEventLine(ev)
		return
	}
	line := newChatLine(chatRoleSystem, chatEventTranscriptText(ev))
	line.Timestamp = ev.At
	line.EventLines = []chatEventLine{ev}
	m.chat.transcript = append(m.chat.transcript, line)
	m.chat.scrollback = 0
}

// updateToolEventLine finds the last chatLine with the same tool Key in its EventLines
// and merges the new event into it. If not found, appends a new chatLine.
func (m *Model) updateToolEventLine(ev chatEventLine) {
	// Search backwards through transcript for a line with this tool's Key.
	for i := len(m.chat.transcript) - 1; i >= 0; i-- {
		line := &m.chat.transcript[i]
		if !line.Role.Eq(chatRoleTool) {
			continue
		}
		found := -1
		for j, existing := range line.EventLines {
			if existing.Key == ev.Key {
				found = j
				break
			}
		}
		if found >= 0 {
			// Merge into existing event; preserve start time, update status/detail/duration.
			line.EventLines[found] = mergeChatEventLine(line.EventLines[found], ev)
			line.Content = chatEventTranscriptText(line.EventLines[found])
			line.Timestamp = ev.At
			m.chat.scrollback = 0
			return
		}
	}
	// No existing line found — append new chatLine for this tool.
	line := newChatLine(chatRoleTool, chatEventTranscriptText(ev))
	line.Timestamp = ev.At
	line.EventLines = []chatEventLine{ev}
	m.chat.transcript = append(m.chat.transcript, line)
	m.chat.scrollback = 0
}

func chatEventTranscriptText(ev chatEventLine) string {
	if isBatchToolEvent(ev) {
		return batchChatEventTranscriptText(ev)
	}
	status := chatEventTranscriptStatusLabel(ev.Status)
	name := strings.TrimSpace(ev.ToolName)
	if name == "" {
		name = strings.TrimSpace(ev.Title)
	}
	if name == "" {
		name = "tool"
	}
	head := []string{status + ": " + name}
	if ev.Step > 0 {
		head = append(head, fmt.Sprintf("step %d", ev.Step))
	}
	if ev.Round > 0 {
		head = append(head, fmt.Sprintf("round %d", ev.Round))
	}
	if ev.Duration > 0 {
		head = append(head, fmt.Sprintf("%dms", ev.Duration))
	}

	lines := []string{strings.Join(head, " | ")}
	if state := toolEventStateLine(ev, status); state != "" {
		lines = append(lines, state)
	}
	if params := timelineEventParamsField(ev.ParamsPreview); params != "" {
		lines = append(lines, "params: "+params)
	}
	if reason := strings.TrimSpace(ev.Reason); reason != "" {
		lines = append(lines, "_reason: "+timelineEventFieldLimit(reason, 260))
	}
	lines = append(lines, ev.DetailLines...)
	if detail := strings.TrimSpace(ev.Detail); detail != "" && !toolDetailDuplicatesParams(detail, ev.ParamsPreview) {
		label := "detail"
		if status == "done" || status == "failed" {
			label = "result"
		}
		lines = append(lines, label+": "+timelineEventFieldLimit(detail, 240))
	}
	if len(ev.RunningLog) > 0 && len(lines) < 7 {
		log := strings.TrimSpace(ev.RunningLog[len(ev.RunningLog)-1])
		if log != "" {
			lines = append(lines, "log: "+timelineEventFieldLimit(log, 180))
		}
	}
	return strings.Join(limitToolEventLines(lines, toolEventLineLimit(ev)), "\n")
}

func batchChatEventTranscriptText(ev chatEventLine) string {
	status := chatEventTranscriptStatusLabel(ev.Status)
	name := strings.TrimSpace(ev.ToolName)
	if name == "" {
		name = strings.TrimSpace(ev.Title)
	}
	if name == "" {
		name = "tool_batch_call"
	}
	head := []string{status + ": " + name}
	if ev.Step > 0 {
		head = append(head, fmt.Sprintf("step %d", ev.Step))
	}
	if ev.Duration > 0 {
		head = append(head, fmt.Sprintf("%dms", ev.Duration))
	}

	lines := []string{strings.Join(head, " | ")}
	if reason := strings.TrimSpace(ev.Reason); reason != "" {
		lines = append(lines, "_reason: "+timelineEventFieldLimit(reason, 260))
	}
	if detail := strings.TrimSpace(ev.Detail); detail != "" {
		lines = append(lines, "summary: "+timelineEventFieldLimit(detail, 240))
	}
	if len(ev.RunningLog) > 0 {
		lines = append(lines, "calls:")
		for _, log := range ev.RunningLog {
			if log = timelineEventFieldLimit(log, 220); log != "" {
				lines = append(lines, "  "+log)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func isBatchToolEvent(ev chatEventLine) bool {
	return strings.EqualFold(strings.TrimSpace(ev.ToolName), "tool_batch_call") ||
		strings.EqualFold(strings.TrimSpace(ev.Title), "tool_batch_call")
}

func toolEventLineLimit(ev chatEventLine) int {
	name := strings.ToLower(strings.TrimSpace(ev.ToolName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(ev.Title))
	}
	switch name {
	case "write_file":
		return 13
	case "edit_file", "apply_patch":
		return 13
	default:
		return 8
	}
}

func toolEventStateLine(ev chatEventLine, status string) string {
	name := strings.ToLower(strings.TrimSpace(ev.ToolName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(ev.Title))
	}
	switch status {
	case "running":
		action := toolEventActionVerb(name)
		if action == "" {
			action = "dispatching"
		}
		return "state: " + action + " -> waiting for result"
	case "done":
		if ev.Duration > 0 {
			return fmt.Sprintf("state: completed in %dms", ev.Duration)
		}
		return "state: completed"
	case "failed":
		if ev.Duration > 0 {
			return fmt.Sprintf("state: failed after %dms", ev.Duration)
		}
		return "state: failed"
	case "warn":
		return "state: warning"
	default:
		return ""
	}
}

func toolEventActionVerb(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "list_dir", "glob":
		return "reading"
	case "grep_codebase", "semantic_search", "ast_query":
		return "searching"
	case "run_command":
		return "running command"
	case "write_file":
		return "writing file"
	case "edit_file":
		return "editing file"
	case "apply_patch":
		return "applying patch"
	case "tool_batch_call":
		return "dispatching batch"
	default:
		return ""
	}
}

func displayToolName(toolName string, payload map[string]any) string {
	canonical := strings.TrimSpace(toolName)
	switch strings.ToLower(canonical) {
	case "tool_call":
		if target := metaToolCallTarget(payload); target != "" {
			return target
		}
	case "tool_batch_call":
		if summary := batchToolCallNameSummary(payload); summary != "" {
			return "batch " + summary
		}
		return "batch"
	}
	return canonical
}

func isMetaToolName(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "tool_call", "tool_batch_call":
		return true
	default:
		return false
	}
}

func metaToolCallTarget(payload map[string]any) string {
	params, _ := payload["params"].(map[string]any)
	if params == nil {
		return ""
	}
	name := strings.TrimSpace(fmt.Sprint(params["name"]))
	if name == "" {
		name = strings.TrimSpace(fmt.Sprint(params["tool"]))
	}
	return name
}

func batchToolCallNameSummary(payload map[string]any) string {
	calls := batchToolCallsFromPayload(payload)
	if len(calls) == 0 {
		return ""
	}
	counts := map[string]int{}
	order := make([]string, 0, len(calls))
	for _, raw := range calls {
		call, _ := raw.(map[string]any)
		name := strings.TrimSpace(fmt.Sprint(call["name"]))
		if name == "" {
			name = strings.TrimSpace(fmt.Sprint(call["tool"]))
		}
		if name == "" {
			name = "tool"
		}
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if count := counts[name]; count > 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", name, count))
		} else {
			parts = append(parts, name)
		}
	}
	return fmt.Sprintf("[%d: %s]", len(calls), strings.Join(parts, ", "))
}

func timelineEventField(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, `\r\n`, " ")
	text = strings.ReplaceAll(text, `\n`, " ")
	text = strings.ReplaceAll(text, `\t`, " ")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func timelineEventFieldLimit(text string, width int) string {
	field := timelineEventField(text)
	if field == "" {
		return ""
	}
	if width <= 0 {
		return field
	}
	return truncateSingleLine(field, width)
}

func timelineEventParamsField(params string) string {
	params = strings.TrimSpace(params)
	if params == "" {
		return ""
	}
	field := timelineEventField(params)
	if field == "" {
		return ""
	}
	if shouldHideTimelineParams(params, field) {
		if target := timelineEventMutationTarget(field); target != "" {
			return target
		}
		return ""
	}
	return truncateSingleLine(field, 220)
}

func shouldHideTimelineParams(params, field string) bool {
	lower := strings.ToLower(field)
	heavyMutationPayload := strings.Contains(lower, "old_string=") ||
		strings.Contains(lower, "new_string=") ||
		strings.Contains(lower, "content=") ||
		strings.Contains(lower, "patch=")
	if !heavyMutationPayload {
		return false
	}
	return len([]rune(field)) > 160 ||
		strings.Count(params, `\n`) > 1 ||
		strings.Count(params, "\n") > 1
}

func timelineEventMutationTarget(field string) string {
	if path := timelineEventKVValue(field, "path"); path != "" {
		return "path=" + truncateSingleLine(path, 180)
	}
	if file := timelineEventKVValue(field, "file"); file != "" {
		return "file=" + truncateSingleLine(file, 180)
	}
	if strings.Contains(strings.ToLower(field), "patch=") {
		return "patch payload hidden"
	}
	return ""
}

func timelineEventKVValue(field, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	lower := strings.ToLower(field)
	marker := key + "="
	idx := strings.LastIndex(lower, " "+marker)
	if idx >= 0 {
		idx++
	} else if strings.HasPrefix(lower, marker) {
		idx = 0
	}
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(field[idx+len(marker):])
	if value == "" {
		return ""
	}
	if quote := value[0]; quote == '"' || quote == '\'' {
		value = value[1:]
		if end := strings.IndexRune(value, rune(quote)); end >= 0 {
			value = value[:end]
		}
		return strings.TrimSpace(value)
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		return strings.Trim(fields[0], `"'`)
	}
	return strings.Trim(value, `"'`)
}

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
		if files := changedFilesTimelineLine(payload); files != "" {
			lines = append(lines, "files: "+files)
		}
		if diff := diffTimelineLine(payload); diff != "" {
			lines = append(lines, "diff: "+diff)
		}
		if bytes := payloadInt(payload, "written_bytes", 0); bytes > 0 {
			lines = append(lines, fmt.Sprintf("payload: wrote %d bytes; file content hidden", bytes))
		} else {
			lines = append(lines, "payload: file content hidden")
		}
		lines = append(lines, "review: /diff shows the actual workspace change")
	case "edit_file", "apply_patch":
		lines = append(lines, mutationResultCardLine(toolName, success))
		if files := changedFilesTimelineLine(payload); files != "" {
			lines = append(lines, "files: "+files)
		}
		if diff := diffTimelineLine(payload); diff != "" {
			lines = append(lines, "diff: "+diff)
		}
		if hunks := payloadIntAny(payload, 0, "hunks_applied", "hunks"); hunks > 0 {
			lines = append(lines, fmt.Sprintf("summary: %d hunk%s applied", hunks, pluralSuffix(hunks)))
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
	} else if outcome := toolOutcomeTimelineLine(toolName, payload); outcome != "" {
		lines = append(lines, "outcome: "+outcome)
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
		return 10
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "run_command", "grep_codebase", "semantic_search", "ast_query", "glob", "list_dir":
		return 7
	}
	return 5
}

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

func compactTimelineLines(lines []string, limit int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func firstTimelineString(primary, secondary map[string]any, keys ...string) string {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			if value := payloadString(data, key, ""); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func firstTimelineInt(primary, secondary map[string]any, keys ...string) int {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			if value := payloadInt(data, key, 0); value > 0 {
				return value
			}
		}
	}
	return 0
}

func firstTimelineBool(primary, secondary map[string]any, keys ...string) bool {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			if payloadBool(data, key, false) {
				return true
			}
		}
	}
	return false
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func chatEventTranscriptStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "ok", "done":
		return "done"
	case "failed", "error":
		return "failed"
	case "warn", "throttle":
		return "warn"
	default:
		return "info"
	}
}

func (m *Model) attachReasonToStreamingChatEvent(toolName, reason string) {
	toolName = strings.TrimSpace(toolName)
	reason = strings.TrimSpace(reason)
	if toolName == "" || reason == "" {
		return
	}
	for lineIndex := len(m.chat.transcript) - 1; lineIndex >= 0; lineIndex-- {
		line := &m.chat.transcript[lineIndex]
		for i := len(line.EventLines) - 1; i >= 0; i-- {
			ev := line.EventLines[i]
			if ev.Kind != "tool" || !chatEventToolNameMatches(ev, toolName) {
				continue
			}
			ev.Reason = reason
			line.EventLines[i] = ev
			line.Content = chatEventTranscriptText(ev)
			return
		}
	}
}

func chatEventToolNameMatches(ev chatEventLine, toolName string) bool {
	return strings.EqualFold(strings.TrimSpace(ev.Title), toolName) ||
		strings.EqualFold(strings.TrimSpace(ev.ToolName), toolName)
}

func toolDetailDuplicatesParams(detail, params string) bool {
	detail = strings.TrimSpace(detail)
	params = strings.TrimSpace(params)
	return detail != "" && params != "" && strings.Contains(detail, params)
}

func batchToolCallPreviewLines(payload map[string]any) []string {
	calls := batchToolCallsFromPayload(payload)
	if len(calls) == 0 {
		return nil
	}
	lines := make([]string, 0, len(calls))
	for i, raw := range calls {
		line := batchToolCallPreviewLine(i+1, raw)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func batchToolCallsFromPayload(payload map[string]any) []any {
	if payload == nil {
		return nil
	}
	if calls, ok := payload["calls"].([]any); ok {
		return calls
	}
	params, _ := payload["params"].(map[string]any)
	if params == nil {
		return nil
	}
	switch calls := params["calls"].(type) {
	case []any:
		return calls
	case []map[string]any:
		out := make([]any, 0, len(calls))
		for _, call := range calls {
			out = append(out, call)
		}
		return out
	}
	return nil
}

func batchToolCallPreviewLine(index int, raw any) string {
	call, _ := raw.(map[string]any)
	if call == nil {
		return fmt.Sprintf("%d. tool", index)
	}
	name := strings.TrimSpace(fmt.Sprint(call["name"]))
	if name == "" {
		name = strings.TrimSpace(fmt.Sprint(call["tool"]))
	}
	if name == "" {
		name = "tool"
	}
	args, _ := call["args"].(map[string]any)
	target := batchToolCallTarget(name, args)
	reason := batchToolCallReason(args, call)
	if target == "" {
		if reason != "" {
			return fmt.Sprintf("%d. %s | why: %s", index, name, reason)
		}
		return fmt.Sprintf("%d. %s", index, name)
	}
	line := fmt.Sprintf("%d. %s - %s", index, name, target)
	if reason != "" {
		line += " | why: " + reason
	}
	return line
}

func batchToolCallTarget(name string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "run_command":
		cmd := strings.TrimSpace(fmt.Sprint(args["command"]))
		if rest := batchToolArgsList(args["args"]); rest != "" {
			return "$ " + strings.TrimSpace(cmd+" "+rest)
		}
		if cmd != "" {
			return "$ " + cmd
		}
	case "read_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return ""
		}
		start, hasStart := pickPayloadInt(args["line_start"])
		end, hasEnd := pickPayloadInt(args["line_end"])
		if hasStart && hasEnd && end > 0 {
			return fmt.Sprintf("Read %s (lines %d-%d)", path, start, end)
		}
		return "Read " + path
	case "edit_file":
		return "Edit " + strings.TrimSpace(fmt.Sprint(args["path"]))
	case "write_file":
		return "Write " + strings.TrimSpace(fmt.Sprint(args["path"]))
	case "list_dir":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			path = "."
		}
		return "List " + path
	case "grep_codebase":
		pattern := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pattern != "" {
			return `Search "` + pattern + `"`
		}
	case "glob":
		return "Glob " + strings.TrimSpace(fmt.Sprint(args["pattern"]))
	}
	for _, key := range []string{"path", "pattern", "query", "command", "url"} {
		if value := strings.TrimSpace(fmt.Sprint(args[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func batchToolArgsList(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, " ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func batchToolCallReason(args map[string]any, call map[string]any) string {
	for _, raw := range []any{args["_reason"], call["_reason"], call["reason"]} {
		reason := strings.TrimSpace(fmt.Sprint(raw))
		if reason != "" && reason != "<nil>" {
			return timelineEventField(reason)
		}
	}
	return ""
}

func pickPayloadInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	default:
		return 0, false
	}
}

func batchResultSummaryDetail(payload map[string]any, fallback string) string {
	count := payloadInt(payload, "batch_count", 0)
	if count <= 0 {
		return fallback
	}
	parts := []string{fmt.Sprintf("%d calls", count)}
	if parallel := payloadInt(payload, "batch_parallel", 0); parallel > 0 {
		parts = append(parts, fmt.Sprintf("%d parallel", parallel))
	}
	parts = append(parts, fmt.Sprintf("%d ok", payloadInt(payload, "batch_ok", 0)))
	if fail := payloadInt(payload, "batch_fail", 0); fail > 0 {
		parts = append(parts, fmt.Sprintf("%d fail", fail))
	}
	return strings.Join(parts, " | ")
}

func limitToolEventLines(lines []string, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(lines), maxLines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) == maxLines {
			break
		}
	}
	return out
}

func toolNameKey(toolName string) string {
	return "tool:" + toolName
}

func toolChatEventKey(toolName string, step int) string {
	toolName = strings.TrimSpace(toolName)
	if step > 0 {
		return fmt.Sprintf("tool:%d:%s", step, toolName)
	}
	return toolNameKey(toolName)
}

// elapsedLabel returns a compact elapsed-time string for a running tool.
// Returns "" if the event is not running or the delta is not positive.
func elapsedLabel(startTime time.Time) string {
	if startTime.IsZero() {
		return ""
	}
	elapsed := time.Since(startTime)
	if elapsed < time.Second {
		return ""
	}
	seconds := int(elapsed.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("+%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	return fmt.Sprintf("+%dm%02ds", minutes, seconds)
}

func compactMetric(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fm", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func toolCallChatDetail(payload map[string]any, step int, paramsPreview string) string {
	parts := []string{}
	if step > 0 {
		parts = append(parts, fmt.Sprintf("step %d", step))
	}
	if read := readChatDetail(payload); read != "" {
		parts = append(parts, read)
	}
	if mutation := mutationChatDetail(payload, "will change"); mutation != "" {
		parts = append(parts, mutation)
	}
	if paramsPreview = timelineEventParamsField(paramsPreview); paramsPreview != "" {
		parts = append(parts, paramsPreview)
	}
	if provider := payloadString(payload, "provider", ""); provider != "" {
		model := payloadString(payload, "model", "")
		if model != "" {
			parts = append(parts, provider+"/"+model)
		} else {
			parts = append(parts, provider)
		}
	}
	return strings.Join(parts, " | ")
}

func toolResultChatDetail(payload map[string]any, preview string, success bool, compressionPct int) string {
	reads := payloadInt(payload, "files_read", 0)
	writes := payloadInt(payload, "files_written", 0)
	tokens := payloadInt(payload, "tokens_used", 0)
	parts := []string{}
	if read := readChatDetail(payload); read != "" {
		parts = append(parts, read)
	}
	if mutation := mutationChatDetail(payload, "changed"); mutation != "" {
		parts = append(parts, mutation)
	}
	if reads > 0 {
		if reads == 1 {
			parts = append(parts, "1 file read")
		} else {
			parts = append(parts, fmt.Sprintf("%d files read", reads))
		}
	}
	if writes > 0 {
		if writes == 1 {
			parts = append(parts, "1 file written")
		} else {
			parts = append(parts, fmt.Sprintf("%d files written", writes))
		}
	}
	if tokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", tokens))
	}
	if outputTokens := payloadInt(payload, "output_tokens", 0); outputTokens > 0 {
		parts = append(parts, "out "+compactMetric(outputTokens)+" tok")
	}
	if modelTokens := payloadInt(payload, "payload_tokens", 0); modelTokens > 0 {
		parts = append(parts, "model "+compactMetric(modelTokens)+" tok")
	}
	if savedChars := payloadInt(payload, "compression_saved_chars", 0); savedChars > 0 {
		if compressionPct > 0 {
			parts = append(parts, fmt.Sprintf("rtk saved %s chars (%d%%)", compactMetric(savedChars), compressionPct))
		} else {
			parts = append(parts, "rtk saved "+compactMetric(savedChars)+" chars")
		}
	}
	if compressionPct > 0 {
		parts = append(parts, fmt.Sprintf("%d%% compressed", compressionPct))
	}
	if !success {
		if errText := payloadString(payload, "error", ""); errText != "" {
			parts = append(parts, truncateSingleLine(errText, 120))
		}
	} else if preview = strings.TrimSpace(preview); preview != "" {
		parts = append(parts, truncateSingleLine(preview, 120))
	}
	return strings.Join(parts, " | ")
}

func readChatDetail(payload map[string]any) string {
	if path := payloadString(payload, "read_path", ""); path != "" {
		start := payloadInt(payload, "read_line_start", 0)
		end := payloadInt(payload, "read_line_end", 0)
		returned := payloadInt(payload, "read_returned_lines", 0)
		total := payloadInt(payload, "read_total_lines", 0)
		rangeLabel := path
		if start > 0 && end > 0 {
			rangeLabel = fmt.Sprintf("%s:%d-%d", path, start, end)
		}
		if returned > 0 && total > 0 {
			return fmt.Sprintf("read %s (%d/%d lines)", rangeLabel, returned, total)
		}
		if returned > 0 {
			return fmt.Sprintf("read %s (%d lines)", rangeLabel, returned)
		}
		return "read " + rangeLabel
	}
	files := payloadStringSlice(payload, "files_read")
	if len(files) == 0 {
		if single := payloadString(payload, "file_read", ""); single != "" {
			files = []string{single}
		}
	}
	if len(files) == 0 {
		return ""
	}
	count := len(files)
	if count == 0 {
		return ""
	}
	if count == 1 {
		return files[0] + " read"
	}
	return fmt.Sprintf("%d files read", count)
}

func mutationChatDetail(payload map[string]any, label string) string {
	if files := payloadStringSlice(payload, "changed_files"); len(files) > 0 {
		added := payloadInt(payload, "added_lines", 0)
		removed := payloadInt(payload, "removed_lines", 0)
		target := files[0]
		if len(files) > 1 {
			target = fmt.Sprintf("%d files", len(files))
		}
		detail := strings.TrimSpace(label + " " + target)
		if added > 0 || removed > 0 {
			detail += fmt.Sprintf(" +%d -%d lines", added, removed)
		}
		return detail
	}
	mutations := payloadMap(payload, "mutations")
	if mutations == nil {
		return ""
	}
	files := payloadStringSlice(mutations, "files")
	if len(files) == 0 {
		if single := payloadString(mutations, "file", ""); single != "" {
			files = []string{single}
		}
	}
	if len(files) > 0 {
		count := len(files)
		if count == 1 {
			return fmt.Sprintf("%s: %s", files[0], label)
		}
		return fmt.Sprintf("%d files %s", count, label)
	}
	return ""
}

func contextBuiltChatDetail(files, tokens, budget, perFile int, task, compression string) string {
	parts := []string{}
	if files > 0 {
		parts = append(parts, fmt.Sprintf("%d files", files))
	}
	if tokens > 0 {
		parts = append(parts, compactMetric(tokens)+" tok")
	}
	if tokens > 0 && budget > 0 {
		parts = append(parts, fmt.Sprintf("%d%% budget", (tokens*100)/budget))
	}
	if budget > 0 {
		parts = append(parts, "budget "+compactMetric(budget))
	}
	if perFile > 0 {
		parts = append(parts, "per-file "+compactMetric(perFile))
	}
	if task = strings.TrimSpace(task); task != "" && task != "general" {
		parts = append(parts, task)
	}
	if compression = strings.TrimSpace(compression); compression != "" && compression != "-" {
		parts = append(parts, compression)
	}
	return strings.Join(parts, " | ")
}

func contextLifecycleChatDetail(payload map[string]any) string {
	before := payloadInt(payload, "before_tokens", 0)
	after := payloadInt(payload, "after_tokens", 0)
	rounds := payloadInt(payload, "rounds_collapsed", 0)
	removed := payloadInt(payload, "messages_removed", 0)
	keepRecent := payloadInt(payload, "keep_recent", 0)
	step := payloadInt(payload, "step", 0)
	parts := []string{}
	if before > 0 || after > 0 {
		parts = append(parts, fmt.Sprintf("%s -> %s tok", compactMetric(before), compactMetric(after)))
		if before > after {
			parts = append(parts, "saved "+compactMetric(before-after)+" tok")
		}
	}
	if rounds > 0 {
		parts = append(parts, fmt.Sprintf("%d rounds summarized", rounds))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("%d msgs removed", removed))
	}
	if keepRecent > 0 {
		parts = append(parts, fmt.Sprintf("kept last %d rounds", keepRecent))
	}
	if step > 0 {
		parts = append(parts, fmt.Sprintf("step %d", step))
	}
	return strings.Join(parts, " | ")
}

func payloadMap(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	v, ok := payload[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}
