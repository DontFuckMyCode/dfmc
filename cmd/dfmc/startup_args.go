package main

import "strings"

type startupArgs struct {
	dataDir       string
	telegramToken string
	sessionName   string
}

// parseStartupArgs scans only the flags needed before config.Load or
// engine.Init. Full CLI parsing still lives in ui/cli; this pre-scan
// exists for boot-time overrides that must be known earlier.
func parseStartupArgs(args []string) startupArgs {
	return startupArgs{
		dataDir:       extractFlagValue(args, "--data-dir"),
		telegramToken: extractFlagValue(args, "--telegram-token"),
		sessionName:   extractFlagValue(args, "--session-name"),
	}
}

func extractFlagValue(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
	}
	return ""
}

// extractDataDir scans args for --data-dir before flag parsing and
// returns the value so LoadOptions can be populated before config.Load.
// This lets the user point multiple DFMC instances at different data
// dirs without file-lock contention on dfmc.db.
func extractDataDir(args []string) string {
	return parseStartupArgs(args).dataDir
}

// extractTelegramToken scans args for --telegram-token before flag parsing.
func extractTelegramToken(args []string) string {
	return parseStartupArgs(args).telegramToken
}

// extractSessionName returns the --session-name value.
func extractSessionName(args []string) string {
	return parseStartupArgs(args).sessionName
}
