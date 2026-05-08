// cli_admin_status.go — runStatus subcommand. Sibling of cli_admin.go
// which keeps the version/init/initNextSteps trio and the
// admin-package doc-comment listing companion siblings.
//
// Splitting status out gives it room to grow: it already aggregates
// engine.Status + provider profile + AST/codemap metrics + tools/skills
// counts + context budget/breakdown/tuning + prompt recommendation +
// approval gate + hooks + denial counter + open circuits + conversation
// memory snapshot, and every new operator-visible field tends to land
// here first. Keeping it next to cli_admin.go (rather than spread across
// every subsystem) means the operator-facing summary stays one Read away.

package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

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

	// Conversation memory snapshot — surfaces the stored-message count
	// alongside the configured ceilings so users can verify their
	// MaxHistoryTokens / MaxHistoryMessages knobs took effect and see
	// how close they are to the trim window.
	storedMsgs := 0
	if active := eng.ConversationActive(); active != nil {
		storedMsgs = len(active.Messages())
	}
	conversationMemory := map[string]any{
		"stored_msgs":          storedMsgs,
		"max_history_tokens":   contextPreview.MaxHistoryTokens,
		"max_history_messages": contextPreview.MaxHistoryMessages,
	}

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
		"open_circuits":              st.OpenCircuits,
		"conversation_memory":        conversationMemory,
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
	workspaceFiles := "explicit/tool"
	if contextPreview.AutoIncludeFiles {
		workspaceFiles = "auto"
	}
	fmt.Printf("context budget: task=%s workspace_files=%s total=%d per_file=%d files=%d reserve=%d available=%d\n",
		contextPreview.Task,
		workspaceFiles,
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
	fmt.Printf("conversation memory: stored=%d msgs · max_tokens=%d max_msgs=%d\n",
		storedMsgs,
		contextPreview.MaxHistoryTokens,
		contextPreview.MaxHistoryMessages,
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
	if len(st.OpenCircuits) > 0 {
		// Surface circuit-breaker open state so operators can see at a
		// glance which providers are currently being skipped. The router
		// silently routes around them; this line is the only operator-
		// visible signal aside from the EventBus (TUI/web).
		fmt.Printf("open circuits: %s (router skipping these until cooldown elapses)\n", strings.Join(st.OpenCircuits, ", "))
	}
	return 0
}
