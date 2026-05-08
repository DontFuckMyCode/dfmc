package cli

// cli_prompt_render.go — the two heaviest `dfmc prompt` subcommands:
// `render` (resolves task/language/profile/role + builds vars +
// renders+budget-trims+token-counts the template) and
// `inspect` (walks the system-prompt bundle section-by-section with
// per-section token/lines/preview metadata, optional `--full` body
// dump). Sibling of cli_prompt.go which keeps the runPrompt dispatcher
// and the smaller list/stats/recommend cases.

import (
	"flag"
	"fmt"
	"os"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func runPromptRender(eng *engine.Engine, args []string, projectRoot string, lib *promptlib.Library, jsonMode bool) int {
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
	if err := fs.Parse(args); err != nil {
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
		"context_files":    strings.TrimSpace(*contextFiles),
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
}

func runPromptInspect(eng *engine.Engine, args []string, projectRoot string, jsonMode bool) int {
	fs := flag.NewFlagSet("prompt inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	full := fs.Bool("full", false, "print each section's full body instead of a preview")
	queryFlag := fs.String("query", "", "user query to seed task/profile/role detection")
	if err := fs.Parse(args); err != nil {
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
}
