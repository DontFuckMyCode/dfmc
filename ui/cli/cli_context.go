// Context CLI: `dfmc context [budget|recommend|recent|brief]` inspects
// the context-budget planner, the tuning recommender, recent files, and
// the project brief. Extracted from cli_analysis.go.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runContext(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"budget"}
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))

	switch action {
	case "budget", "show":
		fs := flag.NewFlagSet("context budget", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		query := fs.String("query", "", "query for task-aware budget simulation")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for budget simulation")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for budget simulation")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for budget simulation")
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
		preview := eng.ContextBudgetPreviewWithRuntime(*query, runtimeHints)
		if jsonMode {
			mustPrintJSON(preview)
			return 0
		}
		fmt.Printf("context budget: provider=%s model=%s task=%s mentions=%d scale[t=%.2f f=%.2f pf=%.2f] provider_max=%d available=%d reserve_total=%d reserve[prompt=%d history=%d response=%d tools=%d] total=%d per_file=%d history=%d files=%d compression=%s tests=%t docs=%t\n",
			preview.Provider,
			preview.Model,
			preview.Task,
			preview.ExplicitFileMentions,
			preview.TaskTotalScale,
			preview.TaskFileScale,
			preview.TaskPerFileScale,
			preview.ProviderMaxContext,
			preview.ContextAvailableTokens,
			preview.ReserveTotalTokens,
			preview.ReservePromptTokens,
			preview.ReserveHistoryTokens,
			preview.ReserveResponseTokens,
			preview.ReserveToolTokens,
			preview.MaxTokensTotal,
			preview.MaxTokensPerFile,
			preview.MaxHistoryTokens,
			preview.MaxFiles,
			preview.Compression,
			preview.IncludeTests,
			preview.IncludeDocs,
		)
		return 0

	case "recommend", "recommendations":
		fs := flag.NewFlagSet("context recommend", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		query := fs.String("query", "", "query for context tuning recommendations")
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
		preview := eng.ContextBudgetPreviewWithRuntime(*query, runtimeHints)
		recs := eng.ContextRecommendationsWithRuntime(*query, runtimeHints)
		tuning := eng.ContextTuningSuggestionsWithRuntime(*query, runtimeHints)
		if jsonMode {
			_ = printJSON(map[string]any{
				"query":              strings.TrimSpace(*query),
				"preview":            preview,
				"recommendations":    recs,
				"tuning_suggestions": tuning,
			})
			return 0
		}
		fmt.Printf("context recommend: task=%s mentions=%d available=%d total=%d reserve=%d\n",
			preview.Task,
			preview.ExplicitFileMentions,
			preview.ContextAvailableTokens,
			preview.MaxTokensTotal,
			preview.ReserveTotalTokens,
		)
		for _, rec := range recs {
			fmt.Printf("- [%s] %s: %s\n", strings.ToUpper(rec.Severity), rec.Code, rec.Message)
		}
		if len(tuning) > 0 {
			fmt.Println("tuning suggestions:")
			for _, s := range tuning {
				fmt.Printf("- [%s] %s=%v (%s)\n", strings.ToUpper(strings.TrimSpace(s.Priority)), s.Key, s.Value, s.Reason)
			}
		}
		return 0

	case "recent", "files":
		w := eng.MemoryWorking()
		if jsonMode {
			_ = printJSON(map[string]any{
				"count": len(w.RecentFiles),
				"files": w.RecentFiles,
			})
			return 0
		}
		if len(w.RecentFiles) == 0 {
			fmt.Println("context: no recent files yet")
			return 0
		}
		fmt.Println("recent context files:")
		for _, f := range w.RecentFiles {
			fmt.Printf("- %s\n", f)
		}
		return 0

	case "brief":
		fs := flag.NewFlagSet("context brief", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		maxWords := fs.Int("max-words", 240, "max words for context brief")
		pathFlag := fs.String("path", "", "path to magic doc file (relative to project root or absolute)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		projectRoot := strings.TrimSpace(eng.Status().ProjectRoot)
		if projectRoot == "" {
			fmt.Fprintln(os.Stderr, "context brief error: project root is not set")
			return 1
		}
		targetPath := resolvePromptBriefPath(projectRoot, strings.TrimSpace(*pathFlag))
		data, err := os.ReadFile(targetPath)
		exists := err == nil
		brief := loadPromptProjectBriefWithPath(projectRoot, strings.TrimSpace(*pathFlag), *maxWords)
		if brief == "" {
			brief = "(none)"
		}
		wordCount := len(strings.Fields(strings.TrimSpace(brief)))
		sizeBytes := 0
		if exists {
			sizeBytes = len(data)
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"path":       filepath.ToSlash(targetPath),
				"exists":     exists,
				"max_words":  *maxWords,
				"word_count": wordCount,
				"brief":      brief,
				"size_bytes": sizeBytes,
			})
			return 0
		}
		fmt.Printf("context brief: path=%s exists=%t words=%d max=%d bytes=%d\n", filepath.ToSlash(targetPath), exists, wordCount, *maxWords, sizeBytes)
		fmt.Println(brief)
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc context [budget --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[recommend --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[recent]|[brief --max-words 240 --path ...]")
		return 2
	}
}
