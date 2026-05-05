// agent_loop_events.go — persistence and event-bus publishers for the
// native agent loop:
//
//   - recordNativeAgentInteraction: writes the completed turn into the
//     conversation log and memory store, preserving per-step tool_call /
//     tool_result records so a restart can replay the full trajectory.
//   - publishNativeToolCall / publishNativeToolResultWithPayload: fire the
//     "tool:call" and "tool:result" events the TUI chip strip and web
//     activity feed both subscribe to. The result event carries RTK
//     compression stats (raw vs. post-trim payload) so the UI can surface
//     the savings.
//
// Extracted from agent_loop_native.go to keep the main loop file focused
// on control flow.

package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) recordNativeAgentInteraction(question string, completion nativeToolCompletion) {
	now := time.Now()
	assistantMsg := types.Message{
		Role:      types.RoleAssistant,
		Content:   completion.Answer,
		Timestamp: now,
		TokenCnt:  completion.TokenCount,
		Metadata: map[string]string{
			"provider":    completion.Provider,
			"model":       completion.Model,
			"tool_rounds": fmt.Sprintf("%d", len(completion.ToolTraces)),
			"surface":     "native",
		},
	}
	for _, trace := range completion.ToolTraces {
		callMetadata := map[string]string{
			"provider":     trace.Provider,
			"model":        trace.Model,
			"step":         fmt.Sprintf("%d", trace.Step),
			"tool_call_id": trace.Call.ID,
		}
		resultMetadata := map[string]string{
			"provider":     trace.Provider,
			"model":        trace.Model,
			"step":         fmt.Sprintf("%d", trace.Step),
			"tool_call_id": trace.Call.ID,
		}
		if trace.Err != "" {
			resultMetadata["error"] = trace.Err
		}
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, types.ToolCallRecord{
			Name:      trace.Call.Name,
			Params:    trace.Call.Input,
			Timestamp: trace.OccurredAt,
			Metadata:  callMetadata,
		})
		assistantMsg.Results = append(assistantMsg.Results, types.ToolResultRecord{
			Name:      trace.Call.Name,
			Output:    strings.TrimSpace(trace.Result.Output),
			Success:   trace.Err == "",
			Timestamp: trace.OccurredAt,
			Metadata:  resultMetadata,
		})
	}

	if e.Conversation != nil {
		e.Conversation.AddMessage(completion.Provider, completion.Model, types.Message{
			Role:      types.RoleUser,
			Content:   question,
			Timestamp: now,
		})
		e.Conversation.AddMessage(completion.Provider, completion.Model, assistantMsg)
		// Persist after every completed turn — without this the
		// JSONL is only flushed at engine.Shutdown(), so a panic,
		// SIGKILL, OOM, or power loss between turns silently drops
		// the entire in-memory conversation. The save uses an atomic
		// temp + rename (storage.SaveConversationLog), so the write
		// cost is one disk transaction per turn and any reader either
		// sees the previous full log or the new full log — never a
		// torn intermediate.
		e.saveActiveConversationWithWarning("turn_complete", map[string]any{
			"question": truncateRunesWithMarker(question, 120, "..."),
		})
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, completion.Answer)
		for _, ch := range completion.Context {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, completion.Answer, 0.75)
	}
}

func (e *Engine) publishNativeToolCall(trace nativeToolTrace) {
	if e.EventBus == nil {
		return
	}
	payload := map[string]any{
		"tool":           trace.Call.Name,
		"params":         trace.Call.Input,
		"params_preview": formatToolParamsPreview(trace.Call.Input, 0),
		"step":           trace.Step,
		"provider":       trace.Provider,
		"model":          trace.Model,
		"tool_call_id":   trace.Call.ID,
		"surface":        "native",
	}
	for k, v := range nativeToolEventMetadata(trace.Call.Name, trace.Call.Input, nil) {
		payload[k] = v
	}
	e.EventBus.Publish(Event{
		Type:    "tool:call",
		Source:  "engine",
		Payload: payload,
	})
}

// publishNativeToolResultWithPayload emits a tool:result event enriched
// with RTK compression stats — the exact bytes (and token estimate) that
// go back to the model after the noise filter + char-cap trim. When
// modelPayload is empty (e.g. coming from the legacy publish path), the
// payload-size fields are omitted. The diff between raw output and payload
// is the RTK savings the TUI stats panel can surface.
func (e *Engine) publishNativeToolResultWithPayload(trace nativeToolTrace, modelPayload string) {
	if e.EventBus == nil {
		return
	}
	outputText := trace.Result.Output
	payload := map[string]any{
		"tool":           trace.Call.Name,
		"success":        trace.Err == "",
		"durationMs":     trace.Result.DurationMs,
		"step":           trace.Step,
		"provider":       trace.Provider,
		"model":          trace.Model,
		"truncated":      trace.Result.Truncated,
		"output_preview": compactToolPayload(outputText, 180),
		"output_chars":   len(outputText),
		"output_tokens":  tokens.Estimate(outputText),
		"tool_call_id":   trace.Call.ID,
		"surface":        "native",
	}
	if modelPayload != "" {
		payload["payload_chars"] = len(modelPayload)
		payload["payload_tokens"] = tokens.Estimate(modelPayload)
		if raw := len(outputText); raw > 0 {
			saved := max(raw-len(modelPayload), 0)
			payload["compression_saved_chars"] = saved
			// Ratio kept as float so the UI can render "83%".
			payload["compression_ratio"] = float64(len(modelPayload)) / float64(raw)
		}
	}
	if trace.Err != "" {
		payload["error"] = trace.Err
	}
	if summary := batchFanoutSummary(trace.Call.Name, trace.Result.Data); summary != nil {
		for k, v := range summary {
			payload[k] = v
		}
	}
	for k, v := range nativeToolEventMetadata(trace.Call.Name, trace.Call.Input, trace.Result.Data) {
		payload[k] = v
	}
	e.EventBus.Publish(Event{
		Type:    "tool:result",
		Source:  "engine",
		Payload: payload,
	})
}

func nativeToolEventMetadata(toolName string, params map[string]any, data map[string]any) map[string]any {
	out := map[string]any{}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file":
		if path := firstStringAny(data, params, "path"); path != "" {
			out["read_path"] = path
		}
		if n := firstIntAny(data, params, "line_start"); n > 0 {
			out["read_line_start"] = n
		}
		if n := firstIntAny(data, params, "line_end"); n > 0 {
			out["read_line_end"] = n
		}
		if n := firstIntAny(data, nil, "returned_lines"); n > 0 {
			out["read_returned_lines"] = n
		}
		if n := firstIntAny(data, nil, "total_lines", "line_count"); n > 0 {
			out["read_total_lines"] = n
		}
		if lang := firstStringAny(data, nil, "language"); lang != "" {
			out["read_language"] = lang
		}
	case "edit_file":
		path := firstStringAny(data, params, "path")
		if path != "" {
			out["changed_files"] = []string{path}
		}
		if replacements := firstIntAny(data, nil, "replacements"); replacements > 0 {
			out["replacements"] = replacements
		}
		oldLines := textLineCount(firstStringAny(nil, params, "old_string"))
		newLines := textLineCount(firstStringAny(nil, params, "new_string"))
		if oldLines > 0 || newLines > 0 {
			out["removed_lines"] = oldLines
			out["added_lines"] = newLines
			out["net_lines"] = newLines - oldLines
		}
	case "write_file":
		path := firstStringAny(data, params, "path")
		if path != "" {
			out["changed_files"] = []string{path}
		}
		lines := textLineCount(firstStringAny(nil, params, "content"))
		if lines > 0 {
			out["added_lines"] = lines
			if firstBoolAny(data, params, "overwrite", "overwrote_existing") {
				out["mutation_mode"] = "overwrite"
			} else {
				out["mutation_mode"] = "create"
			}
		}
		if bytes := firstIntAny(data, nil, "bytes"); bytes > 0 {
			out["written_bytes"] = bytes
		}
	case "apply_patch":
		patch := firstStringAny(nil, params, "patch")
		files, added, removed, hunks := summarizeUnifiedDiffPatch(patch)
		if len(files) > 0 {
			out["changed_files"] = files
		}
		if added > 0 {
			out["added_lines"] = added
		}
		if removed > 0 {
			out["removed_lines"] = removed
		}
		if added > 0 || removed > 0 {
			out["net_lines"] = added - removed
		}
		if hunks > 0 {
			out["hunks"] = hunks
		}
		if appliedHunks, rejectedHunks := summarizePatchResultHunks(data); appliedHunks > 0 || rejectedHunks > 0 {
			out["hunks_applied"] = appliedHunks
			out["hunks_rejected"] = rejectedHunks
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstStringAny(primary, secondary map[string]any, keys ...string) string {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			raw, ok := data[key]
			if !ok || raw == nil {
				continue
			}
			text := strings.TrimSpace(fmt.Sprint(raw))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func firstIntAny(primary, secondary map[string]any, keys ...string) int {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			raw, ok := data[key]
			if !ok || raw == nil {
				continue
			}
			switch v := raw.(type) {
			case int:
				return v
			case int32:
				return int(v)
			case int64:
				return int(v)
			case float32:
				return int(v)
			case float64:
				return int(v)
			case string:
				var n int
				if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

func firstBoolAny(primary, secondary map[string]any, keys ...string) bool {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			raw, ok := data[key]
			if !ok || raw == nil {
				continue
			}
			switch v := raw.(type) {
			case bool:
				return v
			case string:
				return strings.EqualFold(strings.TrimSpace(v), "true")
			}
		}
	}
	return false
}

func textLineCount(text string) int {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func summarizeUnifiedDiffPatch(patch string) ([]string, int, int, int) {
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	files := make([]string, 0, 4)
	seen := map[string]struct{}{}
	added, removed, hunks := 0, 0, 0
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			path = strings.TrimPrefix(path, "b/")
			if path != "" && path != "/dev/null" {
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					files = append(files, path)
				}
			}
		case strings.HasPrefix(line, "--- "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "--- "))
			path = strings.TrimPrefix(path, "a/")
			if path != "" && path != "/dev/null" {
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					files = append(files, path)
				}
			}
		case strings.HasPrefix(line, "@@"):
			hunks++
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removed++
		}
	}
	return files, added, removed, hunks
}

func summarizePatchResultHunks(data map[string]any) (int, int) {
	raw, ok := data["files"]
	if !ok || raw == nil {
		return 0, 0
	}
	var applied, rejected int
	switch files := raw.(type) {
	case []map[string]any:
		for _, f := range files {
			applied += firstIntAny(f, nil, "hunks_applied")
			rejected += firstIntAny(f, nil, "hunks_rejected")
		}
	case []any:
		for _, item := range files {
			if f, ok := item.(map[string]any); ok {
				applied += firstIntAny(f, nil, "hunks_applied")
				rejected += firstIntAny(f, nil, "hunks_rejected")
			}
		}
	}
	return applied, rejected
}
