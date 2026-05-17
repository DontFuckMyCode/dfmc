package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

type remoteDriveCommandHandler func(defaultURL string, args []string, jsonMode bool) int

func dispatchRemoteDriveCommand(defaultURL, cmd string, args []string, jsonMode bool) (int, bool) {
	handler, ok := remoteDriveCommandRegistry()[cmd]
	if !ok {
		return 0, false
	}
	return handler(defaultURL, args, jsonMode), true
}

func remoteDriveCommandNames() []string {
	commands := remoteDriveCommandRegistry()
	out := make([]string, 0, len(commands))
	for name := range commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func remoteDriveUsage() string {
	return fmt.Sprintf(
		`usage: dfmc remote drive ["<task>" | %s]`,
		strings.Join(remoteDriveCommandNames(), " | "),
	)
}

func remoteDriveCommandRegistry() map[string]remoteDriveCommandHandler {
	return map[string]remoteDriveCommandHandler{
		"list": func(defaultURL string, args []string, jsonMode bool) int {
			return remoteDriveList(defaultURL, args, jsonMode)
		},
		"show":   remoteDriveIDCommand("show", remoteDriveShow),
		"resume": remoteDriveIDCommand("resume", remoteDriveResume),
		"delete": remoteDriveIDCommand("delete", remoteDriveDelete),
		"stop":   remoteDriveIDCommand("stop", remoteDriveStop),
		"cancel": remoteDriveIDCommand("stop", remoteDriveStop),
		"active": func(defaultURL string, args []string, jsonMode bool) int {
			return remoteDriveActive(defaultURL, args, jsonMode)
		},
	}
}

func remoteDriveIDCommand(name string, fn func(defaultURL, id string, args []string, jsonMode bool) int) remoteDriveCommandHandler {
	return func(defaultURL string, args []string, jsonMode bool) int {
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "usage: dfmc remote drive %s <id>\n", name)
			return 2
		}
		return fn(defaultURL, args[0], args[1:], jsonMode)
	}
}
