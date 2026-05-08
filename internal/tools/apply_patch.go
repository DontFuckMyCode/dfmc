package tools

// apply_patch.go — ApplyPatchTool surface: ingests a unified-diff
// string, parses it via apply_patch_parse.go, runs each file's hunks
// via apply_patch_hunks.go, and serialises results into the standard
// Result shape. Owns the per-target read-before-mutate gate and the
// atomic write path; the diff-shape understanding lives in the two
// sibling files. Companion siblings:
//
//   - apply_patch_parse.go  parseUnifiedDiff + diffFile/Hunk/Line
//                           types + path/range parsing helpers
//   - apply_patch_hunks.go  applyHunks anchor-based splice +
//                           findHunkAnchor (±10-line fuzz) +
//                           splitKeepNewline byte-identity helper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApplyPatchTool applies a unified-diff patch to files under the project
// root. Supports multi-file patches. Hunks are applied with strict context
// matching — if context lines don't match, the hunk is rejected rather than
// "close-enough" patched.
//
// Scope: single-purpose, deliberately narrow. Use for surgical LLM-generated
// edits where edit_file would require awkward string-matching. For broader
// refactors, prefer a sequence of edit_file calls.
type ApplyPatchTool struct {
	// engine is set at registration time so apply_patch can call the
	// per-target read-before-mutate gate. Without this, a fabricated
	// diff could overwrite arbitrary files inside the project root with
	// no prior read_file — a silent gap in the safety model that
	// edit_file and write_file both already plug.
	engine *Engine
}

func NewApplyPatchTool() *ApplyPatchTool { return &ApplyPatchTool{} }
func (t *ApplyPatchTool) Name() string   { return "apply_patch" }
func (t *ApplyPatchTool) Description() string {
	return "Apply a unified-diff patch to one or more files."
}

// SetEngine wires the engine reference for the read-before-mutate
// check. Called once at registration time (see tools.New).
func (t *ApplyPatchTool) SetEngine(e *Engine) {
	t.engine = e
}

func (t *ApplyPatchTool) Execute(_ context.Context, req Request) (Result, error) {
	// C2: refuse when ProjectRoot is unset. Without a root, EnsureWithinRoot
	// resolves relative targets against the current working directory,
	// which means a fabricated patch could touch any file the dfmc
	// process can write to. Better to fail loudly here than silently
	// honour an attacker-controlled path.
	if strings.TrimSpace(req.ProjectRoot) == "" {
		return Result{}, fmt.Errorf("apply_patch: project root is not set — refusing to apply patch with no path anchor")
	}
	patch := asString(req.Params, "patch", "")
	if strings.TrimSpace(patch) == "" {
		return Result{}, missingParamError("apply_patch", "patch", req.Params,
			`{"patch":"--- a/foo.go\n+++ b/foo.go\n@@ -1,3 +1,3 @@\n-old line\n+new line\n unchanged\n"}`,
			`patch is a unified-diff string. Each file diff starts with --- a/path / +++ b/path then @@ hunks. Use dry_run=true first to validate without writing. For single-line replacements prefer edit_file — apply_patch shines when changing multiple non-adjacent regions.`)
	}
	dryRun := asBool(req.Params, "dry_run", false)

	files, err := parseUnifiedDiff(patch)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf(
			"apply_patch: patch parsed but contained no file diffs. " +
				"A unified diff must have at least one `--- a/path` / `+++ b/path` header followed by `@@ ... @@` hunks. " +
				"Example: `--- a/foo.go\\n+++ b/foo.go\\n@@ -1,3 +1,3 @@\\n-old\\n+new\\n unchanged\\n`. " +
				"For a single-line replacement, prefer edit_file (no diff format needed)")
	}

	var applied []map[string]any
	var outLines []string
	for _, f := range files {
		targetPath := f.NewPath
		if targetPath == "" || targetPath == "/dev/null" {
			targetPath = f.OldPath
		}
		if targetPath == "" {
			return Result{}, fmt.Errorf(
				"apply_patch: diff entry has no target path — both `--- a/<path>` and `+++ b/<path>` headers are missing. " +
					"Each file diff in the patch must have at least one of those headers naming the file relative to the project root")
		}
		// C2: normalize before EnsureWithinRoot. filepath.Clean collapses
		// `a/../b` and adjacent slashes so a hostile diff that wrote
		// `--- a/../../etc/passwd` can't bypass the root check via a
		// non-canonical form. Absolute paths are also refused — every
		// patch target must be relative to the project root.
		targetPath = filepath.Clean(targetPath)
		if filepath.IsAbs(targetPath) {
			return Result{}, fmt.Errorf("apply_patch %s: absolute paths are not allowed — patches must target paths relative to the project root", targetPath)
		}
		abs, err := EnsureWithinRoot(req.ProjectRoot, targetPath)
		if err != nil {
			return Result{}, err
		}
		// Per-target read-before-mutate must be keyed off disk reality,
		// not the diff header's "new file" claim. A fabricated /dev/null
		// header against an existing file used to bypass the safety gate
		// entirely. Stat the resolved path independently and require a
		// prior read_file snapshot whenever the file already exists.
		// C2: Per-target read-before-mutate gate. Requires engine to be
		// wired so the engine can track which files have been read. If the
		// engine is nil, refuse rather than silently bypassing the safety
		// check — a nil engine means read-tracking was never initialized.
		if t.engine == nil {
			return Result{}, fmt.Errorf("apply_patch: engine is not wired — read-before-mutate gate is unavailable; refusing to apply without an engine (caller must call SetEngine before use)")
		}
		// Serialize the entire read→write sequence per path so no concurrent
		// goroutine can delete-and-recreate the file between our stat check
		// (which gates EnsureReadBeforeMutation) and our write. The lock
		// covers all mutations: stat, read-before-mutation, read, write.
		var release func()
		if !dryRun {
			release = t.engine.LockPath(abs)
		}
		if _, statErr := os.Stat(abs); statErr == nil {
			// Only gate on read-before-mutation when actually writing.
			// Dry-run has no side effects, so the snapshot isn't required.
			if !dryRun {
				if guardErr := t.engine.EnsureReadBeforeMutation(abs); guardErr != nil {
					release()
					return Result{}, fmt.Errorf("apply_patch %s: %w (read the file first via read_file, then retry)", targetPath, guardErr)
				}
			}
		} else if !os.IsNotExist(statErr) {
			if !dryRun {
				release()
			}
			return Result{}, fmt.Errorf("apply_patch %s: stat target: %w", targetPath, statErr)
		}

		entry := map[string]any{
			"path":     targetPath,
			"hunks":    len(f.Hunks),
			"new_file": f.IsNew,
			"deleted":  f.IsDeleted,
			"dry_run":  dryRun,
		}

		if f.IsDeleted {
			if !dryRun {
				if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
					entry["error"] = err.Error()
					applied = append(applied, entry)
					outLines = append(outLines, fmt.Sprintf("DEL  %s  FAIL %s", targetPath, err))
					release()
					continue
				}
				release() // unlock after delete
			}
			outLines = append(outLines, fmt.Sprintf("DEL  %s", targetPath))
			applied = append(applied, entry)
			continue
		}

		var original string
		if !f.IsNew {
			data, err := os.ReadFile(abs)
			if err != nil {
				return Result{}, fmt.Errorf("read %s: %w", targetPath, err)
			}
			original = string(data)
		}

		updated, applied1, rejected, fuzzyOffsets, err := applyHunks(original, f.Hunks, f.IsNew)
		if err != nil {
			entry["error"] = err.Error()
			applied = append(applied, entry)
			outLines = append(outLines, fmt.Sprintf("FAIL %s  %s", targetPath, err))
			if !dryRun {
				release()
			}
			continue
		}
		entry["hunks_applied"] = applied1
		entry["hunks_rejected"] = rejected
		if len(fuzzyOffsets) > 0 {
			entry["fuzzy_offsets"] = fuzzyOffsets
		}

		if !dryRun {
			// release is already held from above — LockPath was called before
			// stat to serialize the entire read→write sequence.
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				release()
				return Result{}, err
			}
			if err := writeFileAtomic(abs, []byte(updated), 0o644); err != nil {
				release()
				return Result{}, fmt.Errorf("write %s: %w", targetPath, err)
			}
			release() // unlock after write
		}
		action := "EDIT"
		if f.IsNew {
			action = "NEW "
		}
		outLines = append(outLines, fmt.Sprintf("%s %s  %d/%d hunks", action, targetPath, applied1, applied1+rejected))
		applied = append(applied, entry)
	}

	header := fmt.Sprintf("%d file(s) patched", len(files))
	if dryRun {
		header += " (dry run — no files written)"
	}
	return Result{
		Output: header + "\n" + strings.Join(outLines, "\n"),
		Data: map[string]any{
			"files":   applied,
			"count":   len(applied),
			"dry_run": dryRun,
		},
	}, nil
}
