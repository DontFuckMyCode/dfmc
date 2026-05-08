package tui

// chat_timeline_format_detail.go — "Detail" line builders for chat
// events (the second line under each tool/context chip in the
// timeline). Pure functions over the engine-event payloads.
// Companion siblings:
//
//   - chat_timeline_format.go        tool-name labels, transcript-row
//                                    helpers, event keys, numeric
//                                    formatters
//   - chat_timeline_format_fields.go field sanitisation/truncation +
//                                    payload accessor helpers
//
// toolCallChatDetail / toolResultChatDetail produce the call-time and
// result-time annotation; readChatDetail / mutationChatDetail surface
// the file scope and edit deltas; contextBuiltChatDetail and
// contextLifecycleChatDetail explain what the context manager just
// did (pack vs compact); batchResultSummaryDetail folds a
// tool_batch_call's per-leg counts into a one-liner.

import (
	"fmt"
	"strings"
)

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
