// Provider profile, models.dev cache, AST, and codemap metric one-line
// formatters used by `dfmc status` and `dfmc version`. Companion
// siblings:
//
//   - cli_admin.go         runVersion / runStatus / runInit /
//                          initNextSteps
//   - cli_admin_summary.go approval-gate + hooks-dispatcher
//                          summarisers
//
// These are pure printers — no engine/state mutation, no error paths.
// Each takes the typed status/metric struct from the engine package
// and emits a single human-readable string suitable for `dfmc status`
// or `dfmc version --verbose`.

package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func formatASTLanguageSummary(items []ast.BackendLanguageStatus) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		lang := strings.TrimSpace(item.Language)
		active := strings.TrimSpace(item.Active)
		if lang == "" || active == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", lang, active))
	}
	return strings.Join(parts, ", ")
}

func formatASTMetricsSummary(metrics ast.ParseMetrics) string {
	parts := make([]string, 0, 7)
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
	return strings.Join(parts, " ")
}

func formatMetricMap(items map[string]int64) string {
	if len(items) == 0 {
		return ""
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, items[key]))
	}
	return strings.Join(parts, ",")
}

func blankFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func formatProviderProfileSummary(profile engine.ProviderProfileStatus) string {
	name := strings.TrimSpace(profile.Name)
	model := strings.TrimSpace(profile.Model)
	protocol := strings.TrimSpace(profile.Protocol)
	baseURL := strings.TrimSpace(profile.BaseURL)
	if name == "" && model == "" && protocol == "" && baseURL == "" && profile.MaxContext <= 0 && profile.MaxTokens <= 0 {
		return ""
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
	parts = append(parts, "configured="+strconv.FormatBool(profile.Configured))
	if count := len(profile.Advisories); count > 0 {
		parts = append(parts, fmt.Sprintf("advisories=%d", count))
	}
	return strings.Join(parts, " ")
}

func formatModelsDevCacheSummary(cache engine.ModelsDevCacheStatus) string {
	path := strings.TrimSpace(cache.Path)
	if path == "" {
		return ""
	}
	if !cache.Exists {
		return "missing"
	}
	parts := []string{"ready"}
	if !cache.UpdatedAt.IsZero() {
		parts = append(parts, "updated="+cache.UpdatedAt.Format(time.RFC3339))
	}
	if cache.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("size=%d", cache.SizeBytes))
	}
	return strings.Join(parts, " ")
}

func formatCodeMapMetricsSummary(metrics codemap.BuildMetrics) string {
	parts := make([]string, 0, 12)
	if metrics.Builds > 0 {
		parts = append(parts, fmt.Sprintf("builds=%d", metrics.Builds))
	}
	if metrics.FilesRequested > 0 || metrics.FilesProcessed > 0 {
		parts = append(parts, fmt.Sprintf("files=%d/%d", metrics.FilesProcessed, metrics.FilesRequested))
	}
	if metrics.FilesSkipped > 0 {
		parts = append(parts, fmt.Sprintf("skipped=%d", metrics.FilesSkipped))
	}
	if metrics.ParseErrors > 0 {
		parts = append(parts, fmt.Sprintf("parse_errors=%d", metrics.ParseErrors))
	}
	if metrics.LastDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("last=%dms", metrics.LastDurationMs))
	}
	if metrics.LastGraphNodes > 0 || metrics.LastGraphEdges > 0 {
		parts = append(parts, fmt.Sprintf("graph=%dN/%dE", metrics.LastGraphNodes, metrics.LastGraphEdges))
	}
	if metrics.LastNodesAdded > 0 || metrics.LastEdgesAdded > 0 {
		parts = append(parts, fmt.Sprintf("delta=+%dN/+%dE", metrics.LastNodesAdded, metrics.LastEdgesAdded))
	}
	if metrics.RecentBuilds > 1 {
		parts = append(parts, fmt.Sprintf("trend=%druns", metrics.RecentBuilds))
	}
	if recent := latestBuildSample(metrics.Recent); recent != nil {
		if langs := formatMetricKeySummary(recent.Languages, 3, false); len(langs) > 0 {
			parts = append(parts, "recent_langs="+strings.Join(langs, ","))
		}
		if dirs := formatMetricKeySummary(recent.Directories, 2, true); len(dirs) > 0 {
			parts = append(parts, "recent_dirs="+strings.Join(dirs, ","))
		}
	}
	if langs := formatMetricKeySummary(metrics.RecentLanguages, 3, false); len(langs) > 0 {
		parts = append(parts, "trend_langs="+strings.Join(langs, ","))
	}
	if dirs := formatMetricKeySummary(metrics.RecentDirectories, 2, true); len(dirs) > 0 {
		parts = append(parts, "hot_dirs="+strings.Join(dirs, ","))
	}
	return strings.Join(parts, " ")
}

func latestBuildSample(samples []codemap.BuildSample) *codemap.BuildSample {
	if len(samples) == 0 {
		return nil
	}
	return &samples[len(samples)-1]
}

func formatMetricKeySummary(items map[string]int64, limit int, shortenPaths bool) []string {
	if len(items) == 0 {
		return nil
	}
	type pair struct {
		key   string
		count int64
	}
	pairs := make([]pair, 0, len(items))
	for key, count := range items {
		label := strings.TrimSpace(key)
		if label == "" {
			continue
		}
		if shortenPaths {
			label = shortenMetricPath(label)
		}
		pairs = append(pairs, pair{key: label, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].count > pairs[j].count
	})
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}
	out := make([]string, 0, len(pairs))
	for _, item := range pairs {
		out = append(out, item.key)
	}
	return out
}

func shortenMetricPath(value string) string {
	pathValue := filepath.ToSlash(strings.TrimSpace(value))
	if pathValue == "" || pathValue == "." {
		return "."
	}
	trimmed := strings.Trim(pathValue, "/")
	if trimmed == "" {
		return pathValue
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[len(parts)-2:], "/")
}
