package tools

// command_recovery.go — recovery-hint generators for the two most
// common LLM packing footguns we see hit run_command:
//
//  1. Whole shell line in `command`: `cd /repo && go build ./...` —
//     suggestRunCommandRecovery turns it into a copy-pasteable tool_call
//     with `command`, `args`, and `dir` correctly populated.
//  2. Binary+args packed in `command`: `command:"go build ./..."` with
//     no `args` set — suggestSplitRunCommand emits the split shape so
//     the model self-corrects on the next call.
//
// Companion siblings:
//
//   - command.go              RunCommandTool.Execute + result shaping
//   - command_args.go         argv tokenization + timeout resolution
//   - command_validate.go     binary blocklist + arg-sequence policy

import (
	"fmt"
	"path/filepath"
	"strings"
)

// suggestRunCommandRecovery turns a shell-line command that the model
// fed into `command` into a copy-pasteable recovery tool_call shape.
// We focus on the single most common case caught in real sessions:
// `cd <dir> && <real command>` (and the `;` variant). When that
// pattern matches we extract the directory and the trailing command
// so the model sees exactly which tokens go into `command`, `args`,
// and `dir`. For anything else we return "" and the caller emits the
// generic example. Keep this conservative — a wrong-looking suggestion
// is worse than the generic one.
func suggestRunCommandRecovery(command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(cmd), "cd ") {
		return ""
	}
	// Find the first `&&` or `;` separator after `cd <dir>`.
	rest := cmd[3:]
	sepIdx, sepLen := -1, 0
	if i := strings.Index(rest, "&&"); i >= 0 {
		sepIdx, sepLen = i, 2
	}
	if i := strings.Index(rest, ";"); i >= 0 && (sepIdx == -1 || i < sepIdx) {
		sepIdx, sepLen = i, 1
	}
	if sepIdx < 0 {
		return ""
	}
	dir := strings.TrimSpace(rest[:sepIdx])
	tail := strings.TrimSpace(rest[sepIdx+sepLen:])
	if dir == "" || tail == "" {
		return ""
	}
	// Strip surrounding quotes from dir, normalize separators.
	dir = strings.Trim(dir, `"'`)
	dir = slashPath(dir)
	// Split tail into binary + args using whitespace; keep it simple
	// (no quote-aware tokenization) since the goal is a hint, not an
	// exec.
	parts := strings.Fields(tail)
	if len(parts) == 0 {
		return ""
	}
	bin := parts[0]
	rawArgs := parts[1:]
	// JSON-encode args array with %q-style quoting on each element.
	argLits := make([]string, 0, len(rawArgs))
	for _, a := range rawArgs {
		argLits = append(argLits, fmt.Sprintf("%q", a))
	}
	return fmt.Sprintf(
		`{"name":"run_command","args":{"command":%q,"args":[%s],"dir":%q}}`,
		bin, strings.Join(argLits, ","), dir,
	)
}

func slashPath(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
}

// detectBinaryArgsPacking flags the `command:"go build ./..."` shape:
// no shell syntax (so detectShellMetacharacter passed), but the binary
// slot has whitespace-separated tokens — almost certainly bin+args
// packed together. Returns (bin, rest, true) when the pattern matches,
// otherwise zero+false. Conservative on purpose: we skip anything that
// looks like a real path (Windows "Program Files\foo.exe", Unix
// "/usr/local/bin/my prog", etc.) so legitimate quoted paths with
// spaces aren't false-positived.
func detectBinaryArgsPacking(command string) (string, string, bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "", "", false
	}
	// Quoted command (`"foo bar.exe" --flag`) — leave it alone; the model
	// is being explicit about the path.
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, `'`) {
		return "", "", false
	}
	idx := strings.IndexAny(cmd, " \t")
	if idx <= 0 {
		return "", "", false
	}
	bin := cmd[:idx]
	rest := strings.TrimSpace(cmd[idx+1:])
	if rest == "" {
		return "", "", false
	}
	// If the binary token itself has a path separator, treat the whole
	// thing as a path with embedded spaces (rare but legitimate). Same
	// for ".exe" without separators — could still be a packed call, so
	// we let the path-separator check be the discriminator.
	if strings.ContainsAny(bin, "/\\") {
		return "", "", false
	}
	return bin, rest, true
}

// suggestSplitRunCommand renders the recovery shape for the binary+args
// packing case: split the offender on whitespace and JSON-encode each
// token into the `args` array. Same conservative tokenization as
// suggestRunCommandRecovery — if the model packed clever quoting
// nonsense, the hint will at least show the right *shape* even if the
// exact tokens need adjustment.
func suggestSplitRunCommand(bin, rest string) string {
	parts := strings.Fields(rest)
	argLits := make([]string, 0, len(parts))
	for _, p := range parts {
		argLits = append(argLits, fmt.Sprintf("%q", p))
	}
	return fmt.Sprintf(
		`{"name":"run_command","args":{"command":%q,"args":[%s]}}`,
		bin, strings.Join(argLits, ","),
	)
}
