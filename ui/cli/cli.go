package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type globalOptions struct {
	Provider string
	Model    string
	Profile  string
	Verbose  bool
	JSON     bool
	NoColor  bool
	Project  string
}

func Run(ctx context.Context, eng *engine.Engine, args []string, version string) int {
	opts, rest, err := parseGlobalFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flag error: %v\n", err)
		return 2
	}

	eng.SetProviderModel(opts.Provider, opts.Model)
	eng.SetVerbose(opts.Verbose)

	if len(rest) == 0 {
		printHelp()
		return 0
	}

	cmd := rest[0]
	cmdArgs := rest[1:]

	switch cmd {
	case "help", "-h", "--help":
		printHelp()
		return 0
	case "version":
		return runVersion(eng, version, cmdArgs, opts.JSON)
	case "init":
		return runInit(opts.JSON, opts.Project)
	case "ask":
		return runAsk(ctx, eng, cmdArgs, opts.JSON)
	case "chat":
		return runChat(ctx, eng, cmdArgs, opts.JSON)
	case "analyze":
		return runAnalyze(ctx, eng, cmdArgs, opts.JSON)
	case "map":
		return runMap(ctx, eng, cmdArgs, opts.JSON)
	case "tool":
		return runTool(ctx, eng, cmdArgs, opts.JSON)
	case "scan":
		return runScan(ctx, eng, cmdArgs, opts.JSON)
	case "memory":
		return runMemory(ctx, eng, cmdArgs, opts.JSON)
	case "conversation", "conv":
		return runConversation(ctx, eng, cmdArgs, opts.JSON)
	case "config", "plugin", "skill", "serve", "remote", "review", "explain", "refactor", "test", "doc":
		return runPlaceholder(cmd, opts.JSON)
	default:
		if strings.HasPrefix(cmd, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", cmd)
			return 2
		}
		// If command is not known, treat it as a one-shot question.
		return runAsk(ctx, eng, rest, opts.JSON)
	}
}

func parseGlobalFlags(args []string) (globalOptions, []string, error) {
	var opts globalOptions
	fs := flag.NewFlagSet("dfmc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fs.StringVar(&opts.Provider, "provider", "", "LLM provider override")
	fs.StringVar(&opts.Model, "model", "", "model override")
	fs.StringVar(&opts.Profile, "profile", "", "config profile")
	fs.BoolVar(&opts.Verbose, "verbose", false, "verbose output")
	fs.BoolVar(&opts.JSON, "json", false, "json output mode")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable colors")
	fs.StringVar(&opts.Project, "project", "", "project root path")

	if err := fs.Parse(args); err != nil {
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func runVersion(eng *engine.Engine, version string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := fs.Bool("json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || *jsonFlag

	st := eng.Status()
	payload := map[string]any{
		"name":         "dfmc",
		"version":      version,
		"provider":     st.Provider,
		"model":        st.Model,
		"project_root": st.ProjectRoot,
		"state":        st.State,
		"go_version":   runtimeVersion(),
	}
	if jsonMode {
		_ = printJSON(payload)
		return 0
	}
	fmt.Printf("dfmc %s\n", version)
	fmt.Printf("provider: %s\n", st.Provider)
	fmt.Printf("model: %s\n", st.Model)
	fmt.Printf("project: %s\n", st.ProjectRoot)
	return 0
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

func runAsk(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	question := strings.TrimSpace(strings.Join(args, " "))
	if question == "" {
		fmt.Fprintln(os.Stderr, "ask requires a question")
		return 2
	}

	answer, err := eng.Ask(ctx, question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask failed: %v\n", err)
		return 1
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"question": question,
			"answer":   answer,
		})
		return 0
	}

	fmt.Println(answer)
	return 0
}

func runChat(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = args
	if jsonMode {
		_ = printJSON(map[string]any{
			"status": "chat_started",
			"mode":   "basic_repl",
		})
		return 0
	}

	fmt.Println("DFMC interactive chat (type /exit to quit)")
	fmt.Println("Note: full provider streaming pipeline will be added in next phases.")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return 0
		default:
		}

		fmt.Print("dfmc> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				if !errors.Is(err, os.ErrClosed) {
					fmt.Fprintf(os.Stderr, "input error: %v\n", err)
				}
			}
			return 0
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "exit" || line == "quit" {
			return 0
		}
		if line == "/save" {
			if err := eng.ConversationSave(); err != nil {
				fmt.Fprintf(os.Stderr, "save error: %v\n", err)
			} else {
				fmt.Println("conversation saved")
			}
			continue
		}
		if line == "/history" {
			items, err := eng.ConversationList()
			if err != nil {
				fmt.Fprintf(os.Stderr, "history error: %v\n", err)
			} else {
				for i, item := range items {
					if i >= 10 {
						break
					}
					fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
				}
			}
			continue
		}
		if line == "/memory" {
			w := eng.MemoryWorking()
			fmt.Printf("last question: %s\n", w.LastQuestion)
			fmt.Printf("last answer: %s\n", truncateLine(w.LastAnswer, 120))
			fmt.Printf("recent files: %d\n", len(w.RecentFiles))
			continue
		}
		resp, err := eng.Ask(ctx, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		fmt.Println(resp)
	}
}

func runPlaceholder(name string, jsonMode bool) int {
	if jsonMode {
		_ = printJSON(map[string]any{
			"command": name,
			"status":  "not_implemented",
		})
		return 0
	}
	fmt.Printf("%s is scaffolded but not implemented yet.\n", name)
	return 0
}

func runAnalyze(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var jsonFlag bool
	var full bool
	var security bool
	var complexity bool
	var deadCode bool
	fs.BoolVar(&jsonFlag, "json", false, "output as json")
	fs.BoolVar(&full, "full", false, "run full analysis set")
	fs.BoolVar(&security, "security", false, "run security analysis")
	fs.BoolVar(&complexity, "complexity", false, "run complexity analysis")
	fs.BoolVar(&deadCode, "dead-code", false, "run dead code analysis")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || jsonFlag

	path := ""
	if len(fs.Args()) > 0 {
		path = fs.Args()[0]
	}
	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{
		Path:       path,
		Full:       full,
		Security:   security,
		Complexity: complexity,
		DeadCode:   deadCode,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze failed: %v\n", err)
		return 1
	}
	if jsonMode {
		_ = printJSON(report)
		return 0
	}
	fmt.Printf("Project: %s\n", report.ProjectRoot)
	fmt.Printf("Files:   %d\n", report.Files)
	fmt.Printf("Nodes:   %d\n", report.Nodes)
	fmt.Printf("Edges:   %d\n", report.Edges)
	fmt.Printf("Cycles:  %d\n", report.Cycles)
	if len(report.HotSpots) > 0 {
		fmt.Println("Hot spots:")
		for i, n := range report.HotSpots {
			if i >= 5 {
				break
			}
			fmt.Printf("  - %s (%s)\n", n.Name, n.Kind)
		}
	}
	if report.Security != nil {
		fmt.Printf("Security: secrets=%d vulns=%d\n", len(report.Security.Secrets), len(report.Security.Vulnerabilities))
	}
	if report.Complexity != nil {
		fmt.Printf("Complexity: avg=%.2f max=%d functions=%d\n", report.Complexity.Average, report.Complexity.Max, len(report.Complexity.TopFunctions))
	}
	if len(report.DeadCode) > 0 {
		fmt.Printf("Dead code candidates: %d\n", len(report.DeadCode))
	}
	return 0
}

func runMap(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	format := fs.String("format", "ascii", "ascii|json")
	jsonFlag := fs.Bool("json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) > 0 {
		*format = fs.Args()[0]
	}
	jsonMode = jsonMode || *jsonFlag
	f := strings.ToLower(*format)
	_, _ = eng.Analyze(ctx, "")

	graph := eng.CodeMap.Graph()
	if graph == nil {
		fmt.Fprintln(os.Stderr, "codemap is not initialized")
		return 1
	}

	if jsonMode || f == "json" {
		_ = printJSON(map[string]any{
			"nodes": graph.Nodes(),
			"edges": graph.Edges(),
		})
		return 0
	}

	for _, e := range graph.Edges() {
		fmt.Printf("%s -> %s (%s)\n", e.From, e.To, e.Type)
	}
	return 0
}

func runTool(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 || args[0] == "list" {
		tools := eng.ListTools()
		if jsonMode {
			_ = printJSON(map[string]any{"tools": tools})
			return 0
		}
		for _, t := range tools {
			fmt.Println(t)
		}
		return 0
	}

	if args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool [list|run <name> [--key value ...]]")
		return 2
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool run <name> [--key value ...]")
		return 2
	}
	name := args[1]
	params := map[string]any{}
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		part := rest[i]
		if !strings.HasPrefix(part, "--") {
			continue
		}
		key := strings.TrimPrefix(part, "--")
		val := "true"
		if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
			val = rest[i+1]
			i++
		}
		params[key] = val
	}

	res, err := eng.CallTool(ctx, name, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tool error: %v\n", err)
		return 1
	}
	if jsonMode {
		_ = printJSON(res)
		return 0
	}
	if strings.TrimSpace(res.Output) != "" {
		fmt.Println(res.Output)
	}
	return 0
}

func runMemory(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"working"}
	}
	cmd := args[0]
	switch cmd {
	case "working":
		w := eng.MemoryWorking()
		if jsonMode {
			_ = printJSON(w)
			return 0
		}
		fmt.Printf("Last question: %s\n", w.LastQuestion)
		fmt.Printf("Last answer: %s\n", truncateLine(w.LastAnswer, 160))
		fmt.Printf("Recent files: %d\n", len(w.RecentFiles))
		fmt.Printf("Recent symbols: %d\n", len(w.RecentSymbols))
		return 0
	case "list":
		fs := flag.NewFlagSet("memory list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		limit := fs.Int("limit", 20, "max entries")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		items, err := eng.MemoryList(parseTier(*tierS), *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory list error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(items)
			return 0
		}
		for _, e := range items {
			fmt.Printf("- %s | %s | %s\n", e.ID, e.Key, truncateLine(e.Value, 120))
		}
		return 0
	case "search":
		fs := flag.NewFlagSet("memory search", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		limit := fs.Int("limit", 20, "max entries")
		query := fs.String("query", "", "search query")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
			*query = strings.Join(fs.Args(), " ")
		}
		items, err := eng.MemorySearch(*query, parseTier(*tierS), *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory search error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(items)
			return 0
		}
		for _, e := range items {
			fmt.Printf("- %s | %s | %s\n", e.ID, e.Key, truncateLine(e.Value, 120))
		}
		return 0
	case "add":
		fs := flag.NewFlagSet("memory add", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		key := fs.String("key", "", "memory key")
		value := fs.String("value", "", "memory value")
		category := fs.String("category", "note", "memory category")
		conf := fs.Float64("confidence", 0.7, "confidence 0..1")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *key == "" || *value == "" {
			fmt.Fprintln(os.Stderr, "memory add requires --key and --value")
			return 2
		}
		entry := types.MemoryEntry{
			Tier:       parseTier(*tierS),
			Category:   *category,
			Key:        *key,
			Value:      *value,
			Confidence: *conf,
			Project:    eng.Status().ProjectRoot,
		}
		if err := eng.MemoryAdd(entry); err != nil {
			fmt.Fprintf(os.Stderr, "memory add error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{"status": "ok"})
		} else {
			fmt.Println("memory entry added")
		}
		return 0
	case "clear":
		fs := flag.NewFlagSet("memory clear", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if err := eng.MemoryClear(parseTier(*tierS)); err != nil {
			fmt.Fprintf(os.Stderr, "memory clear error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{"status": "ok"})
		} else {
			fmt.Println("memory cleared")
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc memory [working|list|search|add|clear]")
		return 2
	}
}

func runScan(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var jsonFlag bool
	fs.BoolVar(&jsonFlag, "json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || jsonFlag
	path := ""
	if len(fs.Args()) > 0 {
		path = fs.Args()[0]
	}

	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{
		Path:     path,
		Security: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		return 1
	}
	if report.Security == nil {
		fmt.Println("No security report generated.")
		return 0
	}
	if jsonMode {
		_ = printJSON(report.Security)
		return 0
	}
	fmt.Printf("Scanned files: %d\n", report.Security.FilesScanned)
	fmt.Printf("Secrets: %d\n", len(report.Security.Secrets))
	for i, f := range report.Security.Secrets {
		if i >= 10 {
			break
		}
		fmt.Printf("  - [%s] %s:%d %s (%s)\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Pattern, f.Match)
	}
	fmt.Printf("Vulnerabilities: %d\n", len(report.Security.Vulnerabilities))
	for i, f := range report.Security.Vulnerabilities {
		if i >= 10 {
			break
		}
		fmt.Printf("  - [%s] %s:%d %s | %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Kind, f.CWE)
	}
	return 0
}

func runConversation(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items, err := eng.ConversationList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation list error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(items)
			return 0
		}
		for _, item := range items {
			fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
		}
		return 0
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation search <query>")
			return 2
		}
		query := strings.Join(args[1:], " ")
		items, err := eng.ConversationSearch(query, 20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation search error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(items)
			return 0
		}
		for _, item := range items {
			fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc conversation [list|search <query>]")
		return 2
	}
}

func parseTier(v string) types.MemoryTier {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "semantic":
		return types.MemorySemantic
	default:
		return types.MemoryEpisodic
	}
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func printHelp() {
	fmt.Println(`Usage: dfmc [global flags] <command> [args]

Commands:
  init        Initialize DFMC in project
  chat        Interactive chat session
  ask         One-shot question
  analyze     Analyze codebase
  scan        Quick security scan
  map         Generate/display codemap
  tool        Tool engine (list/run)
  conversation Conversation history (list/search)
  memory      Memory management
  config      Configuration management (placeholder)
  plugin      Plugin management (placeholder)
  skill       Skill management (placeholder)
  serve       Start WebUI server (placeholder)
  remote      Remote control server (placeholder)
  review      Code review shortcut (placeholder)
  explain     Explain code shortcut (placeholder)
  refactor    Refactor code shortcut (placeholder)
  test        Test generation shortcut (placeholder)
  doc         Documentation shortcut (placeholder)
  version     Version info

Global flags:
  --provider  LLM provider override
  --model     Model override
  --profile   Config profile
  --verbose   Verbose output
  --json      JSON output mode
  --no-color  Disable colors
  --project   Project root path`)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runtimeVersion() string {
	return runtime.Version()
}
