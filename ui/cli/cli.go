package cli

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/pkg/types"
	"github.com/dontfuckmycode/dfmc/ui/web"
	"gopkg.in/yaml.v3"
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
	case "serve":
		return runServe(ctx, eng, cmdArgs, opts.JSON)
	case "config":
		return runConfig(ctx, eng, cmdArgs, opts.JSON)
	case "prompt":
		return runPrompt(ctx, eng, cmdArgs, opts.JSON)
	case "plugin":
		return runPlugin(ctx, eng, cmdArgs, opts.JSON)
	case "skill":
		return runSkill(ctx, eng, cmdArgs, opts.JSON)
	case "review", "explain", "refactor", "test", "doc":
		return runSkillShortcut(ctx, eng, cmd, cmdArgs, opts.JSON)
	case "remote":
		return runRemote(ctx, eng, cmdArgs, opts.JSON)
	case "doctor":
		return runDoctor(ctx, eng, cmdArgs, opts.JSON)
	case "completion":
		return runCompletion(cmdArgs, opts.JSON)
	case "man":
		return runMan(cmdArgs, opts.JSON)
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
		_ = printJSON(payload)
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
		{Name: "init", Description: "Initialize DFMC in project"},
		{Name: "chat", Description: "Interactive chat session"},
		{Name: "ask", Description: "One-shot question"},
		{Name: "analyze", Description: "Analyze codebase"},
		{Name: "scan", Description: "Quick security scan"},
		{Name: "map", Description: "Generate/display codemap"},
		{Name: "tool", Description: "Tool engine (list/run)"},
		{Name: "conversation", Description: "Conversation management (list/search/load/save/undo/branch)"},
		{Name: "memory", Description: "Memory management"},
		{Name: "config", Description: "Configuration management"},
		{Name: "prompt", Description: "Prompt library management"},
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
		"init",
		"chat",
		"ask",
		"analyze",
		"scan",
		"map",
		"tool",
		"conversation",
		"memory",
		"config",
		"prompt",
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
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	branch := fs.String("branch", "", "start/switch to branch name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if eng.ConversationActive() == nil {
		_ = eng.ConversationStart()
	}
	if b := strings.TrimSpace(*branch); b != "" {
		if err := eng.ConversationBranchSwitch(b); err != nil {
			if err2 := eng.ConversationBranchCreate(b); err2 != nil {
				fmt.Fprintf(os.Stderr, "branch setup failed: %v\n", err)
				return 1
			}
			if err2 := eng.ConversationBranchSwitch(b); err2 != nil {
				fmt.Fprintf(os.Stderr, "branch switch failed: %v\n", err2)
				return 1
			}
		}
	}

	if jsonMode {
		active := eng.ConversationActive()
		branchName := ""
		if active != nil {
			branchName = active.Branch
		}
		_ = printJSON(map[string]any{
			"status": "chat_started",
			"mode":   "basic_repl",
			"branch": branchName,
		})
		return 0
	}

	fmt.Println("DFMC interactive chat (type /exit to quit)")
	fmt.Println("Type /help for slash commands.")

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
		if strings.HasPrefix(line, "/") {
			shouldExit, handled := runChatSlash(ctx, eng, line)
			if handled {
				if shouldExit {
					return 0
				}
				continue
			}
		}
		if line == "/exit" || line == "exit" || line == "quit" {
			return 0
		}
		stream, err := eng.StreamAsk(ctx, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		printed := false
		endsWithNL := true
		for ev := range stream {
			switch ev.Type {
			case "delta":
				fmt.Print(ev.Delta)
				printed = true
				endsWithNL = strings.HasSuffix(ev.Delta, "\n")
			case "error":
				if printed && !endsWithNL {
					fmt.Println()
				}
				fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
				printed = false
			case "done":
			}
		}
		if printed && !endsWithNL {
			fmt.Println()
		}
	}
}

func runChatSlash(ctx context.Context, eng *engine.Engine, line string) (exit bool, handled bool) {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) == 0 {
		return false, false
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		printChatHelp()
		return false, true

	case "/exit":
		return true, true

	case "/clear":
		conv := eng.ConversationStart()
		if conv == nil {
			fmt.Fprintln(os.Stderr, "unable to create conversation")
			return false, true
		}
		fmt.Printf("Started new conversation: %s\n", conv.ID)
		return false, true

	case "/save":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "save error: %v\n", err)
		} else {
			fmt.Println("conversation saved")
		}
		return false, true

	case "/load":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /load <conversation-id>")
			return false, true
		}
		conv, err := eng.ConversationLoad(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "load error: %v\n", err)
			return false, true
		}
		fmt.Printf("Loaded conversation %s (%d messages)\n", conv.ID, len(conv.Messages()))
		return false, true

	case "/history":
		limit := 10
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := eng.ConversationList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "history error: %v\n", err)
			return false, true
		}
		for i, item := range items {
			if i >= limit {
				break
			}
			fmt.Printf("- %s (%d messages)\n", item.ID, item.MessageN)
		}
		return false, true

	case "/memory":
		w := eng.MemoryWorking()
		fmt.Printf("last question: %s\n", w.LastQuestion)
		fmt.Printf("last answer: %s\n", truncateLine(w.LastAnswer, 120))
		fmt.Printf("recent files: %d\n", len(w.RecentFiles))
		return false, true

	case "/provider":
		if len(args) == 0 {
			st := eng.Status()
			fmt.Printf("provider=%s model=%s\n", st.Provider, st.Model)
			return false, true
		}
		eng.SetProviderModel(strings.TrimSpace(args[0]), "")
		st := eng.Status()
		fmt.Printf("provider set to %s (model=%s)\n", st.Provider, st.Model)
		return false, true

	case "/model":
		if len(args) == 0 {
			st := eng.Status()
			fmt.Printf("provider=%s model=%s\n", st.Provider, st.Model)
			return false, true
		}
		st := eng.Status()
		eng.SetProviderModel(st.Provider, strings.TrimSpace(args[0]))
		st = eng.Status()
		fmt.Printf("model set to %s (provider=%s)\n", st.Model, st.Provider)
		return false, true

	case "/branch":
		if eng.ConversationActive() == nil {
			_ = eng.ConversationStart()
		}
		if len(args) == 0 {
			active := eng.ConversationActive()
			fmt.Printf("current branch: %s\n", active.Branch)
			for _, name := range eng.ConversationBranchList() {
				fmt.Printf("- %s\n", name)
			}
			return false, true
		}
		action := strings.ToLower(strings.TrimSpace(args[0]))
		switch action {
		case "list":
			for _, name := range eng.ConversationBranchList() {
				fmt.Printf("- %s\n", name)
			}
			return false, true
		case "create":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: /branch create <name>")
				return false, true
			}
			name := strings.TrimSpace(args[1])
			if err := eng.ConversationBranchCreate(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch create error: %v\n", err)
				return false, true
			}
			fmt.Printf("branch created: %s\n", name)
			return false, true
		case "switch":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: /branch switch <name>")
				return false, true
			}
			name := strings.TrimSpace(args[1])
			if err := eng.ConversationBranchSwitch(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
				return false, true
			}
			fmt.Printf("switched branch: %s\n", name)
			return false, true
		case "compare":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: /branch compare <branch-a> <branch-b>")
				return false, true
			}
			comp, err := eng.ConversationBranchCompare(args[1], args[2])
			if err != nil {
				fmt.Fprintf(os.Stderr, "branch compare error: %v\n", err)
				return false, true
			}
			fmt.Printf("%s vs %s: shared=%d only_%s=%d only_%s=%d\n",
				comp.BranchA, comp.BranchB, comp.SharedPrefixN, comp.BranchA, comp.OnlyA, comp.BranchB, comp.OnlyB)
			return false, true
		default:
			// /branch <name> => switch if exists, otherwise create+switch
			name := strings.TrimSpace(args[0])
			if err := eng.ConversationBranchSwitch(name); err == nil {
				fmt.Printf("switched branch: %s\n", name)
				return false, true
			}
			if err := eng.ConversationBranchCreate(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch error: %v\n", err)
				return false, true
			}
			if err := eng.ConversationBranchSwitch(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
				return false, true
			}
			fmt.Printf("created and switched branch: %s\n", name)
			return false, true
		}

	case "/tools":
		for _, t := range eng.ListTools() {
			fmt.Printf("- %s\n", t)
		}
		return false, true

	case "/skills":
		for _, s := range discoverSkills(eng.Status().ProjectRoot) {
			source := s.Source
			if s.Builtin {
				source = "builtin"
			}
			fmt.Printf("- %s [%s]\n", s.Name, source)
		}
		return false, true

	case "/context":
		action := "show"
		if len(args) > 0 {
			action = strings.ToLower(strings.TrimSpace(args[0]))
		}
		switch action {
		case "show":
			w := eng.MemoryWorking()
			if len(w.RecentFiles) == 0 {
				fmt.Println("context: no recent files yet")
				return false, true
			}
			fmt.Println("recent context files:")
			for _, f := range w.RecentFiles {
				fmt.Printf("- %s\n", f)
			}
		case "add", "rm", "remove":
			fmt.Println("context add/remove is not available in this REPL yet")
		default:
			fmt.Fprintln(os.Stderr, "usage: /context [show|add <file>|rm <file>]")
		}
		return false, true

	case "/cost":
		active := eng.ConversationActive()
		if active == nil {
			fmt.Println("no active conversation")
			return false, true
		}
		msgN, userN, assistantN, tokenN := summarizeMessageUsage(active.Messages())
		usd := estimateConversationCostUSD(strings.ToLower(strings.TrimSpace(active.Provider)), tokenN)
		fmt.Printf("messages=%d user=%d assistant=%d tokens=%d", msgN, userN, assistantN, tokenN)
		if usd >= 0 {
			fmt.Printf(" approx_cost=$%.6f", usd)
		}
		fmt.Println()
		return false, true

	case "/diff":
		diff, err := gitWorkingDiff(eng.Status().ProjectRoot, 200_000)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff error: %v\n", err)
			return false, true
		}
		if strings.TrimSpace(diff) == "" {
			fmt.Println("working tree is clean")
			return false, true
		}
		fmt.Print(diff)
		if !strings.HasSuffix(diff, "\n") {
			fmt.Println()
		}
		return false, true

	case "/undo":
		removed, err := eng.ConversationUndoLast()
		if err != nil {
			fmt.Fprintf(os.Stderr, "undo error: %v\n", err)
			return false, true
		}
		fmt.Printf("undone messages: %d\n", removed)
		return false, true

	case "/apply":
		fmt.Println("apply is not implemented in this REPL yet")
		return false, true

	case "/run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: /run <skill> [input]")
			return false, true
		}
		name := strings.TrimSpace(args[0])
		input := strings.TrimSpace(strings.Join(args[1:], " "))
		if input == "" {
			input = "Analyze the current project."
		}
		_ = runNamedSkill(ctx, eng, name, input, false)
		return false, true
	}

	return false, false
}

func printChatHelp() {
	fmt.Println(`Chat slash commands:
  /help                         Show this help
  /exit                         Exit chat
  /clear                        Start a new conversation
  /save                         Save active conversation
  /load <id>                    Load conversation by id
  /history [limit]              List saved conversations
  /provider [name]              Show/set provider
  /model [name]                 Show/set model
  /branch [name]                Switch/create branch
  /branch list                  List branches
  /branch create <name>         Create branch
  /branch switch <name>         Switch branch
  /branch compare <a> <b>       Compare branches
  /context [show]               Show recent context files
  /memory                       Show working memory snapshot
  /tools                        List tools
  /skills                       List skills
  /diff                         Show working tree diff
  /undo                         Undo last conversation exchange
  /apply                        Reserved for future patch apply flow
  /run <skill> [input]          Run skill
  /cost                         Show token/cost summary`)
}

func summarizeMessageUsage(msgs []types.Message) (messages int, users int, assistants int, tokens int) {
	for _, msg := range msgs {
		messages++
		switch msg.Role {
		case types.RoleUser:
			users++
		case types.RoleAssistant:
			assistants++
		}
		if msg.TokenCnt > 0 {
			tokens += msg.TokenCnt
			continue
		}
		// Fallback estimate for historic messages without explicit token count.
		tokens += len(strings.Fields(msg.Content))
	}
	return messages, users, assistants, tokens
}

func estimateConversationCostUSD(provider string, totalTokens int) float64 {
	if totalTokens <= 0 {
		return 0
	}
	blendedPerMillion := map[string]float64{
		"anthropic": 9.0,
		"openai":    6.25,
		"google":    7.0,
		"deepseek":  0.35,
		"kimi":      1.8,
		"minimax":   0.75,
		"zai":       2.1,
		"alibaba":   2.0,
	}
	rate, ok := blendedPerMillion[provider]
	if !ok {
		return -1
	}
	return (float64(totalTokens) / 1_000_000.0) * rate
}

func gitWorkingDiff(projectRoot string, maxBytes int64) (string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	args := []string{"-C", root, "diff", "--"}
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		out = out[:maxBytes]
		return string(out) + "\n... [truncated]\n", nil
	}
	return string(out), nil
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
	var deps bool
	fs.BoolVar(&jsonFlag, "json", false, "output as json")
	fs.BoolVar(&full, "full", false, "run full analysis set")
	fs.BoolVar(&security, "security", false, "run security analysis")
	fs.BoolVar(&complexity, "complexity", false, "run complexity analysis")
	fs.BoolVar(&deadCode, "dead-code", false, "run dead code analysis")
	fs.BoolVar(&deps, "deps", false, "run dependency analysis summary")
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
	depSummary := []depStat{}
	if deps || full {
		depSummary = collectDependencyStats(eng, 20)
	}
	if jsonMode {
		if deps || full {
			_ = printJSON(map[string]any{
				"report":        report,
				"dependencies":  depSummary,
				"dep_count":     len(depSummary),
				"dep_requested": true,
			})
		} else {
			_ = printJSON(report)
		}
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
	if deps || full {
		fmt.Printf("Dependencies: %d\n", len(depSummary))
		for i, d := range depSummary {
			if i >= 10 {
				break
			}
			fmt.Printf("  - %s (%d imports)\n", d.Module, d.Count)
		}
	}
	return 0
}

func runMap(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	format := fs.String("format", "ascii", "ascii|json|dot|svg")
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
	if f == "dot" {
		fmt.Println(graphToDOT(graph.Nodes(), graph.Edges()))
		return 0
	}
	if f == "svg" {
		fmt.Println(graphToSVG(graph.Nodes(), graph.Edges()))
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

	case "active":
		active := eng.ConversationActive()
		if active == nil {
			if jsonMode {
				_ = printJSON(map[string]any{"active": nil})
				return 0
			}
			fmt.Println("No active conversation.")
			return 0
		}
		payload := map[string]any{
			"id":         active.ID,
			"provider":   active.Provider,
			"model":      active.Model,
			"started_at": active.StartedAt,
			"branch":     active.Branch,
			"branches":   eng.ConversationBranchList(),
			"messages":   len(active.Messages()),
		}
		if jsonMode {
			_ = printJSON(payload)
			return 0
		}
		fmt.Printf("ID:       %s\n", active.ID)
		fmt.Printf("Provider: %s\n", active.Provider)
		fmt.Printf("Model:    %s\n", active.Model)
		fmt.Printf("Branch:   %s\n", active.Branch)
		fmt.Printf("Messages: %d\n", len(active.Messages()))
		return 0

	case "new", "clear":
		c := eng.ConversationStart()
		if c == nil {
			fmt.Fprintln(os.Stderr, "failed to start a new conversation")
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status": "ok",
				"id":     c.ID,
				"branch": c.Branch,
			})
		} else {
			fmt.Printf("Started new conversation: %s\n", c.ID)
		}
		return 0

	case "save":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "conversation save error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{"status": "ok"})
		} else {
			fmt.Println("conversation saved")
		}
		return 0

	case "undo":
		n, err := eng.ConversationUndoLast()
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation undo error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{"status": "ok", "removed": n})
		} else {
			fmt.Printf("undone messages: %d\n", n)
		}
		return 0

	case "load":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation load <id>")
			return 2
		}
		c, err := eng.ConversationLoad(strings.TrimSpace(args[1]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "conversation load error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"id":       c.ID,
				"branch":   c.Branch,
				"messages": len(c.Messages()),
			})
		} else {
			fmt.Printf("Loaded %s (%d messages)\n", c.ID, len(c.Messages()))
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

	case "branch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch [list|create|switch|compare]")
			return 2
		}
		action := strings.ToLower(strings.TrimSpace(args[1]))
		if eng.ConversationActive() == nil {
			_ = eng.ConversationStart()
		}
		switch action {
		case "list":
			items := eng.ConversationBranchList()
			if jsonMode {
				_ = printJSON(map[string]any{"branches": items})
			} else {
				for _, name := range items {
					fmt.Printf("- %s\n", name)
				}
			}
			return 0
		case "create":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch create <name>")
				return 2
			}
			name := strings.TrimSpace(args[2])
			if err := eng.ConversationBranchCreate(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch create error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(map[string]any{"status": "ok", "branch": name})
			} else {
				fmt.Printf("branch created: %s\n", name)
			}
			return 0
		case "switch":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch switch <name>")
				return 2
			}
			name := strings.TrimSpace(args[2])
			if err := eng.ConversationBranchSwitch(name); err != nil {
				fmt.Fprintf(os.Stderr, "branch switch error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(map[string]any{"status": "ok", "branch": name})
			} else {
				fmt.Printf("branch switched: %s\n", name)
			}
			return 0
		case "compare":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch compare <branch-a> <branch-b>")
				return 2
			}
			comp, err := eng.ConversationBranchCompare(strings.TrimSpace(args[2]), strings.TrimSpace(args[3]))
			if err != nil {
				fmt.Fprintf(os.Stderr, "branch compare error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(comp)
			} else {
				fmt.Printf("%s vs %s: shared=%d only_%s=%d only_%s=%d\n",
					comp.BranchA, comp.BranchB, comp.SharedPrefixN, comp.BranchA, comp.OnlyA, comp.BranchB, comp.OnlyB)
			}
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc conversation branch [list|create|switch|compare]")
			return 2
		}

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc conversation [list|search <query>|active|new|clear|save|undo|load <id>|branch ...]")
		return 2
	}
}

func runServe(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", eng.Config.Web.Host, "host")
	port := fs.Int("port", eng.Config.Web.Port, "port")
	auth := fs.String("auth", eng.Config.Web.Auth, "none|token")
	token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_WEB_TOKEN")), "api token (for auth=token)")
	openBrowser := fs.Bool("open-browser", eng.Config.Web.OpenBrowser, "open default browser")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mode := strings.ToLower(strings.TrimSpace(*auth))
	if mode != "none" && mode != "token" {
		fmt.Fprintln(os.Stderr, "serve auth must be none|token")
		return 2
	}
	if mode == "token" && strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "serve token auth requires --token or DFMC_WEB_TOKEN")
		return 2
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"status": "starting",
			"host":   *host,
			"port":   *port,
			"auth":   mode,
		})
	}

	srv := web.New(eng, *host, *port)
	handler := srv.Handler()
	if mode == "token" {
		handler = bearerTokenMiddleware(handler, *token)
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	fmt.Printf("DFMC Web API listening on http://%s\n", addr)
	if *openBrowser {
		target := "http://" + addr
		go func() {
			// Give server a small head-start before opening browser.
			time.Sleep(120 * time.Millisecond)
			_ = tryOpenBrowser(target)
		}()
	}
	if err := serveWithContext(ctx, server); err != nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		return 1
	}
	return 0
}

func runRemote(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"start"}
	}

	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		live := fs.Bool("live", false, "query remote server status instead of local config")
		baseURL := fs.String("url", defaultURL, "remote base URL (for --live)")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		if !*live {
			payload := map[string]any{
				"enabled":   eng.Config.Remote.Enabled,
				"host":      eng.Config.Web.Host,
				"grpc_port": eng.Config.Remote.GRPCPort,
				"ws_port":   eng.Config.Remote.WSPort,
				"auth":      eng.Config.Remote.Auth,
			}
			if jsonMode {
				_ = printJSON(payload)
				return 0
			}
			fmt.Printf("Remote enabled: %t\n", eng.Config.Remote.Enabled)
			fmt.Printf("Host:           %s\n", eng.Config.Web.Host)
			fmt.Printf("gRPC port:      %d\n", eng.Config.Remote.GRPCPort)
			fmt.Printf("WS/HTTP port:   %d\n", eng.Config.Remote.WSPort)
			fmt.Printf("Auth:           %s\n", eng.Config.Remote.Auth)
			return 0
		}

		statusURL := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/status"
		providersURL := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/providers"
		statusPayload, _, err := remoteJSONRequest(http.MethodGet, statusURL, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote status error: %v\n", err)
			return 1
		}
		providersPayload, _, err := remoteJSONRequest(http.MethodGet, providersURL, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote providers error: %v\n", err)
			return 1
		}
		out := map[string]any{
			"url":       *baseURL,
			"status":    statusPayload,
			"providers": providersPayload,
		}
		if jsonMode {
			_ = printJSON(out)
			return 0
		}
		_ = printJSON(out)
		return 0

	case "probe":
		fs := flag.NewFlagSet("remote probe", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 3*time.Second, "request timeout")
		endpointsRaw := fs.String("endpoints", "/healthz,/api/v1/status,/api/v1/providers", "comma-separated endpoint paths")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		client := &http.Client{Timeout: *timeout}
		endpoints := parseEndpointList(*endpointsRaw)
		results := make([]remoteProbeResult, 0, len(endpoints))
		hasFailure := false
		for _, endpoint := range endpoints {
			res := probeRemoteEndpoint(client, *baseURL, endpoint, *token)
			results = append(results, res)
			if !res.OK {
				hasFailure = true
			}
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"url":       *baseURL,
				"timeout":   timeout.String(),
				"endpoints": endpoints,
				"results":   results,
			})
			if hasFailure {
				return 1
			}
			return 0
		}

		fmt.Printf("Remote probe: %s\n", *baseURL)
		for _, r := range results {
			status := "PASS"
			if !r.OK {
				status = "FAIL"
			}
			details := strings.TrimSpace(r.Error)
			if details == "" {
				details = strings.TrimSpace(r.Body)
			}
			if details == "" {
				details = "(empty)"
			}
			fmt.Printf("[%s] %s -> %d (%dms) %s\n", status, r.Endpoint, r.StatusCode, r.DurationMs, truncateLine(details, 160))
		}
		if hasFailure {
			return 1
		}
		return 0

	case "events":
		fs := flag.NewFlagSet("remote events", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		eventType := fs.String("type", "*", "event type filter")
		timeout := fs.Duration("timeout", 20*time.Second, "stream timeout")
		maxEvents := fs.Int("max", 100, "max events to collect before stopping")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/ws"
		if t := strings.TrimSpace(*eventType); t != "" {
			endpoint = endpoint + "?type=" + url.QueryEscape(t)
		}
		events, err := remoteCollectEvents(endpoint, *token, *timeout, *maxEvents)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote events error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"url":      endpoint,
				"count":    len(events),
				"events":   events,
				"timeout":  timeout.String(),
				"max":      *maxEvents,
				"filter":   *eventType,
				"received": time.Now().UTC().Format(time.RFC3339),
			})
			return 0
		}
		fmt.Printf("Remote events: %s\n", endpoint)
		for _, ev := range events {
			kind := strings.TrimSpace(fmt.Sprint(ev["type"]))
			if kind == "" {
				kind = "event"
			}
			body := truncateLine(compactJSON(ev), 200)
			fmt.Printf("[%s] %s\n", strings.ToUpper(kind), body)
		}
		return 0

	case "ask":
		fs := flag.NewFlagSet("remote ask", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
		message := fs.String("message", "", "question/message to send")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*message) == "" && len(fs.Args()) > 0 {
			*message = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		if strings.TrimSpace(*message) == "" {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote ask [--url ...] [--token ...] --message \"...\"")
			return 2
		}

		events, answer, err := remoteAsk(*baseURL, *token, strings.TrimSpace(*message), *timeout, jsonMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote ask error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"url":      *baseURL,
				"message":  *message,
				"events":   events,
				"answer":   answer,
				"event_n":  len(events),
				"received": time.Now().UTC().Format(time.RFC3339),
			})
			return 0
		}
		if !strings.HasSuffix(answer, "\n") {
			fmt.Println()
		}
		return 0

	case "tool":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote tool <name> [--url ...] [--token ...] [--param key=value]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		fs := flag.NewFlagSet("remote tool", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		var paramsRaw multiStringFlag
		fs.Var(&paramsRaw, "param", "tool param in key=value format (repeatable)")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		params, err := parseKeyValueParams(paramsRaw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote tool param error: %v\n", err)
			return 2
		}
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/tools/"+url.PathEscape(name),
			*token,
			map[string]any{"params": params},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote tool error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(payload)
			return 0
		}
		if out, ok := payload["output"]; ok {
			fmt.Println(fmt.Sprint(out))
		} else {
			_ = printJSON(payload)
		}
		return 0

	case "skill":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote skill <name> [--url ...] [--token ...] [--input ...]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		fs := flag.NewFlagSet("remote skill", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
		input := fs.String("input", "", "skill input text")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*input) == "" && len(fs.Args()) > 0 {
			*input = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/skills/"+url.PathEscape(name),
			*token,
			map[string]any{"input": *input},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote skill error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(payload)
			return 0
		}
		if out, ok := payload["answer"]; ok {
			fmt.Println(fmt.Sprint(out))
		} else {
			_ = printJSON(payload)
		}
		return 0

	case "analyze":
		fs := flag.NewFlagSet("remote analyze", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
		path := fs.String("path", "", "target path")
		full := fs.Bool("full", false, "run full analysis")
		security := fs.Bool("security", false, "include security report")
		complexity := fs.Bool("complexity", false, "include complexity report")
		deadCode := fs.Bool("dead-code", false, "include dead-code report")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/analyze",
			*token,
			map[string]any{
				"path":       *path,
				"full":       *full,
				"security":   *security,
				"complexity": *complexity,
				"dead_code":  *deadCode,
			},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote analyze error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(payload)
			return 0
		}
		_ = printJSON(payload)
		return 0

	case "files":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote files [list|get <path>] [--url ...] [--token ...]")
			return 2
		}
		action := strings.ToLower(strings.TrimSpace(args[1]))
		fs := flag.NewFlagSet("remote files", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		limit := fs.Int("limit", 500, "max files for list")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}

		switch action {
		case "list":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/files?limit=" + strconv.Itoa(*limit)
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote files list error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(payload)
				return 0
			}
			root := strings.TrimSpace(fmt.Sprint(payload["root"]))
			if root != "" {
				fmt.Printf("Root: %s\n", root)
			}
			items, _ := payload["files"].([]any)
			for _, it := range items {
				fmt.Println(fmt.Sprint(it))
			}
			return 0
		case "get":
			var rel string
			if len(fs.Args()) > 0 {
				rel = strings.TrimSpace(fs.Args()[0])
			}
			if rel == "" {
				fmt.Fprintln(os.Stderr, "usage: dfmc remote files get <path> [--url ...] [--token ...]")
				return 2
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/files/" + remotePathEscape(rel)
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote files get error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(payload)
				return 0
			}
			if typ := strings.TrimSpace(fmt.Sprint(payload["type"])); typ == "file" {
				fmt.Println(fmt.Sprint(payload["content"]))
				return 0
			}
			_ = printJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote files [list|get <path>] [--url ...] [--token ...]")
			return 2
		}

	case "memory":
		action := "working"
		if len(args) >= 2 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		fs := flag.NewFlagSet("remote memory", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		tier := fs.String("tier", "episodic", "working|episodic|semantic")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}

		switch action {
		case "working":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/memory?tier=working"
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote memory working error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(payload)
			} else {
				_ = printJSON(payload)
			}
			return 0
		case "list":
			v := url.Values{}
			v.Set("tier", strings.ToLower(strings.TrimSpace(*tier)))
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/memory?" + v.Encode()
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote memory list error: %v\n", err)
				return 1
			}
			if jsonMode {
				_ = printJSON(payload)
				return 0
			}
			_ = printJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote memory [working|list --tier episodic|semantic] [--url ...] [--token ...]")
			return 2
		}

	case "conversation":
		action := "list"
		if len(args) >= 2 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		branchAction := "list"
		parseFrom := 2
		if action == "branch" && len(args) >= 3 {
			branchAction = strings.ToLower(strings.TrimSpace(args[2]))
			parseFrom = 3
		}
		fs := flag.NewFlagSet("remote conversation", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		limit := fs.Int("limit", 20, "max results")
		query := fs.String("query", "", "search query")
		id := fs.String("id", "", "conversation id")
		name := fs.String("name", "", "branch name")
		branchA := fs.String("a", "", "branch A name")
		branchB := fs.String("b", "", "branch B name")
		if err := fs.Parse(args[parseFrom:]); err != nil {
			return 2
		}

		switch action {
		case "list":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversations?limit=" + strconv.Itoa(*limit)
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation list error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "search":
			q := strings.TrimSpace(*query)
			if q == "" && len(fs.Args()) > 0 {
				q = strings.TrimSpace(strings.Join(fs.Args(), " "))
			}
			v := url.Values{}
			v.Set("q", q)
			v.Set("limit", strconv.Itoa(*limit))
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversations/search?" + v.Encode()
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation search error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "active":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation"
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation active error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "new", "clear":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/new"
			payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{}, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation new error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "save":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/save"
			payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{}, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation save error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "load":
			convID := strings.TrimSpace(*id)
			if convID == "" && len(fs.Args()) > 0 {
				convID = strings.TrimSpace(fs.Args()[0])
			}
			if convID == "" {
				fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation load --id <conversation-id>")
				return 2
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/load"
			payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{"id": convID}, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation load error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "branch":
			switch branchAction {
			case "list":
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches"
				payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch list error: %v\n", err)
					return 1
				}
				_ = printJSON(payload)
				return 0
			case "create":
				branchName := strings.TrimSpace(*name)
				if branchName == "" && len(fs.Args()) > 0 {
					branchName = strings.TrimSpace(fs.Args()[0])
				}
				if branchName == "" {
					fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch create --name <branch>")
					return 2
				}
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches/create"
				payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{"name": branchName}, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch create error: %v\n", err)
					return 1
				}
				_ = printJSON(payload)
				return 0
			case "switch":
				branchName := strings.TrimSpace(*name)
				if branchName == "" && len(fs.Args()) > 0 {
					branchName = strings.TrimSpace(fs.Args()[0])
				}
				if branchName == "" {
					fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch switch --name <branch>")
					return 2
				}
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches/switch"
				payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{"name": branchName}, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch switch error: %v\n", err)
					return 1
				}
				_ = printJSON(payload)
				return 0
			case "compare":
				a := strings.TrimSpace(*branchA)
				b := strings.TrimSpace(*branchB)
				if a == "" && len(fs.Args()) >= 1 {
					a = strings.TrimSpace(fs.Args()[0])
				}
				if b == "" && len(fs.Args()) >= 2 {
					b = strings.TrimSpace(fs.Args()[1])
				}
				if a == "" || b == "" {
					fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch compare --a <branch-a> --b <branch-b>")
					return 2
				}
				v := url.Values{}
				v.Set("a", a)
				v.Set("b", b)
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches/compare?" + v.Encode()
				payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch compare error: %v\n", err)
					return 1
				}
				_ = printJSON(payload)
				return 0
			default:
				fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch [list|create|switch|compare]")
				return 2
			}
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation [list|search|active|new|save|load|branch ...] [--url ...] [--token ...]")
			return 2
		}

	case "codemap":
		fs := flag.NewFlagSet("remote codemap", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		format := fs.String("format", "json", "json|dot|svg|ascii")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/codemap"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote codemap error: %v\n", err)
			return 1
		}
		nodes, edges, err := decodeCodemapPayload(payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote codemap decode error: %v\n", err)
			return 1
		}
		f := strings.ToLower(strings.TrimSpace(*format))
		if jsonMode || f == "json" {
			_ = printJSON(map[string]any{"nodes": nodes, "edges": edges})
			return 0
		}
		switch f {
		case "dot":
			fmt.Println(graphToDOT(nodes, edges))
		case "svg":
			fmt.Println(graphToSVG(nodes, edges))
		default:
			for _, e := range edges {
				fmt.Printf("%s -> %s (%s)\n", e.From, e.To, e.Type)
			}
		}
		return 0

	case "tools":
		fs := flag.NewFlagSet("remote tools", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/tools"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote tools error: %v\n", err)
			return 1
		}
		_ = printJSON(payload)
		return 0

	case "skills":
		fs := flag.NewFlagSet("remote skills", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/skills"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote skills error: %v\n", err)
			return 1
		}
		_ = printJSON(payload)
		return 0

	case "prompt":
		action := "list"
		if len(args) >= 2 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		fs := flag.NewFlagSet("remote prompt", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		typ := fs.String("type", "system", "prompt type")
		task := fs.String("task", "auto", "prompt task")
		language := fs.String("language", "auto", "prompt language")
		query := fs.String("query", "", "user query")
		contextFiles := fs.String("context-files", "(none)", "context file summary")
		var varsRaw multiStringFlag
		fs.Var(&varsRaw, "var", "prompt var key=value (repeatable)")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}

		switch action {
		case "list":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/prompts"
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt list error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		case "render":
			extraVars, err := parsePromptVars(varsRaw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt var parse error: %v\n", err)
				return 2
			}
			payload, _, err := remoteJSONRequest(
				http.MethodPost,
				strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/prompts/render",
				*token,
				map[string]any{
					"type":          *typ,
					"task":          *task,
					"language":      *language,
					"query":         *query,
					"context_files": *contextFiles,
					"vars":          extraVars,
				},
				*timeout,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt render error: %v\n", err)
				return 1
			}
			_ = printJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote prompt [list|render --query ...]")
			return 2
		}

	case "start":
		fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		host := fs.String("host", eng.Config.Web.Host, "bind host")
		grpcPort := fs.Int("grpc-port", eng.Config.Remote.GRPCPort, "bind grpc port")
		port := fs.Int("ws-port", eng.Config.Remote.WSPort, "bind ws/http port")
		auth := fs.String("auth", eng.Config.Remote.Auth, "none|token")
		token := fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token (for auth=token)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		mode := strings.ToLower(strings.TrimSpace(*auth))
		if mode != "none" && mode != "token" {
			fmt.Fprintln(os.Stderr, "remote auth must be none|token")
			return 2
		}
		if mode == "token" && strings.TrimSpace(*token) == "" {
			fmt.Fprintln(os.Stderr, "remote token auth requires --token or DFMC_REMOTE_TOKEN")
			return 2
		}

		base := web.New(eng, *host, *port)
		handler := base.Handler()
		if mode == "token" {
			handler = bearerTokenMiddleware(handler, *token)
		}

		addr := fmt.Sprintf("%s:%d", *host, *port)
		server := &http.Server{
			Addr:    addr,
			Handler: handler,
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"status":    "starting",
				"mode":      "remote",
				"host":      *host,
				"grpc_port": *grpcPort,
				"ws_port":   *port,
				"auth":      mode,
				"healthz":   fmt.Sprintf("http://%s/healthz", addr),
				"base_api":  fmt.Sprintf("http://%s/api/v1", addr),
				"grpc":      "not_started",
			})
		} else {
			fmt.Printf("DFMC remote server listening on http://%s\n", addr)
			fmt.Printf("gRPC port (reserved): %d\n", *grpcPort)
			fmt.Printf("Auth: %s\n", mode)
		}

		if err := serveWithContext(ctx, server); err != nil {
			fmt.Fprintf(os.Stderr, "remote server error: %v\n", err)
			return 1
		}
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc remote [status|probe|events|ask|tool|tools|skill|skills|prompt|analyze|files|memory|conversation (list/search/active/new/save/load/branch)|codemap|start --host 127.0.0.1 --ws-port 7779 --auth none|token]")
		return 2
	}
}

type remoteProbeResult struct {
	Endpoint   string `json:"endpoint"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	DurationMs int64  `json:"duration_ms"`
	Body       string `json:"body,omitempty"`
	Error      string `json:"error,omitempty"`
}

func parseEndpointList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return []string{"/healthz"}
	}
	return out
}

func probeRemoteEndpoint(client *http.Client, baseURL, endpoint, token string) remoteProbeResult {
	start := time.Now()
	res := remoteProbeResult{Endpoint: endpoint}

	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + endpoint
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	res.StatusCode = resp.StatusCode
	res.Body = strings.TrimSpace(string(body))
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

type remoteChatEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
	Error string `json:"error,omitempty"`
}

func remoteAsk(baseURL, token, message string, timeout time.Duration, streamOutput bool) ([]remoteChatEvent, string, error) {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return nil, "", err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/chat"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("remote returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	events := make([]remoteChatEvent, 0, 64)
	var answer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ev, ok, err := parseSSEDataLine(scanner.Text())
		if err != nil {
			return events, answer.String(), err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
		switch ev.Type {
		case "delta":
			answer.WriteString(ev.Delta)
			if streamOutput {
				fmt.Print(ev.Delta)
			}
		case "error":
			msg := strings.TrimSpace(ev.Error)
			if msg == "" {
				msg = "remote stream error"
			}
			return events, answer.String(), fmt.Errorf(msg)
		case "done":
			return events, answer.String(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return events, answer.String(), err
	}
	return events, answer.String(), nil
}

func parseSSEDataLine(line string) (remoteChatEvent, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return remoteChatEvent{}, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return remoteChatEvent{}, false, nil
	}
	var ev remoteChatEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return remoteChatEvent{}, true, fmt.Errorf("invalid sse json: %w", err)
	}
	return ev, true, nil
}

func parseSSEJSONLine(line string) (map[string]any, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return nil, false, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return nil, true, fmt.Errorf("invalid sse json: %w", err)
	}
	return out, true, nil
}

type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func parseKeyValueParams(items []string) (map[string]any, error) {
	out := map[string]any{}
	for _, raw := range items {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value: %s", raw)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("empty key in param: %s", raw)
		}
		val, err := parseConfigValue(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func parsePromptVars(items []string) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range items {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value: %s", raw)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("empty key in var: %s", raw)
		}
		out[key] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func remoteJSONRequest(method, endpoint, token string, payload any, timeout time.Duration) (map[string]any, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	out := map[string]any{}
	if len(strings.TrimSpace(string(rawBody))) > 0 {
		if err := json.Unmarshal(rawBody, &out); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("invalid json response (%d): %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg, ok := out["error"]; ok {
			return out, resp.StatusCode, fmt.Errorf("remote returned %s: %v", resp.Status, msg)
		}
		return out, resp.StatusCode, fmt.Errorf("remote returned %s", resp.Status)
	}
	return out, resp.StatusCode, nil
}

func remoteCollectEvents(endpoint, token string, timeout time.Duration, maxEvents int) ([]map[string]any, error) {
	if maxEvents <= 0 {
		maxEvents = 100
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("remote returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	capHint := maxEvents
	if capHint > 32 {
		capHint = 32
	}
	events := make([]map[string]any, 0, capHint)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ev, ok, err := parseSSEJSONLine(scanner.Text())
		if err != nil {
			return events, err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
		if len(events) >= maxEvents {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func compactJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func decodeCodemapPayload(payload map[string]any) ([]codemap.Node, []codemap.Edge, error) {
	nodes := []codemap.Node{}
	edges := []codemap.Edge{}

	nodesRaw, ok := payload["nodes"]
	if !ok {
		return nodes, edges, fmt.Errorf("missing nodes field")
	}
	edgesRaw, ok := payload["edges"]
	if !ok {
		return nodes, edges, fmt.Errorf("missing edges field")
	}
	nb, err := json.Marshal(nodesRaw)
	if err != nil {
		return nodes, edges, err
	}
	if err := json.Unmarshal(nb, &nodes); err != nil {
		return nodes, edges, err
	}
	eb, err := json.Marshal(edgesRaw)
	if err != nil {
		return nodes, edges, err
	}
	if err := json.Unmarshal(eb, &edges); err != nil {
		return nodes, edges, err
	}
	return nodes, edges, nil
}

func remotePathEscape(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
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
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			add("project.root", "warn", "project root is empty")
		} else if st, err := os.Stat(root); err != nil {
			add("project.root", "fail", err.Error())
		} else if !st.IsDir() {
			add("project.root", "fail", "project root is not a directory")
		} else {
			add("project.root", "pass", root)
		}

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
		names := make([]string, 0, len(eng.Config.Providers.Profiles))
		for name := range eng.Config.Providers.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			prof := eng.Config.Providers.Profiles[name]
			configured := providerConfigured(name, prof)
			if configured {
				add("provider."+name+".configured", "pass", "credentials/endpoint present")
			} else {
				add("provider."+name+".configured", "warn", "missing api_key or required endpoint")
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

func serveWithContext(ctx context.Context, server *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func bearerTokenMiddleware(next http.Handler, token string) http.Handler {
	expected := "Bearer " + strings.TrimSpace(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			writeRemoteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		if got != expected {
			writeRemoteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeRemoteJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

type pluginInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Version   string `json:"version,omitempty"`
	Type      string `json:"type,omitempty"`
	Entry     string `json:"entry,omitempty"`
	Manifest  string `json:"manifest,omitempty"`
}

type pluginManifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Type        string `yaml:"type"`
	Entry       string `yaml:"entry"`
	Description string `yaml:"description"`
}

type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Source      string `json:"source"`
	Builtin     bool   `json:"builtin"`
	Prompt      string `json:"-"`
}

func runPlugin(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items := discoverPlugins(eng.Config.PluginDir(), eng.Config.Plugins.Enabled)
		if jsonMode {
			_ = printJSON(map[string]any{
				"directory": eng.Config.PluginDir(),
				"plugins":   items,
			})
			return 0
		}
		if len(items) == 0 {
			fmt.Printf("No plugins found in %s\n", eng.Config.PluginDir())
			return 0
		}
		for _, p := range items {
			state := "disabled"
			if p.Enabled {
				state = "enabled"
			}
			installed := "missing"
			if p.Installed {
				installed = "installed"
			}
			meta := ""
			if p.Version != "" {
				meta = " v" + p.Version
			}
			if p.Type != "" {
				meta += " (" + p.Type + ")"
			}
			fmt.Printf("- %s%s [%s, %s]\n", p.Name, meta, state, installed)
		}
		return 0

	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc plugin info <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		items := discoverPlugins(eng.Config.PluginDir(), eng.Config.Plugins.Enabled)
		for _, p := range items {
			if strings.EqualFold(p.Name, name) {
				if jsonMode {
					_ = printJSON(p)
				} else {
					fmt.Printf("Name:      %s\n", p.Name)
					fmt.Printf("Installed: %t\n", p.Installed)
					fmt.Printf("Enabled:   %t\n", p.Enabled)
					if strings.TrimSpace(p.Version) != "" {
						fmt.Printf("Version:   %s\n", p.Version)
					}
					if strings.TrimSpace(p.Type) != "" {
						fmt.Printf("Type:      %s\n", p.Type)
					}
					if strings.TrimSpace(p.Entry) != "" {
						fmt.Printf("Entry:     %s\n", p.Entry)
					}
					if strings.TrimSpace(p.Manifest) != "" {
						fmt.Printf("Manifest:  %s\n", p.Manifest)
					}
					if strings.TrimSpace(p.Path) != "" {
						fmt.Printf("Path:      %s\n", p.Path)
					}
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "plugin not found: %s\n", name)
		return 1

	case "enable", "disable":
		fs := flag.NewFlagSet("plugin "+args[0], flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintf(os.Stderr, "usage: dfmc plugin %s [--global] <name>\n", args[0])
			return 2
		}
		name := strings.TrimSpace(fs.Args()[0])
		enabled := args[0] == "enable"
		if err := updatePluginEnabled(ctx, eng, name, enabled, *global); err != nil {
			fmt.Fprintf(os.Stderr, "plugin %s failed: %v\n", args[0], err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status":  "ok",
				"plugin":  name,
				"enabled": enabled,
			})
		} else {
			fmt.Printf("Plugin %s: %s\n", name, map[bool]string{true: "enabled", false: "disabled"}[enabled])
		}
		return 0

	case "install":
		fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		nameOverride := fs.String("name", "", "plugin name override")
		enable := fs.Bool("enable", true, "enable after install")
		global := fs.Bool("global", false, "write enable state to ~/.dfmc/config.yaml")
		force := fs.Bool("force", false, "overwrite existing plugin target")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dfmc plugin install [--name X] [--enable] [--global] [--force] <source_path_or_url>")
			return 2
		}
		sourcePath := strings.TrimSpace(fs.Args()[0])
		installed, err := installPluginFile(eng.Config.PluginDir(), sourcePath, strings.TrimSpace(*nameOverride), *force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "plugin install failed: %v\n", err)
			return 1
		}
		if *enable {
			if err := updatePluginEnabled(ctx, eng, installed.Name, true, *global); err != nil {
				fmt.Fprintf(os.Stderr, "plugin installed but enable failed: %v\n", err)
				return 1
			}
			installed.Enabled = true
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status": "ok",
				"plugin": installed,
			})
		} else {
			fmt.Printf("Installed plugin %s at %s\n", installed.Name, installed.Path)
			if installed.Enabled {
				fmt.Println("Plugin enabled")
			}
		}
		return 0

	case "remove":
		fs := flag.NewFlagSet("plugin remove", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write disable state to ~/.dfmc/config.yaml")
		purge := fs.Bool("purge", true, "remove installed files")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dfmc plugin remove [--global] [--purge=true] <name>")
			return 2
		}
		name := strings.TrimSpace(fs.Args()[0])
		if err := updatePluginEnabled(ctx, eng, name, false, *global); err != nil {
			fmt.Fprintf(os.Stderr, "plugin disable failed: %v\n", err)
			return 1
		}
		removedPath := ""
		if *purge {
			path, err := removeInstalledPlugin(eng.Config.PluginDir(), name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "plugin remove failed: %v\n", err)
				return 1
			}
			removedPath = path
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status":  "ok",
				"plugin":  name,
				"removed": removedPath,
			})
		} else {
			fmt.Printf("Plugin %s disabled\n", name)
			if removedPath != "" {
				fmt.Printf("Removed %s\n", removedPath)
			}
		}
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc plugin [list|info <name>|enable <name>|disable <name>|install <path>|remove <name>]")
		return 2
	}
}

func runSkill(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items := discoverSkills(eng.Status().ProjectRoot)
		if jsonMode {
			_ = printJSON(map[string]any{"skills": items})
			return 0
		}
		for _, s := range items {
			label := s.Source
			if s.Builtin {
				label = "builtin"
			}
			fmt.Printf("- %s [%s]\n", s.Name, label)
		}
		return 0

	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc skill info <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		items := discoverSkills(eng.Status().ProjectRoot)
		for _, s := range items {
			if strings.EqualFold(s.Name, name) {
				if jsonMode {
					_ = printJSON(s)
				} else {
					fmt.Printf("Name:        %s\n", s.Name)
					fmt.Printf("Source:      %s\n", s.Source)
					fmt.Printf("Builtin:     %t\n", s.Builtin)
					if s.Description != "" {
						fmt.Printf("Description: %s\n", s.Description)
					}
					if s.Path != "" {
						fmt.Printf("Path:        %s\n", s.Path)
					}
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
		return 1

	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc skill run <name> [input]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		input := strings.TrimSpace(strings.Join(args[2:], " "))
		return runNamedSkill(ctx, eng, name, input, jsonMode)

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc skill [list|info <name>|run <name> [input]]")
		return 2
	}
}

func runSkillShortcut(ctx context.Context, eng *engine.Engine, name string, args []string, jsonMode bool) int {
	input := strings.TrimSpace(strings.Join(args, " "))
	if input == "" {
		input = "Analyze the current project."
	}
	return runNamedSkill(ctx, eng, name, input, jsonMode)
}

func runNamedSkill(ctx context.Context, eng *engine.Engine, name, input string, jsonMode bool) int {
	items := discoverSkills(eng.Status().ProjectRoot)
	for _, s := range items {
		if !strings.EqualFold(s.Name, name) {
			continue
		}
		prompt := buildSkillPrompt(s, input)
		answer, err := eng.Ask(ctx, prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill run failed: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"skill":  s.Name,
				"source": s.Source,
				"input":  input,
				"answer": answer,
			})
			return 0
		}
		fmt.Println(answer)
		return 0
	}
	fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
	return 1
}

func buildSkillPrompt(skill skillInfo, input string) string {
	p := strings.TrimSpace(skill.Prompt)
	if p == "" {
		p = input
	} else if strings.Contains(p, "{input}") {
		p = strings.ReplaceAll(p, "{input}", input)
	} else if strings.TrimSpace(input) != "" {
		p = p + "\n\nUser request:\n" + input
	}
	return p
}

func discoverPlugins(pluginDir string, enabled []string) []pluginInfo {
	seen := map[string]pluginInfo{}
	entries, err := os.ReadDir(pluginDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			base := strings.TrimSuffix(name, filepath.Ext(name))
			path := filepath.Join(pluginDir, name)
			if e.IsDir() {
				base = name
			} else {
				ext := strings.ToLower(filepath.Ext(name))
				if !pluginFileExtSupported(ext) {
					continue
				}
			}
			info := pluginInfo{
				Name:      base,
				Path:      path,
				Installed: true,
				Enabled:   containsCI(enabled, base),
			}
			if mf, mfPath, ok := readPluginManifest(path); ok {
				if strings.TrimSpace(mf.Name) != "" {
					info.Name = strings.TrimSpace(mf.Name)
				}
				info.Version = strings.TrimSpace(mf.Version)
				info.Type = strings.TrimSpace(mf.Type)
				info.Entry = strings.TrimSpace(mf.Entry)
				info.Manifest = mfPath
				info.Enabled = info.Enabled || containsCI(enabled, info.Name)
			}
			key := strings.ToLower(strings.TrimSpace(info.Name))
			if key == "" {
				continue
			}
			seen[key] = info
		}
	}

	for _, name := range enabled {
		n := strings.TrimSpace(name)
		key := strings.ToLower(n)
		if n == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = pluginInfo{
			Name:      n,
			Installed: false,
			Enabled:   true,
		}
	}

	out := make([]pluginInfo, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func installPluginFile(pluginDir, sourcePath, nameOverride string, force bool) (pluginInfo, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return pluginInfo{}, fmt.Errorf("source path is required")
	}

	resolvedSource, cleanup, err := resolvePluginSource(sourcePath)
	if err != nil {
		return pluginInfo{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	srcAbs, err := filepath.Abs(resolvedSource)
	if err != nil {
		return pluginInfo{}, err
	}
	srcAbs, archiveCleanup, err := expandPluginSourceIfArchive(srcAbs)
	if err != nil {
		return pluginInfo{}, err
	}
	if archiveCleanup != nil {
		defer archiveCleanup()
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return pluginInfo{}, err
	}
	if !srcInfo.IsDir() {
		if !pluginSourceFileExtSupported(strings.ToLower(filepath.Ext(srcAbs))) {
			return pluginInfo{}, fmt.Errorf("unsupported plugin file extension: %s", filepath.Ext(srcAbs))
		}
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return pluginInfo{}, err
	}
	pluginDirAbs, err := filepath.Abs(pluginDir)
	if err != nil {
		return pluginInfo{}, err
	}

	targetName := strings.TrimSpace(nameOverride)
	if targetName == "" {
		if srcInfo.IsDir() {
			if mf, _, ok := readPluginManifest(srcAbs); ok && strings.TrimSpace(mf.Name) != "" {
				targetName = strings.TrimSpace(mf.Name)
			}
		}
		if srcInfo.IsDir() {
			if targetName == "" {
				targetName = filepath.Base(srcAbs)
			}
		} else {
			targetName = strings.TrimSuffix(filepath.Base(srcAbs), filepath.Ext(srcAbs))
		}
	}
	targetName = sanitizePluginName(targetName)
	if targetName == "" {
		return pluginInfo{}, fmt.Errorf("invalid plugin name")
	}

	targetPath := filepath.Join(pluginDirAbs, targetName)
	if !srcInfo.IsDir() {
		ext := filepath.Ext(srcAbs)
		if ext != "" {
			targetPath = targetPath + ext
		}
	}
	targetPath, err = resolvePathWithinBase(pluginDirAbs, targetPath)
	if err != nil {
		return pluginInfo{}, err
	}

	if _, err := os.Stat(targetPath); err == nil {
		if !force {
			return pluginInfo{}, fmt.Errorf("target already exists: %s (use --force)", targetPath)
		}
		if err := removePathSafe(pluginDirAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
	}

	if srcInfo.IsDir() {
		if err := copyDir(srcAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
		if err := validatePluginManifestEntry(targetPath); err != nil {
			_ = removePathSafe(pluginDirAbs, targetPath)
			return pluginInfo{}, err
		}
	} else {
		if err := copyFile(srcAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
	}

	info := pluginInfo{
		Name:      targetName,
		Path:      targetPath,
		Installed: true,
		Enabled:   false,
	}
	if srcInfo.IsDir() {
		if mf, mfPath, ok := readPluginManifest(targetPath); ok {
			info.Version = strings.TrimSpace(mf.Version)
			info.Type = strings.TrimSpace(mf.Type)
			info.Entry = strings.TrimSpace(mf.Entry)
			info.Manifest = mfPath
			if strings.TrimSpace(mf.Name) != "" && strings.TrimSpace(nameOverride) == "" {
				info.Name = strings.TrimSpace(mf.Name)
			}
		}
	}
	return info, nil
}

func removeInstalledPlugin(pluginDir, name string) (string, error) {
	items := discoverPlugins(pluginDir, nil)
	for _, item := range items {
		if !item.Installed || strings.TrimSpace(item.Path) == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
		if !strings.EqualFold(item.Name, name) && !strings.EqualFold(base, name) {
			continue
		}
		pluginDirAbs, err := filepath.Abs(pluginDir)
		if err != nil {
			return "", err
		}
		targetPath, err := resolvePathWithinBase(pluginDirAbs, item.Path)
		if err != nil {
			return "", err
		}
		if err := removePathSafe(pluginDirAbs, targetPath); err != nil {
			return "", err
		}
		return targetPath, nil
	}
	return "", nil
}

func resolvePluginSource(source string) (resolved string, cleanup func(), err error) {
	if isHTTPPluginSource(source) {
		path, err := downloadPluginSource(source)
		if err != nil {
			return "", nil, err
		}
		return path, func() { _ = os.Remove(path) }, nil
	}
	return source, nil, nil
}

func isHTTPPluginSource(source string) bool {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	if u == nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return strings.TrimSpace(u.Host) != ""
	default:
		return false
	}
}

func downloadPluginSource(src string) (string, error) {
	resp, err := http.Get(src) //nolint:gosec // plugin install intentionally fetches user-provided URL.
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

	ext := ".plugin"
	if u, err := url.Parse(src); err == nil {
		if e := strings.TrimSpace(filepath.Ext(u.Path)); e != "" {
			ext = e
		}
	}
	tmp, err := os.CreateTemp("", "dfmc-plugin-*"+ext)
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func readPluginManifest(path string) (pluginManifest, string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return pluginManifest{}, "", false
	}
	if !info.IsDir() {
		return pluginManifest{}, "", false
	}

	candidates := []string{
		filepath.Join(path, "plugin.yaml"),
		filepath.Join(path, "plugin.yml"),
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var mf pluginManifest
		if err := yaml.Unmarshal(data, &mf); err != nil {
			continue
		}
		if strings.TrimSpace(mf.Name) == "" &&
			strings.TrimSpace(mf.Version) == "" &&
			strings.TrimSpace(mf.Type) == "" &&
			strings.TrimSpace(mf.Entry) == "" {
			continue
		}
		return mf, candidate, true
	}
	return pluginManifest{}, "", false
}

func sanitizePluginName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, ":", "_")
	return name
}

func pluginFileExtSupported(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".so", ".dll", ".dylib", ".wasm", ".js", ".mjs", ".py", ".sh":
		return true
	default:
		return false
	}
}

func pluginSourceFileExtSupported(ext string) bool {
	if pluginFileExtSupported(ext) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(ext), ".zip")
}

func expandPluginSourceIfArchive(path string) (string, func(), error) {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return path, nil, nil
	}
	tmpDir, err := os.MkdirTemp("", "dfmc-plugin-zip-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	if err := extractZipArchive(path, tmpDir); err != nil {
		cleanup()
		return "", nil, err
	}
	root := archiveRootDir(tmpDir)
	return root, cleanup, nil
}

func archiveRootDir(tmpDir string) string {
	entries, err := os.ReadDir(tmpDir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return tmpDir
	}
	return filepath.Join(tmpDir, entries[0].Name())
}

func extractZipArchive(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) == 0 {
		return fmt.Errorf("zip archive is empty")
	}
	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		if cleanName == "." || cleanName == "" {
			continue
		}
		if filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
			return fmt.Errorf("zip archive contains unsafe path: %s", f.Name)
		}
		targetPath, err := resolvePathWithinBase(dstDir, filepath.Join(dstDir, cleanName))
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip archive contains symlink entry: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if err := writeFileFromReader(targetPath, rc); err != nil {
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
	}
	return nil
}

func writeFileFromReader(path string, r io.Reader) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	return out.Close()
}

func validatePluginManifestEntry(pluginPath string) error {
	mf, _, ok := readPluginManifest(pluginPath)
	if !ok {
		return nil
	}
	entry := strings.TrimSpace(mf.Entry)
	if entry == "" {
		return nil
	}
	entryPath, err := resolvePathWithinBase(pluginPath, filepath.Join(pluginPath, entry))
	if err != nil {
		return fmt.Errorf("plugin manifest entry invalid: %w", err)
	}
	st, err := os.Stat(entryPath)
	if err != nil {
		return fmt.Errorf("plugin manifest entry not found: %s", entry)
	}
	if st.IsDir() {
		return fmt.Errorf("plugin manifest entry points to directory: %s", entry)
	}
	if ext := strings.ToLower(filepath.Ext(entryPath)); ext != "" && !pluginFileExtSupported(ext) {
		return fmt.Errorf("plugin manifest entry has unsupported extension: %s", ext)
	}
	return nil
}

func resolvePathWithinBase(base, target string) (string, error) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	targetAbs := target
	if !filepath.IsAbs(targetAbs) {
		targetAbs = filepath.Join(baseAbs, targetAbs)
	}
	targetAbs, err = filepath.Abs(targetAbs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	return targetAbs, nil
}

func removePathSafe(base, target string) error {
	targetAbs, err := resolvePathWithinBase(base, target)
	if err != nil {
		return err
	}
	if _, err := os.Stat(targetAbs); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(targetAbs)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func updatePluginEnabled(ctx context.Context, eng *engine.Engine, name string, enabled, global bool) error {
	_ = ctx
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	targetPath := projectConfigPath(cwd)
	if global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		return err
	}
	var list []string
	raw, _ := getConfigPath(currentMap, "plugins.enabled")
	switch arr := raw.(type) {
	case []any:
		for _, item := range arr {
			v := strings.TrimSpace(fmt.Sprint(item))
			if v != "" {
				list = append(list, v)
			}
		}
	case []string:
		for _, item := range arr {
			v := strings.TrimSpace(item)
			if v != "" {
				list = append(list, v)
			}
		}
	}

	if enabled {
		if !containsCI(list, name) {
			list = append(list, name)
		}
	} else {
		next := make([]string, 0, len(list))
		for _, item := range list {
			if !strings.EqualFold(item, name) {
				next = append(next, item)
			}
		}
		list = next
	}

	values := make([]any, 0, len(list))
	for _, item := range list {
		values = append(values, item)
	}
	if err := setConfigPath(currentMap, "plugins.enabled", values); err != nil {
		return err
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		return err
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		return fmt.Errorf("config reload failed, reverted: %w", err)
	}
	return nil
}

func discoverSkills(projectRoot string) []skillInfo {
	out := make([]skillInfo, 0, 16)
	seen := map[string]struct{}{}
	for _, item := range builtinSkills() {
		key := strings.ToLower(item.Name)
		seen[key] = struct{}{}
		out = append(out, item)
	}

	roots := []struct {
		Path   string
		Source string
	}{
		{Path: filepath.Join(projectRoot, ".dfmc", "skills"), Source: "project"},
		{Path: filepath.Join(config.UserConfigDir(), "skills"), Source: "global"},
	}

	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root.Path, "*.y*ml"))
		for _, path := range files {
			item := readSkillFile(path, root.Source)
			if strings.TrimSpace(item.Name) == "" {
				continue
			}
			key := strings.ToLower(item.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func builtinSkills() []skillInfo {
	return []skillInfo{
		{
			Name:        "review",
			Description: "Code review shortcut focused on bugs, regressions, and missing tests",
			Source:      "builtin",
			Builtin:     true,
			Prompt:      "Perform a strict code review. Prioritize bugs, risks, behavioral regressions, and missing tests.\n\nRequest:\n{input}",
		},
		{
			Name:        "explain",
			Description: "Explain code behavior and architecture clearly",
			Source:      "builtin",
			Builtin:     true,
			Prompt:      "Explain the target code in a clear and structured way, including key flows and important caveats.\n\nRequest:\n{input}",
		},
		{
			Name:        "refactor",
			Description: "Refactor planning and implementation guidance",
			Source:      "builtin",
			Builtin:     true,
			Prompt:      "Provide a safe refactor plan and concrete edits with minimal regression risk.\n\nRequest:\n{input}",
		},
		{
			Name:        "test",
			Description: "Generate or improve tests for target code",
			Source:      "builtin",
			Builtin:     true,
			Prompt:      "Create or improve automated tests for the target, including edge cases and failure paths.\n\nRequest:\n{input}",
		},
		{
			Name:        "doc",
			Description: "Generate concise and accurate documentation",
			Source:      "builtin",
			Builtin:     true,
			Prompt:      "Write practical documentation for the requested code or module.\n\nRequest:\n{input}",
		},
	}
}

func readSkillFile(path, source string) skillInfo {
	item := skillInfo{
		Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Path:    path,
		Source:  source,
		Builtin: false,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return item
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return item
	}
	if v, ok := raw["name"]; ok {
		name := strings.TrimSpace(fmt.Sprint(v))
		if name != "" {
			item.Name = name
		}
	}
	if v, ok := raw["description"]; ok {
		item.Description = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["prompt"]; ok {
		item.Prompt = strings.TrimSpace(fmt.Sprint(v))
	}
	if item.Prompt == "" {
		if v, ok := raw["template"]; ok {
			item.Prompt = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return item
}

func containsCI(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

type depStat struct {
	Module string `json:"module"`
	Count  int    `json:"count"`
}

func collectDependencyStats(eng *engine.Engine, limit int) []depStat {
	if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, e := range eng.CodeMap.Graph().Edges() {
		if e.Type != "imports" {
			continue
		}
		mod := strings.TrimPrefix(e.To, "module:")
		mod = strings.TrimSpace(mod)
		if mod == "" {
			continue
		}
		counts[mod]++
	}
	out := make([]depStat, 0, len(counts))
	for mod, count := range counts {
		out = append(out, depStat{Module: mod, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Module < out[j].Module
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func graphToDOT(nodes []codemap.Node, edges []codemap.Edge) string {
	var b strings.Builder
	b.WriteString("digraph DFMC {\n")
	for _, n := range nodes {
		label := n.Name
		if strings.TrimSpace(label) == "" {
			label = n.ID
		}
		if n.Kind != "" {
			label = label + "\\n(" + n.Kind + ")"
		}
		fmt.Fprintf(&b, "  \"%s\" [label=\"%s\"];\n", escapeDOT(n.ID), escapeDOT(label))
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  \"%s\" -> \"%s\" [label=\"%s\"];\n",
			escapeDOT(e.From), escapeDOT(e.To), escapeDOT(e.Type))
	}
	b.WriteString("}\n")
	return b.String()
}

func graphToSVG(nodes []codemap.Node, edges []codemap.Edge) string {
	const (
		width     = 1200.0
		height    = 800.0
		margin    = 90.0
		nodeR     = 14.0
		fontSize  = 12
		labelDy   = 24.0
		strokeW   = 1.2
		centerPad = 20.0
	)

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="800" viewBox="0 0 1200 800">` + "\n")
	b.WriteString(`  <defs><style>
    .edge { stroke: #64748b; stroke-width: 1.2; opacity: 0.8; }
    .node { fill: #0ea5e9; stroke: #075985; stroke-width: 1.2; }
    .label { fill: #0f172a; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; text-anchor: middle; }
    .kind { fill: #334155; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; text-anchor: middle; }
  </style></defs>` + "\n")
	b.WriteString(`  <rect x="0" y="0" width="1200" height="800" fill="#f8fafc"/>` + "\n")

	if len(nodes) == 0 {
		b.WriteString(`  <text x="600" y="400" class="label">No codemap nodes</text>` + "\n")
		b.WriteString(`</svg>` + "\n")
		return b.String()
	}

	type pt struct {
		x float64
		y float64
	}
	pos := make(map[string]pt, len(nodes))
	cx := width / 2
	cy := height / 2
	r := math.Min(width, height)/2 - margin
	if len(nodes) == 1 {
		pos[nodes[0].ID] = pt{x: cx, y: cy}
	} else {
		for i, n := range nodes {
			angle := (2 * math.Pi * float64(i) / float64(len(nodes))) - math.Pi/2
			x := cx + (r-centerPad)*math.Cos(angle)
			y := cy + (r-centerPad)*math.Sin(angle)
			pos[n.ID] = pt{x: x, y: y}
		}
	}

	for _, e := range edges {
		from, okFrom := pos[e.From]
		to, okTo := pos[e.To]
		if !okFrom || !okTo {
			continue
		}
		fmt.Fprintf(&b, `  <line class="edge" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`+"\n",
			from.x, from.y, to.x, to.y)
	}

	for _, n := range nodes {
		p := pos[n.ID]
		label := strings.TrimSpace(n.Name)
		if label == "" {
			label = n.ID
		}
		kind := strings.TrimSpace(n.Kind)
		fmt.Fprintf(&b, `  <circle class="node" cx="%.1f" cy="%.1f" r="%.1f"/>`+"\n", p.x, p.y, nodeR)
		fmt.Fprintf(&b, `  <text class="label" x="%.1f" y="%.1f">%s</text>`+"\n", p.x, p.y+labelDy, xmlEscape(label))
		if kind != "" {
			fmt.Fprintf(&b, `  <text class="kind" x="%.1f" y="%.1f">%s</text>`+"\n", p.x, p.y+labelDy+12, xmlEscape(kind))
		}
	}

	b.WriteString(`</svg>` + "\n")
	return b.String()
}

func xmlEscape(s string) string {
	return html.EscapeString(s)
}

func escapeDOT(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func runConfig(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"list"}
	}

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("config list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		raw := fs.Bool("raw", false, "show sensitive values")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		cfgMap, err := configToMap(eng.Config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config list error: %v\n", err)
			return 1
		}
		out := sanitizeConfigValue(cfgMap, "", !*raw)
		if jsonMode {
			_ = printJSON(out)
			return 0
		}
		data, err := yaml.Marshal(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config list error: %v\n", err)
			return 1
		}
		fmt.Print(string(data))
		return 0

	case "get":
		fs := flag.NewFlagSet("config get", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		raw := fs.Bool("raw", false, "show sensitive values")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dfmc config get [--raw] <path>")
			return 2
		}
		keyPath := strings.TrimSpace(fs.Args()[0])
		cfgMap, err := configToMap(eng.Config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config get error: %v\n", err)
			return 1
		}
		value, ok := getConfigPath(cfgMap, keyPath)
		if !ok {
			fmt.Fprintf(os.Stderr, "config path not found: %s\n", keyPath)
			return 1
		}
		out := sanitizeConfigValue(value, keyPath, !*raw)
		if jsonMode {
			_ = printJSON(map[string]any{
				"path":  keyPath,
				"value": out,
			})
			return 0
		}
		switch v := out.(type) {
		case string:
			fmt.Println(v)
		default:
			data, err := yaml.Marshal(v)
			if err != nil {
				fmt.Fprintf(os.Stderr, "config get error: %v\n", err)
				return 1
			}
			fmt.Print(string(data))
		}
		return 0

	case "set":
		fs := flag.NewFlagSet("config set", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc config set [--global] <path> <value>")
			return 2
		}
		keyPath := strings.TrimSpace(fs.Args()[0])
		rawValue := strings.Join(fs.Args()[1:], " ")
		parsedValue, err := parseConfigValue(rawValue)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config set parse error: %v\n", err)
			return 1
		}

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
			return 1
		}
		targetPath := projectConfigPath(cwd)
		if *global {
			targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
		}

		currentMap, err := loadConfigFileMap(targetPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
			return 1
		}
		if err := setConfigPath(currentMap, keyPath, parsedValue); err != nil {
			fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
			return 1
		}

		var oldData []byte
		oldData, _ = os.ReadFile(targetPath)
		if err := saveConfigFileMap(targetPath, currentMap); err != nil {
			fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
			return 1
		}
		if err := eng.ReloadConfig(cwd); err != nil {
			if len(oldData) == 0 {
				_ = os.Remove(targetPath)
			} else {
				_ = os.WriteFile(targetPath, oldData, 0o644)
			}
			fmt.Fprintf(os.Stderr, "config reload failed, reverted change: %v\n", err)
			return 1
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"status":      "ok",
				"path":        keyPath,
				"config_file": targetPath,
			})
			return 0
		}
		fmt.Printf("Updated %s in %s\n", keyPath, targetPath)
		return 0

	case "edit":
		fs := flag.NewFlagSet("config edit", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "edit ~/.dfmc/config.yaml")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config edit error: %v\n", err)
			return 1
		}
		targetPath := projectConfigPath(cwd)
		if *global {
			targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
		}

		if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
			if err := saveConfigFileMap(targetPath, map[string]any{}); err != nil {
				fmt.Fprintf(os.Stderr, "config edit error: %v\n", err)
				return 1
			}
		}

		editor := strings.TrimSpace(os.Getenv("VISUAL"))
		if editor == "" {
			editor = strings.TrimSpace(os.Getenv("EDITOR"))
		}
		if editor == "" {
			if runtime.GOOS == "windows" {
				editor = "notepad"
			} else {
				editor = "vi"
			}
		}
		editorParts := strings.Fields(editor)
		if len(editorParts) == 0 {
			fmt.Fprintln(os.Stderr, "config edit error: no editor configured")
			return 1
		}
		cmdArgs := append(editorParts[1:], targetPath)
		cmd := exec.Command(editorParts[0], cmdArgs...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "config edit error: %v\n", err)
			return 1
		}

		if err := eng.ReloadConfig(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "config reload failed after edit: %v\n", err)
			return 1
		}
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc config [list|get|set|edit]")
		return 2
	}
}

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
			_ = printJSON(map[string]any{"prompts": items})
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
		query := fs.String("query", "", "user request/query")
		contextFiles := fs.String("context-files", "(none)", "context file summary to inject")
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

		vars := map[string]string{
			"project_root":  projectRoot,
			"task":          resolvedTask,
			"language":      resolvedLanguage,
			"user_query":    strings.TrimSpace(*query),
			"context_files": strings.TrimSpace(*contextFiles),
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
			Vars:     vars,
		})
		if jsonMode {
			_ = printJSON(map[string]any{
				"type":     strings.TrimSpace(*typ),
				"task":     resolvedTask,
				"language": resolvedLanguage,
				"vars":     vars,
				"prompt":   out,
			})
			return 0
		}
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc prompt [list|render --task auto --language auto --query \"...\"]")
		return 2
	}
}

func projectConfigPath(cwd string) string {
	root := config.FindProjectRoot(cwd)
	if strings.TrimSpace(root) == "" {
		root = cwd
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml")
}

func configToMap(cfg *config.Config) (map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func loadConfigFileMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	out := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return out, nil
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveConfigFileMap(path string, data map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	blob, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o644)
}

func parseConfigValue(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	var v any
	if err := yaml.Unmarshal([]byte(s), &v); err == nil {
		return v, nil
	}

	if b, err := strconv.ParseBool(s); err == nil {
		return b, nil
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	return raw, nil
}

func getConfigPath(root map[string]any, path string) (any, bool) {
	parts := splitConfigPath(path)
	if len(parts) == 0 {
		return root, true
	}
	var current any = root
	for _, part := range parts {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			current = node[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func setConfigPath(root map[string]any, path string, value any) error {
	parts := splitConfigPath(path)
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}
	current := root
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, exists := current[part]
		if !exists {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("path segment %q is not an object", strings.Join(parts[:i+1], "."))
		}
		current = nextMap
	}
	current[parts[len(parts)-1]] = value
	return nil
}

func splitConfigPath(path string) []string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func sanitizeConfigValue(value any, path string, enabled bool) any {
	if !enabled {
		return value
	}
	if isSensitivePath(path) {
		return "***REDACTED***"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			nextPath := k
			if path != "" {
				nextPath = path + "." + k
			}
			out[k] = sanitizeConfigValue(inner, nextPath, enabled)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			nextPath := strconv.Itoa(i)
			if path != "" {
				nextPath = path + "." + nextPath
			}
			out[i] = sanitizeConfigValue(inner, nextPath, enabled)
		}
		return out
	default:
		return v
	}
}

func isSensitivePath(path string) bool {
	if path == "" {
		return false
	}
	parts := splitConfigPath(path)
	if len(parts) == 0 {
		return false
	}
	key := strings.ToLower(parts[len(parts)-1])
	switch key {
	case "api_key", "apikey", "secret", "secret_key", "client_secret", "password", "passphrase", "token":
		return true
	}
	return strings.HasSuffix(key, "_token")
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
  conversation Conversation management (list/search/load/save/undo/branch)
  memory      Memory management
  config      Configuration management (list/get/set/edit)
  prompt      Prompt library management (list/render)
  plugin      Plugin management (list/info/install/remove/enable/disable)
  skill       Skill management (list/info/run)
  serve       Start Web API server
  remote      Remote control server (status/probe/events/ask/tool/tools/skill/skills/prompt/analyze/files/memory/conversation+branch/codemap/start)
  doctor      Environment and config health checks
  completion  Generate shell completion scripts
  man         Generate command manual page
  review      Code review shortcut
  explain     Explain code shortcut
  refactor    Refactor code shortcut
  test        Test generation shortcut
  doc         Documentation shortcut
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

func executableSize() int64 {
	exe, err := os.Executable()
	if err != nil {
		return 0
	}
	st, err := os.Stat(exe)
	if err != nil {
		return 0
	}
	return st.Size()
}

func tryOpenBrowser(targetURL string) error {
	name, args, ok := browserCommandForOS(runtime.GOOS, targetURL)
	if !ok {
		return fmt.Errorf("unsupported platform for browser open: %s", runtime.GOOS)
	}
	cmd := exec.Command(name, args...)
	return cmd.Start()
}

func browserCommandForOS(goos, targetURL string) (name string, args []string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return "cmd", []string{"/c", "start", "", targetURL}, true
	case "darwin":
		return "open", []string{targetURL}, true
	case "linux":
		return "xdg-open", []string{targetURL}, true
	default:
		return "", nil, false
	}
}
