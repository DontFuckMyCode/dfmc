package tools

// command_validate.go — security gates that decide whether a
// run_command invocation is allowed to reach exec.CommandContext.
// Companion siblings:
//
//   - command.go              RunCommandTool.Execute + result shaping
//   - command_args.go         argv tokenization + timeout resolution
//   - command_recovery.go     hint generators for shell-line packing
//
// Layered checks run cheapest → most-permissive: binary blocklist,
// then structured arg-sequence blocklist for legitimate-but-dangerous
// invocations (git reset --hard, etc.), then user-configured
// substring patterns from .dfmc/config.yaml. Companion detectors
// surface the shell-metacharacter / shell-interpreter / script-runner
// inline-eval footguns so Execute can emit a self-teaching error.

import (
	"fmt"
	"path/filepath"
	"strings"
)

var scriptRunnerEvalFlags = map[string]string{
	"node":    "-e",
	"nodejs":  "-e",
	"python":  "-c",
	"python2": "-c",
	"python3": "-c",
	"ruby":    "-e",
	"php":     "-r",
	"perl":    "-e",
}

// ensureCommandAllowed gates execution against the default block list
// plus any user-configured patterns. The checks are ordered from
// cheapest/most-specific to most-permissive:
//
//  1. Binary-name block: strips path + .exe and matches against a
//     fixed list of destructive or privilege-escalating binaries. This
//     catches rm, mkfs, sudo, shutdown, etc. regardless of how they
//     were invoked.
//  2. Structured arg-sequence block: for binaries that ARE legitimate
//     (git, dd) but have specific flag combinations that are
//     destructive. Token-based, so `echo "git reset --hard"` does not
//     false-positive.
//  3. User-configured patterns: kept as substring matches over the
//     joined command+args for back-compat with .dfmc/config.yaml
//     entries. Users opt into this shape knowing it matches substrings.
//
// Substring matching over the *defaults* was the old behaviour and led
// to false positives like blocking `go build -o format ./...` (pattern
// "format " matches inside the args) and `echo "mkfs is cool"`
// (pattern "mkfs" matches the echoed string). The token-based checks
// below avoid that class of bug entirely.
func ensureCommandAllowed(command string, args []string, userBlocked []string) error {
	binary := canonicalCommandBinary(command)
	if isBlockedBinary(binary) {
		return fmt.Errorf("command blocked by policy: %s", command)
	}
	if err := checkBlockedArgSequences(binary, args); err != nil {
		return err
	}
	if len(userBlocked) > 0 {
		full := strings.ToLower(strings.TrimSpace(strings.Join(append([]string{command}, args...), " ")))
		for _, item := range userBlocked {
			pattern := strings.ToLower(strings.TrimSpace(item))
			if pattern == "" {
				continue
			}
			if strings.Contains(full, pattern) {
				return fmt.Errorf("command blocked by policy: %s", item)
			}
		}
	}
	return nil
}

// canonicalCommandBinary extracts a lower-case, .exe-stripped binary
// name from a command string. Doing this once up front keeps the
// block checks simple and platform-symmetric (rm.exe and rm both look
// like "rm").
func canonicalCommandBinary(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		// filepath.Base("") returns "." which is not what we want —
		// the caller almost certainly has an upstream empty-command
		// guard, but keep this defensive.
		return ""
	}
	normalized := strings.ReplaceAll(trimmed, `\`, `/`)
	binary := strings.ToLower(filepath.Base(normalized))
	return strings.TrimSuffix(binary, ".exe")
}

// isBlockedBinary reports whether a canonicalised binary name is on
// the "never run this directly" list. Grouped by rationale so future
// maintainers can reason about whether to add entries.
func isBlockedBinary(binary string) bool {
	switch binary {
	// Destructive filesystem / disk operations.
	case "rm", "del", "rmdir", "format", "mkfs", "diskpart", "dd":
		return true
	// Privilege escalation — running these lifts the agent out of the
	// user's normal permissions, which defeats the purpose of a
	// sandboxed tool.
	case "sudo", "doas", "su", "runas", "pkexec":
		return true
	// System-level control. Even a transient invocation like `shutdown
	// -r now` can kill an unsaved session.
	case "shutdown", "reboot", "halt", "poweroff", "init", "telinit":
		return true
	// Broad process termination. `killall sshd` is the shape we want
	// to refuse; narrow-scope `kill PID` is allowed because it's the
	// normal way to signal a specific process.
	case "killall", "pkill":
		return true
	}
	return false
}

// checkBlockedArgSequences catches destructive invocations of
// legitimate binaries. Token-based to avoid the substring false
// positives of the old pattern-list approach.
func checkBlockedArgSequences(binary string, args []string) error {
	switch binary {
	case "git":
		// git reset --hard, git clean -fd/-fdx, git checkout --,
		// git restore --source, git push --force / --force-with-lease.
		if hasArgSequence(args, "reset", "--hard") ||
			hasArgSequence(args, "clean", "-fd") ||
			hasArgSequence(args, "clean", "-fdx") ||
			hasArgSequence(args, "clean", "-fx") ||
			hasArgSequence(args, "checkout", "--") ||
			hasArgSequence(args, "restore", "--source") ||
			hasArgSequence(args, "push", "--force") ||
			hasArgSequence(args, "push", "-f") ||
			hasArgSequence(args, "push", "--force-with-lease") {
			return fmt.Errorf("command blocked by policy: git %s", strings.Join(args, " "))
		}
	}
	return nil
}

func hasArgSequence(args []string, seq ...string) bool {
	if len(seq) == 0 || len(args) < len(seq) {
		return false
	}
	for i := range len(args) - len(seq) + 1 {
		match := true
		for j := range seq {
			if !strings.EqualFold(strings.TrimSpace(args[i+j]), seq[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func commandWorkingDir(projectRoot, raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" || dir == "." {
		return projectRoot, nil
	}
	return EnsureWithinRoot(projectRoot, dir)
}

func looksLikePath(command string) bool {
	command = strings.TrimSpace(command)
	return strings.Contains(command, "/") || strings.Contains(command, "\\") || strings.HasPrefix(command, ".")
}

// detectShellMetacharacter scans `command` for syntax that only a shell
// interpreter understands. We don't run a shell, so finding any of these
// inside the binary slot is a sign the model packed a whole shell line
// into `command` (e.g. `cd /repo && go build ./...`). Returns the first
// offending token for use in the error message; empty string means the
// command looks like a plain binary invocation.
//
// We deliberately scan only `command`, not `args` — putting `>` or `&&`
// in args is fine because the binary just sees them as positional
// arguments. The footgun is exclusively when shell syntax shows up in
// the slot that becomes argv[0].
func detectShellMetacharacter(command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	// Multi-char operators first so e.g. `&&` doesn't get reported as `&`.
	for _, op := range []string{"&&", "||", ">>", "2>&1", "2>", "<<"} {
		if strings.Contains(cmd, op) {
			return op
		}
	}
	// Single-char shell operators. `|` last so we don't false-positive on
	// the rare absolute path containing `|` (Windows reserves it).
	for _, op := range []string{";", "|", ">", "<", "`", "$("} {
		if strings.Contains(cmd, op) {
			return op
		}
	}
	if hasStandaloneShellAmpersand(cmd) {
		return "&"
	}
	// `cd ` at the start is the other classic LLM tell — the model is
	// trying to chdir-then-run inside one command. Treat it as shell-y.
	if strings.HasPrefix(strings.ToLower(cmd), "cd ") {
		return "cd "
	}
	return ""
}

func hasStandaloneShellAmpersand(cmd string) bool {
	for i, r := range cmd {
		if r != '&' {
			continue
		}
		prevWS := i == 0 || isShellWhitespace(rune(cmd[i-1]))
		nextWS := i == len(cmd)-1 || isShellWhitespace(rune(cmd[i+1]))
		if prevWS || nextWS {
			return true
		}
	}
	return false
}

func detectShellSubstitutionArg(args []string) (string, string) {
	for _, arg := range args {
		switch {
		case strings.Contains(arg, "$("):
			return "$(", arg
		case strings.Contains(arg, "`"):
			return "`", arg
		}
	}
	return "", ""
}

func isShellWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func isBlockedShellInterpreter(command string) bool {
	binary := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	switch binary {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe", "bash", "sh", "zsh", "fish", "nu", "dash", "ash", "ksh", "tcsh", "csh", "jsh":
		return true
	default:
		return false
	}
}

// hasScriptRunnerWithEvalFlag returns true when any arg element is a
// script-interpreter eval flag (node -e, python -c, ruby -e, etc.) either
// immediately after the binary or anywhere in the arg list.
//
// Fixes VULN-NEW-3: previously only detected adjacent (binary, flag) pairs.
// Now also catches:
//   - node --flag -e 'code'       (non-adjacent)
//   - python -c 'code'            (flag as first arg after binary)
//   - ruby -e 'code'
func hasScriptRunnerWithEvalFlag(args []string) bool {
	if len(args) < 1 {
		return false
	}
	// Scan every element independently for known eval flags.
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		for _, flag := range scriptRunnerEvalFlags {
			if arg == flag {
				return true
			}
		}
	}
	// Adjacent-and-beyond scan: binary at position i, eval flag at any
	// position > i. This catches non-adjacent cases like "node --foo -e".
	for i := 0; i < len(args)-1; i++ {
		binary := strings.ToLower(filepath.Base(strings.TrimSpace(args[i])))
		if flag, ok := scriptRunnerEvalFlags[binary]; ok {
			for j := i + 1; j < len(args); j++ {
				if args[j] == flag {
					return true
				}
			}
		}
	}
	return false
}
