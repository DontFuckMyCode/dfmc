// Package cli is the CLI surface for dfmc. This file holds only the
// Run() dispatcher and parseGlobalFlags(). Every subcommand
// implementation lives in a sibling file grouped by concern:
//
//   - cli_admin.go        — version, status, init, completion, man, doctor, config
//   - cli_ask_chat.go     — ask, chat, tui, runChatSlash and diff helpers
//   - cli_analysis.go     — analyze, map, tool, memory, scan, conversation, prompt, context
//   - cli_remote.go       — serve and remote (client against a running serve)
//   - cli_plugin_skill.go — plugin and skill subcommands, installers, discovery
//   - cli_output.go       — printHelp, printCommandHelp, renderCLIHelp, printJSON
//   - cli_utils.go        — small stateless helpers (tier parsing, project brief, etc.)
//
// When adding a new subcommand, wire it into the switch in Run() and
// put its implementation in whichever sibling file matches its concern.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
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

	// Register a stdin-backed approver so any agent-driven subcommand
	// (ask, chat, review, explain, refactor, etc.) that hits a gated tool
	// can prompt the user. TUI runs overwrite this with its own modal
	// approver when `dfmc tui` launches — see ui/tui/approver.go.
	eng.SetApprover(newStdinApprover())

	if len(rest) == 0 {
		printHelp()
		return 0
	}

	cmd := rest[0]
	cmdArgs := rest[1:]

	switch cmd {
	case "help", "-h", "--help":
		if len(cmdArgs) > 0 {
			printCommandHelp(cmdArgs[0])
			return 0
		}
		printHelp()
		return 0
	case "status":
		return runStatus(eng, version, cmdArgs, opts.JSON)
	case "version":
		return runVersion(eng, version, cmdArgs, opts.JSON)
	case "init":
		return runInit(opts.JSON, opts.Project)
	case "ask":
		return runAsk(ctx, eng, cmdArgs, opts.JSON)
	case "chat":
		return runChat(ctx, eng, cmdArgs, opts.JSON)
	case "tui":
		return runTUI(ctx, eng, cmdArgs, opts.JSON)
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
	case "context":
		return runContext(ctx, eng, cmdArgs, opts.JSON)
	case "prompt":
		return runPrompt(ctx, eng, cmdArgs, opts.JSON)
	case "magicdoc":
		return runMagicDoc(ctx, eng, cmdArgs, opts.JSON)
	case "plugin":
		return runPlugin(ctx, eng, cmdArgs, opts.JSON)
	case "skill":
		return runSkill(ctx, eng, cmdArgs, opts.JSON)
	case "review", "explain", "refactor", "debug", "test", "doc",
		"generate", "audit", "onboard":
		return runSkillShortcut(ctx, eng, cmd, cmdArgs, opts.JSON)
	case "remote":
		return runRemote(ctx, eng, cmdArgs, opts.JSON)
	case "provider":
		return runProviderCLI(eng, cmdArgs, opts.JSON)
	case "model":
		return runModelCLI(eng, cmdArgs, opts.JSON)
	case "providers":
		return runProvidersList(eng, opts.JSON)
	case "doctor":
		return runDoctor(ctx, eng, cmdArgs, opts.JSON)
	case "hooks":
		return runHooksCLI(eng, cmdArgs, opts.JSON)
	case "approvals", "approve", "permissions":
		return runApprovalsCLI(eng, cmdArgs, opts.JSON)
	case "mcp":
		return runMCP(ctx, eng, cmdArgs, version)
	case "update":
		return runUpdate(ctx, cmdArgs, version, opts.JSON)
	case "completion":
		return runCompletion(cmdArgs, opts.JSON)
	case "man":
		return runMan(cmdArgs, opts.JSON)
	default:
		if strings.HasPrefix(cmd, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", cmd)
			return 2
		}
		// Typo guard: if the first token looks like a misspelled verb
		// (a single short word that's close to a known command), warn
		// on stderr before routing to ask. Without this guard `dfmc
		// docter` silently becomes an LLM question about 'docter',
		// which is wasteful and confusing.
		if looksLikeCommandTypo(cmd, rest) {
			if suggestion := suggestCLICommand(cmd); suggestion != "" {
				fmt.Fprintf(os.Stderr, "unknown command %q — did you mean %q?\n", cmd, suggestion)
				fmt.Fprintln(os.Stderr, "Run `dfmc help` for the full command list, or quote the text to force a question: `dfmc ask "+strconv.Quote(cmd)+"`.")
				return 2
			}
		}
		// If command is not known and doesn't look like a typo, treat it
		// as a one-shot question.
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
