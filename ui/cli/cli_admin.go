// Administrative CLI subcommands: version, status, init, completion,
// man pages, and doctor. Extracted from cli.go so the dispatcher stays
// focused. The config subcommands moved out to cli_config.go, but these
// commands still share the formatting helpers for engine status,
// provider profiles, and AST/codemap metrics.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
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
func runCompletion(args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	shell := fs.String("shell", "", "bash|zsh|fish|powershell")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*shell) == "" && len(fs.Args()) > 0 {
		*shell = fs.Args()[0]
	}
	sh := strings.ToLower(strings.TrimSpace(*shell))
	if sh == "" {
		fmt.Fprintln(os.Stderr, "usage: dfmc completion [--shell bash|zsh|fish|powershell]")
		return 2
	}

	commands := commandNames()
	if jsonMode {
		_ = printJSON(map[string]any{
			"shell":    sh,
			"commands": commands,
		})
		return 0
	}

	switch sh {
	case "bash":
		fmt.Print(completionBash(commands))
	case "zsh":
		fmt.Print(completionZsh(commands))
	case "fish":
		fmt.Print(completionFish(commands))
	case "powershell", "pwsh":
		fmt.Print(completionPowerShell(commands))
	default:
		fmt.Fprintf(os.Stderr, "unsupported shell: %s\n", sh)
		return 2
	}
	return 0
}

type commandDoc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func commandDocs() []commandDoc {
	return []commandDoc{
		{Name: "status", Description: "Runtime status snapshot"},
		{Name: "init", Description: "Initialize DFMC in project"},
		{Name: "chat", Description: "Interactive chat session"},
		{Name: "tui", Description: "Terminal workbench (chat/status/patch)"},
		{Name: "ask", Description: "One-shot question"},
		{Name: "analyze", Description: "Analyze codebase"},
		{Name: "scan", Description: "Quick security scan"},
		{Name: "map", Description: "Generate/display codemap"},
		{Name: "tool", Description: "Tool engine (list/run)"},
		{Name: "conversation", Description: "Conversation management (list/search/load/save/undo/branch)"},
		{Name: "memory", Description: "Memory management"},
		{Name: "config", Description: "Configuration management"},
		{Name: "context", Description: "Context budget and recent files"},
		{Name: "prompt", Description: "Prompt library management"},
		{Name: "magicdoc", Description: "Build/show concise project magic doc"},
		{Name: "plugin", Description: "Plugin management"},
		{Name: "skill", Description: "Skill management"},
		{Name: "serve", Description: "Start Web API server"},
		{Name: "remote", Description: "Remote control server"},
		{Name: "doctor", Description: "Environment and config health checks"},
		{Name: "completion", Description: "Generate shell completion scripts"},
		{Name: "man", Description: "Generate command manual page"},
		{Name: "review", Description: "Code review shortcut"},
		{Name: "explain", Description: "Explain code shortcut"},
		{Name: "refactor", Description: "Refactor code shortcut"},
		{Name: "test", Description: "Test generation shortcut"},
		{Name: "doc", Description: "Documentation shortcut"},
		{Name: "version", Description: "Version info"},
		{Name: "help", Description: "Show help"},
	}
}

func runMan(args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("man", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	format := fs.String("format", "man", "man|markdown")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	docs := commandDocs()
	if jsonMode {
		_ = printJSON(map[string]any{
			"format":   strings.ToLower(strings.TrimSpace(*format)),
			"commands": docs,
		})
		return 0
	}

	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "markdown", "md":
		fmt.Print(renderManMarkdown(docs))
	case "man", "roff":
		fmt.Print(renderManRoff(docs))
	default:
		fmt.Fprintf(os.Stderr, "unsupported man format: %s\n", *format)
		return 2
	}
	return 0
}

func renderManMarkdown(docs []commandDoc) string {
	var b strings.Builder
	b.WriteString("# dfmc(1)\n\n")
	b.WriteString("Don't Fuck My Code command line interface.\n\n")
	b.WriteString("## Usage\n\n")
	b.WriteString("`dfmc [global flags] <command> [args]`\n\n")
	b.WriteString("## Commands\n\n")
	for _, d := range docs {
		fmt.Fprintf(&b, "- `%s`: %s\n", d.Name, d.Description)
	}
	b.WriteString("\n## Global Flags\n\n")
	b.WriteString("- `--provider`: LLM provider override\n")
	b.WriteString("- `--model`: model override\n")
	b.WriteString("- `--profile`: config profile\n")
	b.WriteString("- `--verbose`: verbose output\n")
	b.WriteString("- `--json`: JSON output mode\n")
	b.WriteString("- `--no-color`: disable colors\n")
	b.WriteString("- `--project`: project root path\n")
	return b.String()
}

func renderManRoff(docs []commandDoc) string {
	var b strings.Builder
	b.WriteString(".TH DFMC 1 \"DFMC\" \"dfmc\"\n")
	b.WriteString(".SH NAME\n")
	b.WriteString("dfmc \\- Don't Fuck My Code CLI\n")
	b.WriteString(".SH SYNOPSIS\n")
	b.WriteString(".B dfmc\n")
	b.WriteString("[global flags] <command> [args]\n")
	b.WriteString(".SH COMMANDS\n")
	for _, d := range docs {
		fmt.Fprintf(&b, ".TP\n.B %s\n%s\n", d.Name, d.Description)
	}
	b.WriteString(".SH GLOBAL FLAGS\n")
	b.WriteString(".TP\n.B --provider\nLLM provider override\n")
	b.WriteString(".TP\n.B --model\nModel override\n")
	b.WriteString(".TP\n.B --profile\nConfig profile\n")
	b.WriteString(".TP\n.B --verbose\nVerbose output\n")
	b.WriteString(".TP\n.B --json\nJSON output mode\n")
	b.WriteString(".TP\n.B --no-color\nDisable colors\n")
	b.WriteString(".TP\n.B --project\nProject root path\n")
	return b.String()
}

func commandNames() []string {
	return []string{
		"status",
		"init",
		"chat",
		"tui",
		"ask",
		"analyze",
		"scan",
		"map",
		"tool",
		"conversation",
		"memory",
		"config",
		"context",
		"prompt",
		"magicdoc",
		"plugin",
		"skill",
		"serve",
		"remote",
		"doctor",
		"completion",
		"man",
		"review",
		"explain",
		"refactor",
		"test",
		"doc",
		"version",
		"help",
	}
}

func completionBash(commands []string) string {
	cmds := strings.Join(commands, " ")
	return fmt.Sprintf(`# bash completion for dfmc
_dfmc_completion() {
  local cur
  cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=( $(compgen -W "%s" -- "$cur") )
  return 0
}
complete -F _dfmc_completion dfmc
`, cmds)
}

func completionZsh(commands []string) string {
	cmds := strings.Join(commands, " ")
	return fmt.Sprintf(`#compdef dfmc
_dfmc_completion() {
  local -a commands
  commands=(%s)
  _describe 'command' commands
}
compdef _dfmc_completion dfmc
`, cmds)
}

func completionFish(commands []string) string {
	var b strings.Builder
	b.WriteString("# fish completion for dfmc\n")
	b.WriteString("complete -c dfmc -f\n")
	for _, cmd := range commands {
		fmt.Fprintf(&b, "complete -c dfmc -n '__fish_use_subcommand' -a %s\n", cmd)
	}
	return b.String()
}

func completionPowerShell(commands []string) string {
	cmds := strings.Join(commands, "', '")
	return fmt.Sprintf(`# PowerShell completion for dfmc
Register-ArgumentCompleter -Native -CommandName dfmc -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $commands = @('%s')
  $commands | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`, cmds)
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // pass|warn|fail
	Details string `json:"details"`
}

func runDoctor(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	network := fs.Bool("network", false, "check provider endpoint network reachability")
	timeout := fs.Duration("timeout", 2*time.Second, "network check timeout")
	providersOnly := fs.Bool("providers-only", false, "only run provider checks")
	fix := fs.Bool("fix", false, "attempt safe auto-fixes for config")
	globalFix := fs.Bool("global", false, "with --fix, update ~/.dfmc/config.yaml instead of project config")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	checks := make([]doctorCheck, 0, 16)
	add := func(name, status, details string) {
		checks = append(checks, doctorCheck{
			Name:    name,
			Status:  status,
			Details: details,
		})
	}

	if *fix {
		details, err := applyDoctorFixes(eng, *globalFix)
		if err != nil {
			add("doctor.fix", "warn", err.Error())
		} else {
			add("doctor.fix", "pass", details)
		}
	}

	if eng.Config == nil {
		add("config.loaded", "fail", "engine config is nil")
	} else {
		if *providersOnly {
			if len(eng.Config.Providers.Profiles) == 0 {
				add("config.providers", "fail", "providers.profiles is empty")
			} else {
				add("config.providers", "pass", "provider profiles are present")
			}
		} else if err := eng.Config.Validate(); err != nil {
			add("config.valid", "fail", err.Error())
		} else {
			add("config.valid", "pass", "configuration is valid")
		}
	}

	if !*providersOnly {
		statusSnapshot := eng.Status()
		// Memory-tier degradation is a silent-killer class of issue —
		// the app still starts, conversations still run, but recall
		// won't find anything because the bbolt-backed episodic/
		// semantic tiers never loaded. Surface here so a user running
		// `dfmc doctor` after a weird startup sees it immediately.
		if statusSnapshot.MemoryDegraded {
			details := "episodic/semantic memory unavailable"
			if reason := strings.TrimSpace(statusSnapshot.MemoryLoadErr); reason != "" {
				details += ": " + reason
			}
			add("memory.tier", "warn", details)
		} else {
			add("memory.tier", "pass", "episodic/semantic tiers loaded")
		}
		if strings.TrimSpace(statusSnapshot.ASTBackend) == "" {
			add("ast.backend", "warn", "ast engine backend is unavailable")
		} else {
			status := "pass"
			details := statusSnapshot.ASTBackend
			if reason := strings.TrimSpace(statusSnapshot.ASTReason); reason != "" {
				details += ": " + reason
			}
			if statusSnapshot.ASTBackend == "regex" {
				status = "warn"
			}
			add("ast.backend", status, details)
		}
		for _, lang := range statusSnapshot.ASTLanguages {
			name := strings.TrimSpace(lang.Language)
			if name == "" {
				continue
			}
			checkStatus := "pass"
			active := strings.TrimSpace(lang.Active)
			if active == "" {
				checkStatus = "warn"
				active = "unavailable"
			}
			details := active
			if reason := strings.TrimSpace(lang.Reason); reason != "" {
				details += ": " + reason
			}
			if active == "regex" {
				checkStatus = "warn"
			}
			add("ast."+name, checkStatus, details)
		}
		metricsDetails := formatASTMetricsSummary(statusSnapshot.ASTMetrics)
		if strings.TrimSpace(metricsDetails) == "" {
			metricsDetails = "no parse activity recorded yet"
		}
		add("ast.metrics", "pass", metricsDetails)
		codemapDetails := formatCodeMapMetricsSummary(statusSnapshot.CodeMap)
		if strings.TrimSpace(codemapDetails) == "" {
			codemapDetails = "no codemap build activity recorded yet"
		}
		add("codemap.metrics", "pass", codemapDetails)
		root := strings.TrimSpace(statusSnapshot.ProjectRoot)
		if root == "" {
			add("project.root", "warn", "project root is empty")
		} else if st, err := os.Stat(root); err != nil {
			add("project.root", "fail", err.Error())
		} else if !st.IsDir() {
			add("project.root", "fail", "project root is not a directory")
		} else {
			add("project.root", "pass", root)
		}
		addMagicDocHealthCheck(&checks, root, 24*time.Hour)
		addPromptHealthCheck(&checks, root, 450)

		if eng.Config != nil {
			addFileSystemHealthCheck(&checks, "storage.data_dir", eng.Config.DataDir())
			addFileSystemHealthCheck(&checks, "plugins.dir", eng.Config.PluginDir())
		}

		for _, bin := range []string{"git", "go"} {
			if path, err := exec.LookPath(bin); err != nil {
				add("dependency."+bin, "warn", "not found in PATH")
			} else {
				add("dependency."+bin, "pass", path)
			}
		}
	}

	if eng.Config != nil {
		cache := eng.Status().ModelsDevCache
		if !cache.Exists {
			add("modelsdev.cache", "warn", "missing: "+cache.Path)
		} else {
			details := cache.Path
			if !cache.UpdatedAt.IsZero() {
				details += " updated " + cache.UpdatedAt.Format(time.RFC3339)
			}
			if cache.SizeBytes > 0 {
				details += fmt.Sprintf(" size=%d", cache.SizeBytes)
			}
			add("modelsdev.cache", "pass", details)
		}

		names := make([]string, 0, len(eng.Config.Providers.Profiles))
		for name := range eng.Config.Providers.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			prof := eng.Config.Providers.Profiles[name]
			profileStatus := "pass"
			if strings.TrimSpace(prof.Model) == "" || strings.TrimSpace(prof.Protocol) == "" || prof.MaxContext <= 0 || prof.MaxTokens <= 0 {
				profileStatus = "warn"
			}
			add("provider."+name+".profile", profileStatus, formatProviderProfileSummary(engine.ProviderProfileStatus{
				Name:       name,
				Model:      prof.Model,
				Protocol:   prof.Protocol,
				BaseURL:    prof.BaseURL,
				MaxTokens:  prof.MaxTokens,
				MaxContext: prof.MaxContext,
				Configured: providerConfigured(name, prof),
			}))
			configured := providerConfigured(name, prof)
			if configured {
				add("provider."+name+".configured", "pass", "credentials/endpoint present")
			} else {
				add("provider."+name+".configured", "warn", "missing api_key or required endpoint")
			}
			for _, advisory := range config.ProviderProfileAdvisories(name, prof) {
				add("provider."+name+".advisory", "warn", advisory)
			}
			if *network && configured {
				status, details := providerReachabilityStatus(name, prof, *timeout)
				add("provider."+name+".network", status, details)
			}
		}
	}

	failN, warnN, passN := 0, 0, 0
	for _, c := range checks {
		switch c.Status {
		case "fail":
			failN++
		case "warn":
			warnN++
		default:
			passN++
		}
	}
	exitCode := 0
	overall := "ok"
	if failN > 0 {
		exitCode = 1
		overall = "fail"
	} else if warnN > 0 {
		overall = "warn"
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"status":   overall,
			"summary":  map[string]int{"pass": passN, "warn": warnN, "fail": failN},
			"checks":   checks,
			"network":  *network,
			"timeout":  timeout.String(),
			"fix":      *fix,
			"scope":    map[bool]string{true: "providers", false: "full"}[*providersOnly],
			"provider": eng.Status().Provider,
		})
		return exitCode
	}

	fmt.Println("DFMC doctor report")
	for _, c := range checks {
		fmt.Printf("[%s] %s: %s\n", strings.ToUpper(c.Status), c.Name, c.Details)
	}
	fmt.Printf("Summary: pass=%d warn=%d fail=%d\n", passN, warnN, failN)
	return exitCode
}

func applyDoctorFixes(eng *engine.Engine, global bool) (string, error) {
	if eng == nil {
		return "", fmt.Errorf("engine is nil")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	targetPath := projectConfigPath(cwd)
	if global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		return "", err
	}
	if len(currentMap) == 0 {
		defMap, err := configToMap(config.DefaultConfig())
		if err != nil {
			return "", err
		}
		currentMap = defMap
	}

	if _, ok := getConfigPath(currentMap, "version"); !ok {
		if err := setConfigPath(currentMap, "version", config.DefaultVersion); err != nil {
			return "", err
		}
	}
	if _, ok := getConfigPath(currentMap, "providers.profiles"); !ok {
		if err := setConfigPath(currentMap, "providers.profiles", config.DefaultConfig().Providers.Profiles); err != nil {
			return "", err
		}
	}

	profiles := map[string]any{}
	if raw, ok := getConfigPath(currentMap, "providers.profiles"); ok {
		switch v := raw.(type) {
		case map[string]any:
			profiles = v
		case map[any]any:
			for k, val := range v {
				key := strings.TrimSpace(fmt.Sprint(k))
				if key != "" {
					profiles[key] = val
				}
			}
		}
	}
	if len(profiles) == 0 {
		defMap, err := configToMap(config.DefaultConfig())
		if err != nil {
			return "", err
		}
		if err := setConfigPath(currentMap, "providers.profiles", defMap["providers"].(map[string]any)["profiles"]); err != nil {
			return "", err
		}
		if raw, ok := getConfigPath(currentMap, "providers.profiles"); ok {
			if v, ok := raw.(map[string]any); ok {
				profiles = v
			}
		}
	}

	rawPrimary, _ := getConfigPath(currentMap, "providers.primary")
	primary := strings.TrimSpace(fmt.Sprint(rawPrimary))
	if primary == "" || !profilesHasKey(profiles, primary) {
		primary = choosePreferredProvider(profiles, config.DefaultConfig().Providers.Primary)
		if primary == "" {
			primary = config.DefaultConfig().Providers.Primary
		}
		if err := setConfigPath(currentMap, "providers.primary", primary); err != nil {
			return "", err
		}
	}

	if raw, ok := getConfigPath(currentMap, "web.auth"); ok {
		auth := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		if auth != "none" && auth != "token" {
			if err := setConfigPath(currentMap, "web.auth", "none"); err != nil {
				return "", err
			}
		}
	}
	if raw, ok := getConfigPath(currentMap, "remote.auth"); ok {
		auth := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		if auth != "none" && auth != "token" && auth != "mtls" {
			if err := setConfigPath(currentMap, "remote.auth", "token"); err != nil {
				return "", err
			}
		}
	}
	if raw, ok := getConfigPath(currentMap, "providers.profiles.zai"); ok {
		if profileMap, ok := raw.(map[string]any); ok {
			modelCfg := modelConfigFromAny(profileMap)
			if advisories := config.ProviderProfileAdvisories("zai", modelCfg); len(advisories) > 0 {
				profileMap["protocol"] = "openai-compatible"
				profileMap["base_url"] = "https://api.z.ai/api/paas/v4"
				if err := setConfigPath(currentMap, "providers.profiles.zai", profileMap); err != nil {
					return "", err
				}
			}
		}
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		return "", err
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		return "", fmt.Errorf("fix applied but reload failed (reverted): %w", err)
	}

	return "updated " + targetPath, nil
}

func profilesHasKey(profiles map[string]any, name string) bool {
	for k := range profiles {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func choosePreferredProvider(profiles map[string]any, fallback string) string {
	preferredOrder := []string{
		"anthropic",
		"openai",
		"deepseek",
		"google",
		"zai",
		"generic",
		"alibaba",
		"kimi",
		"minimax",
	}
	for _, name := range preferredOrder {
		prof, ok := profileByName(profiles, name)
		if !ok {
			continue
		}
		modelCfg := modelConfigFromAny(prof)
		if providerConfigured(name, modelCfg) {
			return name
		}
	}
	for _, name := range preferredOrder {
		if profilesHasKey(profiles, name) {
			return name
		}
	}
	if profilesHasKey(profiles, fallback) {
		return fallback
	}
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, strings.TrimSpace(k))
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func profileByName(profiles map[string]any, name string) (any, bool) {
	for k, v := range profiles {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return v, true
		}
	}
	return nil, false
}

func modelConfigFromAny(v any) config.ModelConfig {
	out := config.ModelConfig{}
	switch m := v.(type) {
	case map[string]any:
		if raw, ok := m["api_key"]; ok {
			out.APIKey = strings.TrimSpace(fmt.Sprint(raw))
		}
		if raw, ok := m["base_url"]; ok {
			out.BaseURL = strings.TrimSpace(fmt.Sprint(raw))
		}
		if raw, ok := m["model"]; ok {
			out.Model = strings.TrimSpace(fmt.Sprint(raw))
		}
	case config.ModelConfig:
		out = m
	}
	return out
}

func addFileSystemHealthCheck(checks *[]doctorCheck, name, dir string) {
	if strings.TrimSpace(dir) == "" {
		*checks = append(*checks, doctorCheck{Name: name, Status: "fail", Details: "path is empty"})
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		*checks = append(*checks, doctorCheck{Name: name, Status: "fail", Details: err.Error()})
		return
	}
	probe, err := os.CreateTemp(dir, ".dfmc-health-*")
	if err != nil {
		*checks = append(*checks, doctorCheck{Name: name, Status: "fail", Details: "not writable: " + err.Error()})
		return
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	*checks = append(*checks, doctorCheck{Name: name, Status: "pass", Details: dir})
}

func addMagicDocHealthCheck(checks *[]doctorCheck, projectRoot string, staleAfter time.Duration) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		*checks = append(*checks, doctorCheck{
			Name:    "magicdoc.health",
			Status:  "warn",
			Details: "project root is empty (cannot evaluate magic doc)",
		})
		return
	}
	if staleAfter <= 0 {
		staleAfter = 24 * time.Hour
	}
	path := resolveMagicDocPath(root, "")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			*checks = append(*checks, doctorCheck{
				Name:    "magicdoc.health",
				Status:  "warn",
				Details: fmt.Sprintf("missing: %s (run: dfmc magicdoc update)", path),
			})
			return
		}
		*checks = append(*checks, doctorCheck{
			Name:    "magicdoc.health",
			Status:  "warn",
			Details: fmt.Sprintf("cannot read %s: %v", path, err),
		})
		return
	}
	age := time.Since(info.ModTime())
	if age > staleAfter {
		*checks = append(*checks, doctorCheck{
			Name:    "magicdoc.health",
			Status:  "warn",
			Details: fmt.Sprintf("stale (%s): %s (run: dfmc magicdoc update)", age.Round(time.Minute), path),
		})
		return
	}
	*checks = append(*checks, doctorCheck{
		Name:    "magicdoc.health",
		Status:  "pass",
		Details: fmt.Sprintf("fresh (%s): %s", age.Round(time.Minute), path),
	})
}

func addPromptHealthCheck(checks *[]doctorCheck, projectRoot string, maxTemplateTokens int) {
	lib := promptlib.New()
	_ = lib.LoadOverrides(strings.TrimSpace(projectRoot))
	report := promptlib.BuildStatsReport(lib.List(), promptlib.StatsOptions{
		MaxTemplateTokens: maxTemplateTokens,
	})
	if report.TemplateCount == 0 {
		*checks = append(*checks, doctorCheck{
			Name:    "prompt.health",
			Status:  "warn",
			Details: "no prompt templates loaded",
		})
		return
	}
	if report.WarningCount > 0 {
		first := ""
		for _, t := range report.Templates {
			if len(t.Warnings) == 0 {
				continue
			}
			first = t.ID + ": " + t.Warnings[0]
			break
		}
		msg := fmt.Sprintf("warnings=%d templates=%d threshold=%d", report.WarningCount, report.TemplateCount, report.MaxTemplateTokens)
		if strings.TrimSpace(first) != "" {
			msg += " first=" + first
		}
		*checks = append(*checks, doctorCheck{
			Name:    "prompt.health",
			Status:  "warn",
			Details: msg,
		})
		return
	}
	*checks = append(*checks, doctorCheck{
		Name:    "prompt.health",
		Status:  "pass",
		Details: fmt.Sprintf("templates=%d total_tokens=%d max_tokens=%d", report.TemplateCount, report.TotalTokens, report.MaxTokens),
	})
}

func providerConfigured(name string, prof config.ModelConfig) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	apiKey := strings.TrimSpace(prof.APIKey)
	baseURL := strings.TrimSpace(prof.BaseURL)

	switch name {
	case "generic":
		return baseURL != ""
	default:
		return apiKey != "" || baseURL != ""
	}
}

func providerReachabilityStatus(name string, prof config.ModelConfig, timeout time.Duration) (string, string) {
	target, err := providerEndpoint(name, prof)
	if err != nil {
		return "warn", err.Error()
	}
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return "warn", fmt.Sprintf("dial %s failed: %v", target, err)
	}
	_ = conn.Close()
	return "pass", "reachable: " + target
}

func providerEndpoint(name string, prof config.ModelConfig) (string, error) {
	if strings.TrimSpace(prof.BaseURL) != "" {
		u, err := url.Parse(strings.TrimSpace(prof.BaseURL))
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		if strings.TrimSpace(u.Host) == "" {
			return "", fmt.Errorf("invalid base_url host")
		}
		if strings.Contains(u.Host, ":") {
			return u.Host, nil
		}
		if strings.EqualFold(u.Scheme, "http") {
			return net.JoinHostPort(u.Host, "80"), nil
		}
		return net.JoinHostPort(u.Host, "443"), nil
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return "api.anthropic.com:443", nil
	case "openai":
		return "api.openai.com:443", nil
	case "google":
		return "generativelanguage.googleapis.com:443", nil
	case "deepseek":
		return "api.deepseek.com:443", nil
	case "kimi":
		return "api.moonshot.cn:443", nil
	case "minimax":
		return "api.minimax.chat:443", nil
	case "zai":
		return "api.z.ai:443", nil
	case "alibaba":
		return "dashscope.aliyuncs.com:443", nil
	default:
		return "", fmt.Errorf("no endpoint mapping for provider %q", name)
	}
}

