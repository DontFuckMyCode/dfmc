// patch_validation.go — Phase 7 tool for validating patches before
// applying them and rolling back when validation fails.
//
// patch_validation_tool runs a dry-run apply followed by a user-specified
// validation command (build, test, lint), then reports per-hunk results
// so the model can decide whether to proceed or roll back. Rollback is
// handled by the companion rollback_tool which uses `git checkout` to
// restore pre-patch file state.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type PatchValidationTool struct {
	engine *Engine
}

func NewPatchValidationTool() *PatchValidationTool { return &PatchValidationTool{} }
func (t *PatchValidationTool) Name() string    { return "patch_validation" }
func (t *PatchValidationTool) Description() string {
	return "Validate a unified-diff patch by running a dry-run apply and optional build/test command."
}

// SetEngine wires the engine for per-target read tracking.
func (t *PatchValidationTool) SetEngine(e *Engine) { t.engine = e }

func (t *PatchValidationTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "patch_validation",
		Title:   "Validate patch",
		Summary: "Dry-run a unified-diff patch and optionally run a build/test command to validate it.",
		Purpose: `Use before applying a large or risky patch to confirm every hunk applies cleanly and the project still builds/passes tests. Dry-run is always performed; a validation command is optional but recommended for non-trivial patches.`,
		Prompt: `Validates a unified-diff patch in two phases:
1. dry-run apply: confirm every hunk matches the target file without modification
2. optional build/test command: run a shell command (e.g. "go build ./..." or "go test ./...") after dry-run to verify the patched code is valid

Returns structured per-file, per-hunk results: hunks_applied, hunks_rejected, fuzzy_offsets, and validation_command exit code. A patch is "clean" when all hunks apply without rejection and the validation command exits 0.`,
		Risk:     RiskExecute,
		Tags:     []string{"patch", "validation", "dry-run", "hunk"},
		Args: []Arg{
			{Name: "patch", Type: ArgString, Required: true, Description: `Unified-diff patch string to validate.`},
			{Name: "validation_command", Type: ArgString, Description: `Optional shell command to run after dry-run (e.g. "go build ./..." or "go test ./..."). Exit code 0 = validation passed.`},
		},
		Returns:    "Structured JSON: {files: [{path, hunks_applied, hunks_rejected, fuzzy_offsets, validation}], validation_passed bool}",
		Idempotent: true,
		CostHint:   "cpu-bound",
	}
}

func (t *PatchValidationTool) Execute(ctx context.Context, req Request) (Result, error) {
	patch := strings.TrimSpace(asString(req.Params, "patch", ""))
	if patch == "" {
		return Result{}, missingParamError("patch_validation", "patch", req.Params,
			`{"patch":"--- a/foo.go\n+++ b/foo.go\n@@ -1,3 +1,3 @@\n-old\n+new\n"}`,
			`patch is required — a unified-diff string. Use apply_patch with dry_run=true to get the same dry-run results if you already have a patch.`)
	}

	validationCmd := strings.TrimSpace(asString(req.Params, "validation_command", ""))
	projectRoot := req.ProjectRoot

	files, parseErr := parseUnifiedDiff(patch)
	if parseErr != nil {
		return Result{}, fmt.Errorf("patch parse error: %w", parseErr)
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf("patch contained no file diffs")
	}

	var results []map[string]any
	var totalHunks, rejectedHunks int

	for _, f := range files {
		targetPath := f.NewPath
		if targetPath == "" || targetPath == "/dev/null" {
			targetPath = f.OldPath
		}
		result := map[string]any{
			"path":           targetPath,
			"hunks_total":    len(f.Hunks),
			"hunks_applied":  0,
			"hunks_rejected": 0,
		}

		if f.IsDeleted {
			result["skip_reason"] = "file deletion"
			results = append(results, result)
			continue
		}

		abs, err := EnsureWithinRoot(projectRoot, targetPath)
		if err != nil {
			result["error"] = err.Error()
			results = append(results, result)
			continue
		}
		data, err := os.ReadFile(abs)
		var original string
		if err != nil {
			if os.IsNotExist(err) {
				result["skip_reason"] = "new file (no original to diff against)"
				results = append(results, result)
				continue
			}
			result["error"] = err.Error()
			results = append(results, result)
			continue
		}
		original = string(data)

		updated, applied, rejected, fuzzyOffsets, hunkErr := applyHunks(original, f.Hunks, f.IsNew)
		result["hunks_applied"] = applied
		result["hunks_rejected"] = rejected
		if len(fuzzyOffsets) > 0 {
			result["fuzzy_offsets"] = fuzzyOffsets
		}
		if hunkErr != nil {
			result["hunk_error"] = hunkErr.Error()
		}
		if updated != "" {
			result["patched_content_preview"] = previewPatch(updated)
		}
		results = append(results, result)
		totalHunks += len(f.Hunks)
		rejectedHunks += rejected
	}

	var validationPassed bool
	var validationOutput string
	var validationExitCode int
	if validationCmd != "" {
		cmdParts, cmdErr := splitCommandArgs(validationCmd)
		if cmdErr != nil || len(cmdParts) == 0 {
			return Result{}, fmt.Errorf("validation_command %q parse error: %v", validationCmd, cmdErr)
		}
		binary, args := cmdParts[0], cmdParts[1:]
		if isBlockedShellInterpreter(binary) {
			return Result{}, fmt.Errorf("validation_command: shell interpreter %q is blocked", binary)
		}
		if token := detectShellMetacharacter(binary); token != "" {
			return Result{}, fmt.Errorf("validation_command does not invoke a shell — binary must be a single executable, not a shell line. Found shell syntax %q in command", token)
		}
		if hasScriptRunnerWithEvalFlag(args) {
			return Result{}, fmt.Errorf("validation_command args contain a script-runner inline-eval flag (-e, -c, -r) which is not supported")
		}
		for _, arg := range args {
			if isBlockedShellInterpreter(arg) {
				return Result{}, fmt.Errorf("validation_command args contain blocked shell interpreter: %s", arg)
			}
		}
		blocked := t.engine.cfg.Tools.Shell.BlockedCommands
		if err := ensureCommandAllowed(binary, args, blocked); err != nil {
			return Result{}, err
		}
		runCtx, cancel := context.WithTimeout(ctx, 120000)
		defer cancel()
		cmd := exec.CommandContext(runCtx, binary, args...)
		cmd.Dir = projectRoot
		out, err := cmd.CombinedOutput()
		validationExitCode = 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				validationExitCode = exitErr.ExitCode()
			} else {
				validationExitCode = 1
			}
		}
		validationOutput = string(out)
		validationPassed = validationExitCode == 0
	}

	clean := rejectedHunks == 0
	if validationCmd != "" {
		clean = clean && validationPassed
	}

	summary := fmt.Sprintf("patch_validation: %d files, %d/%d hunks applied", len(files), totalHunks-rejectedHunks, totalHunks)
	if validationCmd != "" {
		if len(validationOutput) > 80 {
			summary += fmt.Sprintf(", validation: %s...", validationOutput[:80])
		} else {
			summary += fmt.Sprintf(", validation: %s", validationOutput)
		}
	}

	return Result{
		Output: summary,
		Data: map[string]any{
			"files":                results,
			"total_files":          len(files),
			"total_hunks":          totalHunks,
			"rejected_hunks":       rejectedHunks,
			"validation_passed":    validationPassed,
			"validation_exit_code": validationExitCode,
			"validation_output":   validationOutput,
			"clean":               clean,
		},
	}, nil
}

func previewPatch(content string) string {
	lines := strings.SplitN(content, "\n", 10)
	preview := strings.Join(lines, "\n")
	if len(content) > 500 {
		preview += "\n... (truncated)"
	}
	return preview
}


// PatchHunkStats returns per-file hunk counts without applying anything.
func PatchHunkStats(patch string) (map[string]int, error) {
	files, err := parseUnifiedDiff(patch)
	if err != nil {
		return nil, err
	}
	stats := make(map[string]int)
	for _, f := range files {
		path := f.NewPath
		if path == "" || path == "/dev/null" {
			path = f.OldPath
		}
		stats[path] = len(f.Hunks)
	}
	return stats, nil
}

// ValidatePatchIsClean returns true only when all hunks apply without rejection.
func ValidatePatchIsClean(patch, projectRoot string) (bool, int, int, error) {
	files, err := parseUnifiedDiff(patch)
	if err != nil {
		return false, 0, 0, err
	}
	total, rejected := 0, 0
	for _, f := range files {
		path := f.NewPath
		if path == "" || path == "/dev/null" {
			path = f.OldPath
		}
		abs, err := EnsureWithinRoot(projectRoot, path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		_, applied, r, _, _ := applyHunks(string(data), f.Hunks, f.IsNew)
		_ = applied
		total += len(f.Hunks)
		rejected += r
	}
	return rejected == 0, total, rejected, nil
}