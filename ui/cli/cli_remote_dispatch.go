package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type remoteCommandHandler func(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int

func dispatchRemoteCommand(ctx context.Context, eng *engine.Engine, cmd string, args []string, jsonMode bool) (int, bool) {
	handler, ok := remoteCommandRegistry()[cmd]
	if !ok {
		return 0, false
	}
	return handler(ctx, eng, args, jsonMode), true
}

func remoteCommandNames() []string {
	commands := remoteCommandRegistry()
	out := make([]string, 0, len(commands))
	for name := range commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func remoteUsage() string {
	return fmt.Sprintf(
		"usage: dfmc remote [%s] [args...]",
		strings.Join(remoteCommandNames(), "|"),
	)
}

func remoteCommandRegistry() map[string]remoteCommandHandler {
	return map[string]remoteCommandHandler{
		"status": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteStatus(eng, args, jsonMode)
		},
		"probe": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteProbe(eng, args, jsonMode)
		},
		"events": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteEvents(eng, args, jsonMode)
		},
		"ask": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteAskCmd(eng, args, jsonMode)
		},
		"tool": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteTool(eng, args, jsonMode)
		},
		"tools": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteTools(eng, args, jsonMode)
		},
		"skill": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteSkill(eng, args, jsonMode)
		},
		"skills": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteSkills(eng, args, jsonMode)
		},
		"agents": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteAgents(eng, args, jsonMode)
		},
		"prompt": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remotePrompt(eng, args, jsonMode)
		},
		"magicdoc": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteMagicdoc(eng, args, jsonMode)
		},
		"analyze": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteAnalyze(eng, args, jsonMode)
		},
		"context": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteContext(eng, args, jsonMode)
		},
		"files": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteFiles(eng, args, jsonMode)
		},
		"memory": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteMemory(eng, args, jsonMode)
		},
		"conversation": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteConversation(eng, args, jsonMode)
		},
		"codemap": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return remoteCodemap(eng, args, jsonMode)
		},
		"start": remoteStart,
		"drive": func(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
			return runRemoteDrive(eng, args, jsonMode)
		},
	}
}
