package engine

// agent_loop_events_metadata.go — per-tool metadata extractors that
// enrich tool:call / tool:result event payloads with structured fields
// the TUI strip / web activity feed / mutation-impact ribbon need.
// Each tool gets a dedicated case in nativeToolEventMetadata that
// pulls out paths, line counts, mutation modes, etc. so downstream
// surfaces don't have to re-parse params or data. The firstStringAny
// / firstIntAny / firstBoolAny helpers walk both data and params with
// loose JSON-shape coercion so the same extractor works whether the
// field came back from the tool or was passed in by the caller.
//
// Sibling to agent_loop_events.go which owns the event publishers
// themselves (recordNativeAgentInteraction, publishNativeToolCall,
// publishNativeToolResultWithPayload + variants).

import (
	"fmt"
	"strings"
)

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
	case "run_command":
		// Surface the command string so downstream surfaces (TUI
		// validation tracker, web activity feed) can recognise build/
		// test/vet runs without scraping params_preview. We keep it
		// short — full args list lives in params_preview already.
		if cmd := firstStringAny(nil, params, "command"); cmd != "" {
			out["command"] = cmd
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
	for line := range strings.SplitSeq(patch, "\n") {
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
