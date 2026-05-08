package tui

// intent_chat_args.go — slash-command argument parsers (parse*ChatArgs)
// that turn /ls, /read, /grep, /run lines into tool params maps.
// Sibling to intent.go which keeps the heuristic auto-tool detection
// matchers (looksLikeActionRequest, autoToolIntentFromQuestion, the
// extract* family, detectReferencedFile, etc.).

import (
	"fmt"
	"strconv"
	"strings"
)

func parseListDirChatArgs(args []string) (map[string]any, error) {
	params := map[string]any{
		"path":        ".",
		"max_entries": 120,
	}
	pathSet := false
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		switch {
		case arg == "-r" || arg == "--recursive":
			params["recursive"] = true
		case arg == "--max":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing value for --max")
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil {
				return nil, fmt.Errorf("invalid --max value")
			}
			params["max_entries"] = n
			i++
		case strings.HasPrefix(strings.ToLower(arg), "--max="):
			raw := strings.TrimSpace(strings.SplitN(arg, "=", 2)[1])
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid --max value")
			}
			params["max_entries"] = n
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag")
		default:
			if !pathSet {
				params["path"] = arg
				pathSet = true
			}
		}
	}
	return params, nil
}

func parseReadFileChatArgs(args []string) (map[string]any, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, fmt.Errorf("path required")
	}
	params := map[string]any{
		"path":       strings.TrimSpace(args[0]),
		"line_start": 1,
		"line_end":   200,
	}
	if len(args) >= 2 {
		start, err := strconv.Atoi(strings.TrimSpace(args[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid line_start")
		}
		params["line_start"] = start
		if len(args) >= 3 {
			end, err := strconv.Atoi(strings.TrimSpace(args[2]))
			if err != nil {
				return nil, fmt.Errorf("invalid line_end")
			}
			params["line_end"] = end
		} else {
			params["line_end"] = start + 199
		}
	}
	return params, nil
}

func parseGrepChatArgs(args []string) (map[string]any, error) {
	pattern := strings.TrimSpace(strings.Join(args, " "))
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	return map[string]any{
		"pattern":     pattern,
		"max_results": 80,
	}, nil
}

func parseRunCommandChatArgs(args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("command required")
	}
	command := strings.TrimSpace(args[0])
	if command == "" {
		return nil, fmt.Errorf("command required")
	}
	params := map[string]any{
		"command": command,
		"dir":     ".",
	}
	if len(args) > 1 {
		rest := strings.TrimSpace(strings.Join(args[1:], " "))
		if rest != "" {
			params["args"] = rest
		}
	}
	return params, nil
}
