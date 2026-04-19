package tools

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
				"For a single-line replacement, prefer edit_file (no diff format needed).")
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
					"Each file diff in the patch must have at least one of those headers naming the file relative to the project root.")
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
		if t.engine != nil {
			if _, statErr := os.Stat(abs); statErr == nil {
				if guardErr := t.engine.EnsureReadBeforeMutation(abs); guardErr != nil {
					return Result{}, fmt.Errorf("apply_patch %s: %w (read the file first via read_file, then retry)", targetPath, guardErr)
				}
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return Result{}, fmt.Errorf("apply_patch %s: stat target: %w", targetPath, statErr)
			}
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
					continue
				}
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
			continue
		}
		entry["hunks_applied"] = applied1
		entry["hunks_rejected"] = rejected
		if len(fuzzyOffsets) > 0 {
			entry["fuzzy_offsets"] = fuzzyOffsets
		}

		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return Result{}, err
			}
			if err := writeFileAtomic(abs, []byte(updated), 0o644); err != nil {
				return Result{}, fmt.Errorf("write %s: %w", targetPath, err)
			}
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

// --- Unified diff parser ----------------------------------------------------

type diffFile struct {
	OldPath   string
	NewPath   string
	IsNew     bool
	IsDeleted bool
	Hunks     []diffHunk
}

type diffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []diffLine
}

type diffLine struct {
	Kind rune // ' ' context, '+' add, '-' delete
	Text string
}

func parseUnifiedDiff(patch string) ([]diffFile, error) {
	lines := strings.Split(patch, "\n")
	var files []diffFile
	var current *diffFile
	var hunk *diffHunk

	flushHunk := func() {
		if current != nil && hunk != nil {
			current.Hunks = append(current.Hunks, *hunk)
			hunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if current != nil {
			files = append(files, *current)
			current = nil
		}
	}

	// hunkConsumed tracks how many old/new lines we've already read for the
	// current hunk. When both counts match the header's declared totals, the
	// hunk is closed and any further content is treated as header/junk until
	// the next "@@" or file marker.
	oldConsumed, newConsumed := 0, 0
	hunkClosed := func() bool {
		if hunk == nil {
			return true
		}
		return oldConsumed >= hunk.OldCount && newConsumed >= hunk.NewCount
	}

	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			flushFile()
			current = &diffFile{}
		case strings.HasPrefix(line, "--- "):
			flushHunk()
			if current == nil {
				current = &diffFile{}
			}
			current.OldPath = stripPathPrefix(strings.TrimSpace(line[4:]))
			if current.OldPath == "/dev/null" {
				current.IsNew = true
			}
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				current = &diffFile{}
			}
			current.NewPath = stripPathPrefix(strings.TrimSpace(line[4:]))
			if current.NewPath == "/dev/null" {
				current.IsDeleted = true
			}
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			hunk = &h
			oldConsumed, newConsumed = 0, 0
		case hunk != nil && !hunkClosed() && len(line) > 0 && (line[0] == ' ' || line[0] == '+' || line[0] == '-'):
			kind := rune(line[0])
			hunk.Lines = append(hunk.Lines, diffLine{Kind: kind, Text: line[1:]})
			switch kind {
			case ' ':
				oldConsumed++
				newConsumed++
			case '-':
				oldConsumed++
			case '+':
				newConsumed++
			}
		case hunk != nil && !hunkClosed() && line == "":
			// A bare blank line inside a hunk body is a blank context line.
			hunk.Lines = append(hunk.Lines, diffLine{Kind: ' ', Text: ""})
			oldConsumed++
			newConsumed++
		case strings.HasPrefix(line, "\\ No newline at end of file"):
			// Ignore — we preserve original trailing-newline behavior.
		default:
			// Header junk ("index ...", "new file mode ..."), trailing blank
			// after a finished hunk, etc. — safely ignored.
		}
	}
	flushFile()
	return files, nil
}

// stripPathPrefix drops the leading "a/" or "b/" that `git diff` inserts.
func stripPathPrefix(p string) string {
	if len(p) >= 2 && (p[:2] == "a/" || p[:2] == "b/") {
		return p[2:]
	}
	return p
}

func parseHunkHeader(s string) (diffHunk, error) {
	// Format: @@ -oldStart,oldCount +newStart,newCount @@ optional
	idx := strings.Index(s, "@@")
	if idx < 0 {
		return diffHunk{}, fmt.Errorf("invalid hunk header")
	}
	body := s[idx+2:]
	end := strings.Index(body, "@@")
	if end < 0 {
		return diffHunk{}, fmt.Errorf("invalid hunk header (no closing @@)")
	}
	body = strings.TrimSpace(body[:end])
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return diffHunk{}, fmt.Errorf("invalid hunk header spec")
	}
	oldStart, oldCount, err := parseRange(parts[0])
	if err != nil {
		return diffHunk{}, err
	}
	newStart, newCount, err := parseRange(parts[1])
	if err != nil {
		return diffHunk{}, err
	}
	return diffHunk{
		OldStart: oldStart, OldCount: oldCount,
		NewStart: newStart, NewCount: newCount,
	}, nil
}

func parseRange(r string) (int, int, error) {
	if len(r) < 2 {
		return 0, 0, fmt.Errorf("bad range %q", r)
	}
	if r[0] != '-' && r[0] != '+' {
		return 0, 0, fmt.Errorf("bad range %q", r)
	}
	body := r[1:]
	start, count := 0, 1
	if i := strings.Index(body, ","); i >= 0 {
		s, err := atoiSafe(body[:i])
		if err != nil {
			return 0, 0, err
		}
		c, err := atoiSafe(body[i+1:])
		if err != nil {
			return 0, 0, err
		}
		start, count = s, c
	} else {
		s, err := atoiSafe(body)
		if err != nil {
			return 0, 0, err
		}
		start, count = s, 1
	}
	return start, count, nil
}

func atoiSafe(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// applyHunks runs each hunk against the original text. Strategy:
//   1. Use OldStart as the first guess; if context + deletions match there,
//      apply.
//   2. Otherwise, scan forward/backward a small window looking for a match.
//   3. If still no match, mark the hunk rejected (don't force).
func applyHunks(original string, hunks []diffHunk, isNew bool) (string, int, int, []int, error) {
	if isNew {
		// For a new file, all context/removal lines should be zero; just
		// emit the '+' lines.
		var b strings.Builder
		applied := 0
		for _, h := range hunks {
			for _, l := range h.Lines {
				if l.Kind == '+' {
					b.WriteString(l.Text)
					b.WriteByte('\n')
				}
			}
			applied++
		}
		out := b.String()
		return out, applied, 0, nil, nil
	}

	lines := splitKeepNewline(original)
	applied, rejected := 0, 0
	fuzzyOffsets := make([]int, 0, len(hunks))

	for _, h := range hunks {
		// Build the sequence of lines that must be present in the source
		// (context + '-' lines), and the replacement sequence ('+' + context).
		var want []string
		var replace []string
		for _, l := range h.Lines {
			switch l.Kind {
			case ' ':
				want = append(want, l.Text)
				replace = append(replace, l.Text)
			case '-':
				want = append(want, l.Text)
			case '+':
				replace = append(replace, l.Text)
			}
		}
		if len(want) == 0 && len(replace) == 0 {
			continue
		}

		anchor := h.OldStart - 1
		if anchor < 0 {
			anchor = 0
		}
		idx, fuzzyOffset := findHunkAnchor(lines, want, anchor)
		if idx < 0 {
			rejected++
			continue
		}
		if fuzzyOffset != 0 {
			fuzzyOffsets = append(fuzzyOffsets, fuzzyOffset)
		}
		// Splice: replace lines[idx : idx+len(want)] with `replace` (preserve
		// trailing newlines per original line).
		before := append([]string{}, lines[:idx]...)
		after := append([]string{}, lines[idx+len(want):]...)
		middle := make([]string, 0, len(replace))
		for _, r := range replace {
			middle = append(middle, r+"\n")
		}
		// If the last original line lacked a trailing newline and the replace
		// region touches the end, preserve that.
		if idx+len(want) == len(lines) && len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") && len(middle) > 0 {
			middle[len(middle)-1] = strings.TrimSuffix(middle[len(middle)-1], "\n")
		}
		lines = append(before, append(middle, after...)...)
		applied++
	}

	return strings.Join(lines, ""), applied, rejected, fuzzyOffsets, nil
}

// findHunkAnchor searches for the contiguous sequence `want` in `lines`,
// preferring the hint anchor, then expanding outward.
func findHunkAnchor(lines, want []string, hint int) (int, int) {
	if len(want) == 0 {
		return hint, 0
	}
	// Normalize both sides: drop trailing CR+LF so CRLF-ended source
	// files match against LF-normalized hunks (and vice-versa). Without
	// the \r strip, `foo\r\n` trimmed to `foo\r` would never equal a
	// hunk's `foo` — a silent "all hunks rejected" on Windows files.
	trim := func(s string) string { return strings.TrimRight(s, "\r\n") }

	match := func(at int) bool {
		if at < 0 || at+len(want) > len(lines) {
			return false
		}
		for i, w := range want {
			if trim(lines[at+i]) != trim(w) {
				return false
			}
		}
		return true
	}

	if match(hint) {
		return hint, 0
	}
	// Fuzzy anchor search: expand outward a small window around the hint.
	// Previously we expanded ±200 lines and then linear-scanned the whole
	// file as a last resort — that turned a stale-context patch into a
	// silently-misplaced edit far from the intended site (REPORT H1, the
	// "patch landed in the wrong function" class of bug). A tight window
	// keeps the fuzz forgiving for normal drift (a few intervening edits)
	// while letting truly stale hunks fail loudly so the caller re-reads
	// the file.
	const maxFuzz = 10
	for delta := 1; delta <= maxFuzz; delta++ {
		if match(hint + delta) {
			return hint + delta, delta
		}
		if match(hint - delta) {
			return hint - delta, -delta
		}
	}
	return -1, 0
}

// splitKeepNewline splits content on '\n' but keeps the trailing newline on
// each line so reassembly preserves byte identity.
func splitKeepNewline(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
