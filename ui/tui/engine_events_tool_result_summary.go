package tui

import (
	"fmt"
	"strings"
)

type toolResultSummary struct {
	ChipPreview        string
	BatchInner         []string
	BatchCount         int
	SavedChars         int
	RawChars           int
	PayloadChars       int
	CompressionPct     int
	HardTruncated      bool
	HardTruncatedRunes int
}

func buildToolResultSummary(toolName, preview string, success bool, payload map[string]any) toolResultSummary {
	s := toolResultSummary{ChipPreview: preview}
	if s.ChipPreview == "" && !success {
		s.ChipPreview = payloadString(payload, "error", "")
	}
	s.BatchCount = payloadInt(payload, "batch_count", 0)
	if s.BatchCount > 0 {
		batchParallel := payloadInt(payload, "batch_parallel", 0)
		batchOK := payloadInt(payload, "batch_ok", 0)
		batchFail := payloadInt(payload, "batch_fail", 0)
		parts := []string{fmt.Sprintf("%d calls", s.BatchCount)}
		if batchParallel > 0 {
			parts = append(parts, fmt.Sprintf("%d parallel", batchParallel))
		}
		parts = append(parts, fmt.Sprintf("%d ok", batchOK))
		if batchFail > 0 {
			parts = append(parts, fmt.Sprintf("%d fail", batchFail))
		}
		s.ChipPreview = strings.Join(parts, " · ")
		s.BatchInner = payloadStringSlice(payload, "batch_inner")
	}
	s.SavedChars = payloadInt(payload, "compression_saved_chars", 0)
	s.RawChars = payloadInt(payload, "output_chars", 0)
	s.PayloadChars = payloadInt(payload, "payload_chars", 0)
	if ratio, ok := payload["compression_ratio"].(float64); ok && ratio >= 0 && ratio <= 1 {
		s.CompressionPct = int((1 - ratio) * 100)
	} else if s.RawChars > 0 && s.SavedChars > 0 {
		s.CompressionPct = int((int64(s.SavedChars) * 100) / int64(s.RawChars))
	}
	s.HardTruncated = payloadBool(payload, "hard_truncated", false)
	s.HardTruncatedRunes = payloadInt(payload, "hard_truncated_output_runes", 0) +
		payloadInt(payload, "hard_truncated_data_runes", 0)
	s.ChipPreview = readFileLineAccountingPrefix(toolName, s.ChipPreview, payload)
	return s
}

func readFileLineAccountingPrefix(toolName, chipPreview string, payload map[string]any) string {
	if !strings.EqualFold(strings.TrimSpace(toolName), "read_file") {
		return chipPreview
	}
	totalLines := payloadInt(payload, "read_total_lines", 0)
	returnedLines := payloadInt(payload, "read_returned_lines", 0)
	if totalLines <= 0 || returnedLines <= 0 || returnedLines >= totalLines {
		return chipPreview
	}
	suffix := fmt.Sprintf("%d/%d lines · %d omitted", returnedLines, totalLines, totalLines-returnedLines)
	if strings.TrimSpace(chipPreview) == "" {
		return suffix
	}
	return suffix + " · " + chipPreview
}

func (m Model) accumulateToolCompressionStats(summary toolResultSummary) Model {
	if summary.SavedChars > 0 && summary.RawChars > 0 {
		m.telemetry.compressionSavedChars += summary.SavedChars
		m.telemetry.compressionRawChars += summary.RawChars
	}
	return m
}
