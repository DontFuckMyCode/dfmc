// Prompt library CLI: `dfmc prompt [list|render|inspect|stats|recommend]`
// surfaces the prompt registry for inspection and rendering. Extracted
// from cli_analysis.go so the analysis file stays focused on analyze/
// map/tool flows.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func runPrompt(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"list"}
	}

	lib := promptlib.New()
	projectRoot := eng.Status().ProjectRoot
	_ = lib.LoadOverrides(projectRoot)

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		items := lib.List()
		if jsonMode {
			mustPrintJSON(map[string]any{"prompts": items})
			return 0
		}
		for _, item := range items {
			fmt.Printf("- %s type=%s task=%s", item.ID, item.Type, item.Task)
			if strings.TrimSpace(item.Language) != "" {
				fmt.Printf(" lang=%s", item.Language)
			}
			if item.Priority != 0 {
				fmt.Printf(" priority=%d", item.Priority)
			}
			fmt.Println()
		}
		return 0

	case "render":
		fs := flag.NewFlagSet("prompt render", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		typ := fs.String("type", "system", "prompt type")
		task := fs.String("task", "auto", "task (auto|general|planning|review|security|refactor|test|doc|debug)")
		language := fs.String("language", "auto", "language (auto|go|typescript|python|rust|...)")
		profile := fs.String("profile", "auto", "prompt profile (auto|compact|deep)")
		role := fs.String("role", "auto", "prompt role (auto|generalist|planner|security_auditor|code_reviewer|debugger|refactorer|test_engineer|documenter|architect)")
		query := fs.String("query", "", "user request/query")
		contextFiles := fs.String("context-files", "(none)", "context file summary to inject")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for tool policy rendering")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for tool policy rendering")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for tool policy rendering")
		var varsRaw multiStringFlag
		fs.Var(&varsRaw, "var", "template variable in key=value format (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
			*query = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		resolvedTask := strings.TrimSpace(*task)
		if strings.EqualFold(resolvedTask, "auto") || resolvedTask == "" {
			resolvedTask = promptlib.DetectTask(*query)
		}
		resolvedLanguage := strings.TrimSpace(*language)
		if strings.EqualFold(resolvedLanguage, "auto") || resolvedLanguage == "" {
			resolvedLanguage = promptlib.InferLanguage(*query, nil)
		}

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

		resolvedProfile := strings.TrimSpace(*profile)
		if strings.EqualFold(resolvedProfile, "auto") || resolvedProfile == "" {
			resolvedProfile = ctxmgr.ResolvePromptProfile(*query, resolvedTask, runtimeHints)
		}
		resolvedRole := strings.TrimSpace(*role)
		if strings.EqualFold(resolvedRole, "auto") || resolvedRole == "" {
			resolvedRole = ctxmgr.ResolvePromptRole(*query, resolvedTask)
		}
		budget := ctxmgr.ResolvePromptRenderBudget(resolvedTask, resolvedProfile, runtimeHints)

		vars := map[string]string{
			"project_root":     projectRoot,
			"task":             resolvedTask,
			"language":         resolvedLanguage,
			"profile":          resolvedProfile,
			"role":             resolvedRole,
			"project_brief":    loadPromptProjectBrief(projectRoot, budget.ProjectBriefTokens),
			"user_query":       strings.TrimSpace(*query),
			"context_files":   strings.TrimSpace(*contextFiles),
			"injected_context": ctxmgr.BuildInjectedContextWithBudget(projectRoot, *query, budget),
			"tools_overview":   strings.Join(eng.ListTools(), ", "),
			"tool_call_policy": ctxmgr.BuildToolCallPolicy(resolvedTask, runtimeHints),
			"response_policy":  ctxmgr.BuildResponsePolicy(resolvedTask, resolvedProfile),
		}
		extraVars, err := parsePromptVars(varsRaw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prompt render var parse error: %v\n", err)
			return 2
		}
		for k, v := range extraVars {
			vars[k] = v
		}

		out := lib.Render(promptlib.RenderRequest{
			Type:     strings.TrimSpace(*typ),
			Task:     resolvedTask,
			Language: resolvedLanguage,
			Profile:  resolvedProfile,
			Role:     resolvedRole,
			Vars:     vars,
		})

		promptBudgetTokens := ctxmgr.PromptTokenBudget(resolvedTask, resolvedProfile, runtimeHints)
		trimmed := false
		if promptBudgetTokens > 0 {
			before := promptlib.EstimateTokens(out)
			out = strings.TrimSpace(ctxmgr.TrimPromptToBudget(out, promptBudgetTokens))
			after := promptlib.EstimateTokens(out)
			trimmed = after < before
		}
		promptTokensEstimate := promptlib.EstimateTokens(out)
		if jsonMode {
			_ = printJSON(map[string]any{
				"type":                   strings.TrimSpace(*typ),
				"task":                   resolvedTask,
				"language":               resolvedLanguage,
				"profile":                resolvedProfile,
				"role":                   resolvedRole,
				"vars":                   vars,
				"prompt":                 out,
				"prompt_tokens_estimate": promptTokensEstimate,
				"prompt_budget_tokens":   promptBudgetTokens,
				"prompt_trimmed":         trimmed,
			})
			return 0
		}
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		return 0

	case "inspect", "show":
		fs := flag.NewFlagSet("prompt inspect", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		full := fs.Bool("full", false, "print each section's full body instead of a preview")
		queryFlag := fs.String("query", "", "user query to seed task/profile/role detection")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		query := strings.TrimSpace(*queryFlag)
		if query == "" && len(fs.Args()) > 0 {
			query = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		runtime := eng.PromptRuntime()
		bundle := eng.Context.BuildSystemPromptBundle(projectRoot, query, nil, eng.ListTools(), runtime)
		task := promptlib.DetectTask(query)
		profile := ctxmgr.ResolvePromptProfile(query, task, runtime)
		role := ctxmgr.ResolvePromptRole(query, task)
		language := promptlib.InferLanguage(query, nil)
		totalTokens := 0
		cacheableTokens := 0
		type sectionOut struct {
			Index     int    `json:"index"`
			Label     string `json:"label"`
			Cacheable bool   `json:"cacheable"`
			Tokens    int    `json:"tokens"`
			Chars     int    `json:"chars"`
			Lines     int    `json:"lines"`
			Preview   string `json:"preview,omitempty"`
			Text      string `json:"text,omitempty"`
		}
		sections := make([]sectionOut, 0, len(bundle.Sections))
		for i, s := range bundle.Sections {
			tokens := promptlib.EstimateTokens(s.Text)
			totalTokens += tokens
			if s.Cacheable {
				cacheableTokens += tokens
			}
			firstLine := strings.TrimSpace(s.Text)
			if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
				firstLine = firstLine[:nl]
			}
			if len(firstLine) > 80 {
				firstLine = firstLine[:77] + "..."
			}
			so := sectionOut{
				Index:     i + 1,
				Label:     s.Label,
				Cacheable: s.Cacheable,
				Tokens:    tokens,
				Chars:     len(s.Text),
				Lines:     strings.Count(s.Text, "\n") + 1,
				Preview:   firstLine,
			}
			if *full {
				so.Text = s.Text
			}
			sections = append(sections, so)
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"task":             task,
				"language":         language,
				"profile":          profile,
				"role":             role,
				"section_count":    len(sections),
				"total_tokens":     totalTokens,
				"cacheable_tokens": cacheableTokens,
				"sections":         sections,
			})
			return 0
		}
		fmt.Printf("prompt inspect: task=%s language=%s profile=%s role=%s sections=%d tokens=%d cacheable=%d\n",
			task, language, profile, role, len(sections), totalTokens, cacheableTokens)
		for _, s := range sections {
			cache := "·"
			if s.Cacheable {
				cache = "▣"
			}
			fmt.Printf("  %s %2d  %-12s  tok=%-4d lines=%-3d  %s\n", cache, s.Index, s.Label, s.Tokens, s.Lines, s.Preview)
			if *full {
				fmt.Println("    ───")
				for line := range strings.SplitSeq(strings.TrimRight(s.Text, "\n"), "\n") {
					fmt.Println("    " + line)
				}
				fmt.Println("    ───")
			}
		}
		return 0

	case "stats", "validate", "lint":
		fs := flag.NewFlagSet("prompt stats", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		maxTemplateTokens := fs.Int("max-template-tokens", 450, "warning threshold for per-template token estimate")
		failOnWarning := fs.Bool("fail-on-warning", false, "exit with non-zero status if warnings are found")
		var allowVar multiStringFlag
		fs.Var(&allowVar, "allow-var", "extra placeholder variable to allow (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		report := promptlib.BuildStatsReport(lib.List(), promptlib.StatsOptions{
			MaxTemplateTokens: *maxTemplateTokens,
			AllowVars:         allowVar,
		})
		if jsonMode {
			mustPrintJSON(report)
		} else {
			fmt.Printf(
				"prompt stats: templates=%d total_tokens=%d avg_tokens=%.1f max_tokens=%d warnings=%d threshold=%d\n",
				report.TemplateCount,
				report.TotalTokens,
				report.AvgTokens,
				report.MaxTokens,
				report.WarningCount,
				report.MaxTemplateTokens,
			)
			if report.WarningCount == 0 {
				fmt.Println("prompt stats: no warnings")
			} else {
				fmt.Println("warnings:")
				for _, item := range report.Templates {
					if len(item.Warnings) == 0 {
						continue
					}
					fmt.Printf("- %s: %s\n", item.ID, strings.Join(item.Warnings, "; "))
				}
			}
		}
		if *failOnWarning && report.WarningCount > 0 {
			return 1
		}
		return 0

	case "recommend", "recommendation", "tune":
		fs := flag.NewFlagSet("prompt recommend", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		query := fs.String("query", "", "query for prompt profile/budget recommendation")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for recommendation simulation")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for recommendation simulation")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for recommendation simulation")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
			*query = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
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
		info := eng.PromptRecommendationWithRuntime(*query, runtimeHints)
		if jsonMode {
			_ = printJSON(map[string]any{
				"query":          strings.TrimSpace(*query),
				"recommendation": info,
			})
			return 0
		}
		fmt.Printf(
			"prompt recommend: task=%s language=%s profile=%s role=%s provider=%s model=%s tool_style=%s max_context=%d budget=%d render[files=%d tools=%d injected_blocks=%d injected_lines=%d injected_tokens=%d brief=%d]\n",
			info.Task,
			info.Language,
			info.Profile,
			info.Role,
			info.Provider,
			info.Model,
			info.ToolStyle,
			info.MaxContext,
			info.PromptBudgetTokens,
			info.ContextFiles,
			info.ToolList,
			info.InjectedBlocks,
			info.InjectedLines,
			info.InjectedTokens,
			info.ProjectBriefTokens,
		)
		for _, hint := range info.Hints {
			fmt.Printf("- [%s] %s: %s\n", strings.ToUpper(hint.Severity), hint.Code, hint.Message)
		}
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc prompt [list|render --task auto --language auto --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[inspect --query \"...\" --full]|[stats --max-template-tokens 450]|[recommend --query \"...\" --runtime-tool-style ... --runtime-max-context ...]")
		return 2
	}
}
