package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func formatASTLanguageSummaryTUI(items []ast.BackendLanguageStatus) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Language) == "" || strings.TrimSpace(item.Active) == "" {
			continue
		}
		parts = append(parts, item.Language+"="+item.Active)
	}
	return strings.Join(parts, ", ")
}

func formatASTMetricsSummaryTUI(metrics ast.ParseMetrics) string {
	parts := make([]string, 0, 6)
	if metrics.Requests > 0 {
		parts = append(parts, fmt.Sprintf("requests=%d", metrics.Requests))
	}
	if metrics.Parsed > 0 {
		parts = append(parts, fmt.Sprintf("parsed=%d", metrics.Parsed))
	}
	if metrics.CacheHits > 0 || metrics.CacheMisses > 0 {
		parts = append(parts, fmt.Sprintf("cache=%d/%d", metrics.CacheHits, metrics.CacheMisses))
	}
	if metrics.AvgParseDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("avg=%.1fms", metrics.AvgParseDurationMs))
	}
	if metrics.LastLanguage != "" || metrics.LastBackend != "" {
		parts = append(parts, fmt.Sprintf("last=%s/%s", blankFallback(metrics.LastLanguage, "-"), blankFallback(metrics.LastBackend, "-")))
	}
	if len(metrics.ByBackend) > 0 {
		parts = append(parts, "backend["+formatMetricMap(metrics.ByBackend)+"]")
	}
	if len(parts) == 0 {
		return "no parse activity"
	}
	return strings.Join(parts, " ")
}

func formatCodeMapMetricsSummaryTUI(metrics codemap.BuildMetrics) string {
	parts := make([]string, 0, 8)
	if metrics.Builds > 0 {
		parts = append(parts, fmt.Sprintf("builds=%d", metrics.Builds))
	}
	if metrics.FilesRequested > 0 || metrics.FilesProcessed > 0 {
		parts = append(parts, fmt.Sprintf("files=%d/%d", metrics.FilesProcessed, metrics.FilesRequested))
	}
	if metrics.LastDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("last=%dms", metrics.LastDurationMs))
	}
	if metrics.LastGraphNodes > 0 || metrics.LastGraphEdges > 0 {
		parts = append(parts, fmt.Sprintf("graph=%dN/%dE", metrics.LastGraphNodes, metrics.LastGraphEdges))
	}
	if metrics.RecentBuilds > 1 {
		parts = append(parts, fmt.Sprintf("trend=%druns", metrics.RecentBuilds))
	}
	if len(metrics.RecentLanguages) > 0 {
		parts = append(parts, "langs["+formatMetricMap(metrics.RecentLanguages)+"]")
	}
	if len(parts) == 0 {
		return "no codemap activity"
	}
	return strings.Join(parts, " ")
}

func formatMetricMap(items map[string]int64) string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, items[key]))
	}
	return strings.Join(parts, ",")
}

func formatContextInSummaryTUI(report *engine.ContextInStatus) string {
	if report == nil {
		return ""
	}
	task := blankFallback(strings.TrimSpace(report.Task), "general")
	return fmt.Sprintf(
		"%df/%dtok budget=%d per-file=%d task=%s comp=%s explicit=%d",
		report.FileCount,
		report.TokenCount,
		report.MaxTokensTotal,
		report.MaxTokensPerFile,
		task,
		blankFallback(strings.TrimSpace(report.Compression), "-"),
		report.ExplicitFileMentions,
	)
}

func formatContextInReasonSummaryTUI(report *engine.ContextInStatus) string {
	if report == nil || len(report.Reasons) == 0 {
		return ""
	}
	limit := 3
	parts := make([]string, 0, limit+1)
	for _, reason := range report.Reasons {
		reason = truncateSingleLine(strings.TrimSpace(reason), 96)
		if reason == "" {
			continue
		}
		parts = append(parts, reason)
		if len(parts) >= limit {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(report.Reasons) > len(parts) {
		parts = append(parts, "...more")
	}
	return strings.Join(parts, " | ")
}

func formatContextInTopFilesTUI(report *engine.ContextInStatus, limit int) string {
	if report == nil || len(report.Files) == 0 || limit <= 0 {
		return ""
	}
	if limit > len(report.Files) {
		limit = len(report.Files)
	}
	parts := make([]string, 0, limit)
	for _, file := range report.Files[:limit] {
		label := strings.TrimSpace(file.Path)
		if label == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(score=%.2f tok=%d)", label, file.Score, file.TokenCount))
	}
	return strings.Join(parts, "; ")
}

func formatContextInDetailedFileLinesTUI(report *engine.ContextInStatus, limit int) []string {
	if report == nil || len(report.Files) == 0 || limit <= 0 {
		return nil
	}
	files := append([]engine.ContextInFileStatus(nil), report.Files...)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Score == files[j].Score {
			if files[i].TokenCount == files[j].TokenCount {
				return strings.TrimSpace(files[i].Path) < strings.TrimSpace(files[j].Path)
			}
			return files[i].TokenCount > files[j].TokenCount
		}
		return files[i].Score > files[j].Score
	})
	if limit > len(files) {
		limit = len(files)
	}
	lines := make([]string, 0, limit)
	for _, file := range files[:limit] {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		meta := []string{}
		if file.Score > 0 {
			meta = append(meta, fmt.Sprintf("score=%.2f", file.Score))
		}
		if file.TokenCount > 0 {
			meta = append(meta, fmt.Sprintf("tok=%d", file.TokenCount))
		}
		if file.LineStart > 0 {
			end := max(file.LineEnd, file.LineStart)
			meta = append(meta, fmt.Sprintf("L%d-L%d", file.LineStart, end))
		}
		line := path
		if len(meta) > 0 {
			line += " (" + strings.Join(meta, ", ") + ")"
		}
		if reason := strings.TrimSpace(file.Reason); reason != "" {
			line += " - " + reason
		}
		lines = append(lines, line)
	}
	return lines
}

func formatProviderProfileSummaryTUI(profile engine.ProviderProfileStatus) string {
	name := strings.TrimSpace(profile.Name)
	model := strings.TrimSpace(profile.Model)
	protocol := strings.TrimSpace(profile.Protocol)
	baseURL := strings.TrimSpace(profile.BaseURL)
	if name == "" && model == "" && protocol == "" && baseURL == "" && profile.MaxContext <= 0 && profile.MaxTokens <= 0 {
		return "unavailable"
	}

	parts := make([]string, 0, 7)
	if name != "" || model != "" {
		parts = append(parts, fmt.Sprintf("%s/%s", blankFallback(name, "-"), blankFallback(model, "-")))
	}
	if protocol != "" {
		parts = append(parts, "proto="+protocol)
	}
	if profile.MaxContext > 0 {
		parts = append(parts, fmt.Sprintf("ctx=%d", profile.MaxContext))
	}
	if profile.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", profile.MaxTokens))
	}
	if baseURL != "" {
		parts = append(parts, "endpoint="+baseURL)
	}
	parts = append(parts, "configured="+fmt.Sprintf("%t", profile.Configured))
	if count := len(profile.Advisories); count > 0 {
		parts = append(parts, fmt.Sprintf("advisories=%d", count))
	}
	return strings.Join(parts, " ")
}

func providerConnectivityHintTUI(st engine.Status) string {
	providerName := strings.ToLower(strings.TrimSpace(st.Provider))
	profile := st.ProviderProfile
	if providerName == "offline" {
		return "offline provider active"
	}
	if profile.Configured {
		return "provider credentials detected"
	}
	if providerName == "" {
		return "provider unknown"
	}
	return "provider may fallback offline (missing api_key/base_url); update env and run /reload"
}

func formatModelsDevCacheSummaryTUI(cache engine.ModelsDevCacheStatus) string {
	path := strings.TrimSpace(cache.Path)
	if path == "" {
		return "unavailable"
	}
	if !cache.Exists {
		return "missing"
	}
	parts := []string{"ready"}
	if !cache.UpdatedAt.IsZero() {
		parts = append(parts, "updated="+cache.UpdatedAt.Format("2006-01-02 15:04"))
	}
	if cache.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("size=%d", cache.SizeBytes))
	}
	return strings.Join(parts, " ")
}
