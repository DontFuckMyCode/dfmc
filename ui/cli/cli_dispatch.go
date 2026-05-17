package cli

import (
	"context"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type commandHandler func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, version string) int

func dispatchCommand(ctx context.Context, eng *engine.Engine, cmd string, args []string, opts globalOptions, version string) (int, bool) {
	if isSkillShortcut(cmd) {
		return runSkillShortcut(ctx, eng, cmd, args, opts.JSON), true
	}
	handler, ok := commandHandlerRegistry()[cmd]
	if !ok {
		return 0, false
	}
	return handler(ctx, eng, args, opts, version), true
}

var skillShortcutCommands = []string{
	"review",
	"explain",
	"refactor",
	"debug",
	"test",
	"doc",
	"generate",
	"audit",
	"onboard",
}

func isSkillShortcut(cmd string) bool {
	for _, shortcut := range skillShortcutCommands {
		if cmd == shortcut {
			return true
		}
	}
	return false
}

func cliDispatchCommandNames() []string {
	seen := map[string]struct{}{}
	for name := range commandHandlerRegistry() {
		if strings.HasPrefix(name, "-") {
			continue
		}
		seen[name] = struct{}{}
	}
	for _, name := range skillShortcutCommands {
		seen[name] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func handleHelp(_ context.Context, _ *engine.Engine, args []string, _ globalOptions, _ string) int {
	if len(args) > 0 {
		printCommandHelp(args[0])
		return 0
	}
	printHelp()
	return 0
}

func commandHandlerRegistry() map[string]commandHandler {
	return map[string]commandHandler{
		"help":   handleHelp,
		"-h":     handleHelp,
		"--help": handleHelp,
		"status": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, version string) int {
			return runStatus(eng, version, args, opts.JSON)
		},
		"version": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, version string) int {
			return runVersion(eng, version, args, opts.JSON)
		},
		"init": func(_ context.Context, _ *engine.Engine, _ []string, opts globalOptions, _ string) int {
			return runInit(opts.JSON, opts.Project)
		},
		"ask": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runAsk(ctx, eng, args, opts.JSON)
		},
		"chat": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runChat(ctx, eng, args, opts.JSON)
		},
		"tui": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runTUI(ctx, eng, args, opts.JSON)
		},
		"analyze": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runAnalyze(ctx, eng, args, opts.JSON)
		},
		"map": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runMap(ctx, eng, args, opts.JSON)
		},
		"tool": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runTool(ctx, eng, args, opts.JSON)
		},
		"scan": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runScan(ctx, eng, args, opts.JSON)
		},
		"memory": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runMemory(ctx, eng, args, opts.JSON)
		},
		"conversation": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runConversation(ctx, eng, args, opts.JSON)
		},
		"conv": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runConversation(ctx, eng, args, opts.JSON)
		},
		"serve": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runServe(ctx, eng, args, opts.JSON)
		},
		"config": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runConfig(ctx, eng, args, opts.JSON)
		},
		"context": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runContext(ctx, eng, args, opts.JSON)
		},
		"prompt": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runPrompt(ctx, eng, args, opts.JSON)
		},
		"magicdoc": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runMagicDoc(ctx, eng, args, opts.JSON)
		},
		"plugin": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runPlugin(ctx, eng, args, opts.JSON)
		},
		"skill": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runSkill(ctx, eng, args, opts.JSON)
		},
		"agents": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runAgents(ctx, eng, args, opts.JSON)
		},
		"agent": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runAgents(ctx, eng, args, opts.JSON)
		},
		"remote": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runRemote(ctx, eng, args, opts.JSON)
		},
		"drive": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runDrive(ctx, eng, args, opts.JSON)
		},
		"provider": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runProviderCLI(eng, args, opts.JSON)
		},
		"model": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runModelCLI(eng, args, opts.JSON)
		},
		"providers": func(_ context.Context, eng *engine.Engine, _ []string, opts globalOptions, _ string) int {
			return runProvidersList(eng, opts.JSON)
		},
		"doctor": func(ctx context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runDoctor(ctx, eng, args, opts.JSON)
		},
		"hooks": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runHooksCLI(eng, args, opts.JSON)
		},
		"approvals": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runApprovalsCLI(eng, args, opts.JSON)
		},
		"approve": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runApprovalsCLI(eng, args, opts.JSON)
		},
		"permissions": func(_ context.Context, eng *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runApprovalsCLI(eng, args, opts.JSON)
		},
		"mcp": func(ctx context.Context, eng *engine.Engine, args []string, _ globalOptions, version string) int {
			return runMCP(ctx, eng, args, version)
		},
		"update": func(ctx context.Context, _ *engine.Engine, args []string, opts globalOptions, version string) int {
			return runUpdate(ctx, args, version, opts.JSON)
		},
		"completion": func(_ context.Context, _ *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runCompletion(args, opts.JSON)
		},
		"man": func(_ context.Context, _ *engine.Engine, args []string, opts globalOptions, _ string) int {
			return runMan(args, opts.JSON)
		},
	}
}
