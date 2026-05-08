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
		return runPromptRender(eng, args[1:], projectRoot, lib, jsonMode)

	case "inspect", "show":
		return runPromptInspect(eng, args[1:], projectRoot, jsonMode)

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
