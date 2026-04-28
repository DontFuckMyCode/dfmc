// Administrative CLI subcommands: version, status, init, completion,
// man pages, and doctor. Extracted from cli.go so the dispatcher stays
// focused. The config subcommands moved out to cli_config.go, but these
// commands still share the formatting helpers for engine status,
// provider profiles, and AST/codemap metrics.

package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func runVersion(eng *engine.Engine, version string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := fs.Bool("json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || *jsonFlag

	st := eng.Status()
	loadedProviders := []string{}
	if eng.Providers != nil {
		loadedProviders = eng.Providers.List()
		sort.Strings(loadedProviders)
	}
	payload := map[string]any{
		"name":             "dfmc",
		"version":          version,
		"provider":         st.Provider,
		"model":            st.Model,
		"project_root":     st.ProjectRoot,
		"state":            st.State,
		"go_version":       runtimeVersion(),
		"loaded_providers": loadedProviders,
		"binary_size":      executableSize(),
	}
	if jsonMode {
		mustPrintJSON(payload)
		return 0
	}
	fmt.Printf("dfmc %s\n", version)
	fmt.Printf("provider: %s\n", st.Provider)
	fmt.Printf("model: %s\n", st.Model)
	fmt.Printf("project: %s\n", st.ProjectRoot)
	fmt.Printf("providers: %s\n", strings.Join(loadedProviders, ", "))
	if sz := executableSize(); sz > 0 {
		fmt.Printf("binary size: %d bytes\n", sz)
	}
	return 0
}

func runStatus(eng *engine.Engine, version string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := fs.Bool("json", false, "output as json")
	query := fs.String("query", "", "optional query for context/prompt status snapshot")
	runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for context/prompt snapshot")
	runtimeModel := fs.String("runtime-model", "", "runtime model override for context/prompt snapshot")
	runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
	runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for context/prompt snapshot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || *jsonFlag

	st := eng.Status()
	projectRoot := strings.TrimSpace(st.ProjectRoot)
	if projectRoot == "" {
		projectRoot = config.FindProjectRoot("")
	}

	loadedProviders := []string{}
	if eng.Providers != nil {
		loadedProviders = eng.Providers.List()
		sort.Strings(loadedProviders)
	}
	tools := eng.ListTools()
	sort.Strings(tools)
	skills := discoverSkills(projectRoot)
	templates := promptlib.New()
	_ = templates.LoadOverrides(projectRoot)

	q := strings.TrimSpace(*query)
	runtimeHints := eng.PromptRuntime()
	if p := strings.TrimSpace(*runtimeProvider); p != "" {
		runtimeHints.Provider = p
	}
	if m := strings.TrimSpace(*runtimeModel); m != "" {
		runtimeHints.Model = m
	}
	if ts := strings.TrimSpace(*runtimeToolStyle); ts != "" {
		runtimeHints.ToolStyle = ts
	}
	if *runtimeMaxContext > 0 {
		runtimeHints.MaxContext = *runtimeMaxContext
	}
	contextPreview := eng.ContextBudgetPreviewWithRuntime(q, runtimeHints)
	contextBreakdown := eng.ContextBreakdown(q)
	promptRecommendation := eng.PromptRecommendationWithRuntime(q, runtimeHints)
	contextTuning := eng.ContextTuningSuggestionsWithRuntime(q, runtimeHints)

	gateSummary := summarizeApprovalGate(eng)
	hooksSummary := summarizeHooks(eng)
	denialCount := len(eng.RecentDenials())

	payload := map[string]any{
		"name":                       "dfmc",
		"version":                    version,
		"state":                      st.State,
		"ready":                      st.State == engine.StateReady || st.State == engine.StateServing,
		"provider":                   st.Provider,
		"model":                      st.Model,
		"provider_profile":           st.ProviderProfile,
		"models_dev_cache":           st.ModelsDevCache,
		"ast_backend":                st.ASTBackend,
		"ast_reason":                 st.ASTReason,
		"ast_languages":              st.ASTLanguages,
		"ast_metrics":                st.ASTMetrics,
		"codemap_metrics":            st.CodeMap,
		"project_root":               projectRoot,
		"go_version":                 runtimeVersion(),
		"loaded_providers":           loadedProviders,
		"tools_count":                len(tools),
		"skills_count":               len(skills),
		"prompt_templates_count":     len(templates.List()),
		"query":                      q,
		"context_budget":             contextPreview,
		"context_breakdown":          contextBreakdown,
		"context_tuning_suggestions": contextTuning,
		"prompt_recommendation":      promptRecommendation,
		"approval_gate":              gateSummary,
		"hooks":                      hooksSummary,
		"recent_denials":             denialCount,
	}

	if jsonMode {
		mustPrintJSON(payload)
		return 0
	}

	fmt.Printf("dfmc %s\n", version)
	fmt.Printf("state: %v (ready=%t)\n", st.State, st.State == engine.StateReady || st.State == engine.StateServing)
	fmt.Printf("provider/model: %s / %s\n", st.Provider, st.Model)
	if summary := formatProviderProfileSummary(st.ProviderProfile); summary != "" {
		fmt.Printf("provider profile: %s\n", summary)
	}
	for _, advisory := range st.ProviderProfile.Advisories {
		if msg := strings.TrimSpace(advisory); msg != "" {
			fmt.Printf("provider advisory: %s\n", msg)
		}
	}
	if summary := formatModelsDevCacheSummary(st.ModelsDevCache); summary != "" {
		fmt.Printf("models.dev cache: %s\n", summary)
	}
	if strings.TrimSpace(st.ASTBackend) != "" {
		fmt.Printf("ast backend: %s\n", st.ASTBackend)
	}
	if summary := formatASTLanguageSummary(st.ASTLanguages); summary != "" {
		fmt.Printf("ast languages: %s\n", summary)
	}
	if summary := formatASTMetricsSummary(st.ASTMetrics); summary != "" {
		fmt.Printf("ast metrics: %s\n", summary)
	}
	if summary := formatCodeMapMetricsSummary(st.CodeMap); summary != "" {
		fmt.Printf("codemap: %s\n", summary)
	}
	fmt.Printf("project: %s\n", projectRoot)
	fmt.Printf("providers: %d loaded\n", len(loadedProviders))
	fmt.Printf("tools: %d, skills: %d, prompt templates: %d\n", len(tools), len(skills), len(templates.List()))
	fmt.Printf("context budget: task=%s total=%d per_file=%d files=%d reserve=%d available=%d\n",
		contextPreview.Task,
		contextPreview.MaxTokensTotal,
		contextPreview.MaxTokensPerFile,
		contextPreview.MaxFiles,
		contextPreview.ReserveTotalTokens,
		contextPreview.ContextAvailableTokens,
	)
	if contextBreakdown.MaxContext > 0 {
		pct := float64(contextBreakdown.UsedTotal) / float64(contextBreakdown.MaxContext) * 100
		fmt.Printf("context breakdown: %s/%s %d%% used · system=%dK hist=%dK files=%dK response=%dK tool=%dK available=%dK\n",
			contextBreakdown.Provider, contextBreakdown.Model,
			int(pct),
			contextBreakdown.SystemPrompt/1000,
			contextBreakdown.History/1000,
			contextBreakdown.ContextChunks/1000,
			contextBreakdown.Response/1000,
			contextBreakdown.ToolReserve/1000,
			contextBreakdown.Available/1000,
		)
		if len(contextBreakdown.FilesInContext) > 0 {
			fmt.Printf("  context files: %d\n", len(contextBreakdown.FilesInContext))
		}
	}
	fmt.Printf("prompt recommendation: profile=%s role=%s budget=%d tool_style=%s\n",
		promptRecommendation.Profile,
		promptRecommendation.Role,
		promptRecommendation.PromptBudgetTokens,
		promptRecommendation.ToolStyle,
	)
	if promptRecommendation.CacheableTokens+promptRecommendation.DynamicTokens > 0 {
		fmt.Printf("prompt cache: %d%% stable (cacheable=%d, dynamic=%d tokens)\n",
			promptRecommendation.CacheablePercent,
			promptRecommendation.CacheableTokens,
			promptRecommendation.DynamicTokens,
		)
	}
	if len(contextTuning) > 0 {
		fmt.Printf("context tuning suggestions: %d\n", len(contextTuning))
	}
	if summary := formatApprovalGateSummary(gateSummary); summary != "" {
		fmt.Printf("approval gate: %s\n", summary)
	}
	if summary := formatHooksSummary(hooksSummary); summary != "" {
		fmt.Printf("hooks: %s\n", summary)
	}
	if denialCount > 0 {
		fmt.Printf("recent denials: %d\n", denialCount)
	}
	return 0
}

// approvalGateSummary is the JSON-serialized shape returned by
// summarizeApprovalGate. Active reflects whether the gate would stop
// anything today (non-empty list + registered approver). Tools is the
// raw configured list so operators can confirm exactly what is gated.
type approvalGateSummary struct {
	Active   bool     `json:"active"`
	Wildcard bool     `json:"wildcard"`
	Count    int      `json:"count"`
	Tools    []string `json:"tools,omitempty"`
}

func summarizeApprovalGate(eng *engine.Engine) approvalGateSummary {
	out := approvalGateSummary{}
	if eng == nil || eng.Config == nil {
		return out
	}
	raw := eng.Config.Tools.RequireApproval
	tools := make([]string, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			out.Wildcard = true
			continue
		}
		tools = append(tools, entry)
	}
	sort.Strings(tools)
	out.Tools = tools
	out.Count = len(tools)
	if out.Wildcard {
		out.Count = -1 // sentinel: every tool gated
	}
	out.Active = out.Wildcard || len(tools) > 0
	return out
}

func formatApprovalGateSummary(g approvalGateSummary) string {
	if !g.Active {
		return "off"
	}
	if g.Wildcard {
		return "on (*)"
	}
	if len(g.Tools) == 0 {
		return "on"
	}
	preview := g.Tools
	if len(preview) > 4 {
		preview = preview[:4]
		return fmt.Sprintf("on (%d: %s, …)", len(g.Tools), strings.Join(preview, ", "))
	}
	return fmt.Sprintf("on (%s)", strings.Join(preview, ", "))
}

// hooksSummary serializes the dispatcher inventory into a shape that is
// cheap to render in both JSON and human output. PerEvent maps event
// name → count so readers can see which lifecycle phases have hooks.
type hooksSummary struct {
	Total    int            `json:"total"`
	PerEvent map[string]int `json:"per_event,omitempty"`
}

func summarizeHooks(eng *engine.Engine) hooksSummary {
	out := hooksSummary{PerEvent: map[string]int{}}
	if eng == nil || eng.Hooks == nil {
		return out
	}
	inv := eng.Hooks.Inventory()
	for event, entries := range inv {
		key := strings.TrimSpace(string(event))
		if key == "" {
			continue
		}
		out.PerEvent[key] = len(entries)
		out.Total += len(entries)
	}
	return out
}

func formatHooksSummary(h hooksSummary) string {
	if h.Total == 0 {
		return "none registered"
	}
	events := make([]string, 0, len(h.PerEvent))
	for k := range h.PerEvent {
		events = append(events, k)
	}
	sort.Strings(events)
	parts := make([]string, 0, len(events))
	for _, e := range events {
		parts = append(parts, fmt.Sprintf("%s=%d", e, h.PerEvent[e]))
	}
	return fmt.Sprintf("%d (%s)", h.Total, strings.Join(parts, ", "))
}

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

func runInit(jsonMode bool, projectOverride string) int {
	root := projectOverride
	if strings.TrimSpace(root) == "" {
		root = config.FindProjectRoot("")
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot resolve cwd: %v\n", err)
			return 1
		}
		root = cwd
	}

	dfmcDir := filepath.Join(root, ".dfmc")
	cfgPath := filepath.Join(dfmcDir, "config.yaml")

	if err := os.MkdirAll(dfmcDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		return 1
	}

	cfg := config.DefaultConfig()
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write default config: %v\n", err)
		return 1
	}

	// Prepare local knowledge placeholders.
	_ = os.WriteFile(filepath.Join(dfmcDir, "knowledge.json"), []byte("{}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dfmcDir, "conventions.json"), []byte("{}\n"), 0o644)

	if jsonMode {
		_ = printJSON(map[string]any{
			"status":       "ok",
			"project_root": root,
			"config_path":  cfgPath,
		})
		return 0
	}

	fmt.Printf("Initialized DFMC project at %s\n", root)
	fmt.Printf("Created %s\n", cfgPath)
	return 0
}

