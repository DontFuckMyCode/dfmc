package security

// report_diff.go — narrow a Report to only the findings a unified
// diff actually introduced. The auto-audit hook (engine/security_audit.go)
// scans whole files after a write, which surfaces every pre-existing
// secret/vuln in the same file as well — too noisy. Scoping the
// report to the diff's added lines lets callers say "this write
// introduced N findings" with confidence.
//
// Pure transformation: takes a Report + a unified-diff string, returns
// a new Report. No I/O, no goroutines, no scanner re-run. Empty or
// unparsable diff returns the original report unchanged so callers
// can adopt the filter incrementally without breaking on edge cases.
//
// Diff parsing is intentionally minimal: ATX-style hunk headers
// (`@@ -old,n +new,n @@`) are honoured, the `+++` file header tags
// the new path, and `+` body lines (not `+++`) count as added. We
// don't try to handle git-binary patches, rename-only headers, or
// `\ No newline at end of file` markers — those don't carry secrets.

import (
	"bufio"
	"path/filepath"
	"strconv"
	"strings"
)

// AddedLineIndex maps a new-file path (slash-form, no leading
// `a/`/`b/`) to the set of line numbers added by the diff. Use
// IndexAddedLines to build one; FilterToAddedLines will do it for
// you when given a raw diff string.
type AddedLineIndex map[string]map[int]struct{}

// Has returns true when the diff added (or marked as added) the
// given line number for path. Path matching is suffix-tolerant — a
// finding stored as "/abs/path/internal/auth.go" matches a diff
// header "+++ b/internal/auth.go" because we strip a/ b/ prefixes
// and compare on slash-normalised suffixes.
func (idx AddedLineIndex) Has(path string, line int) bool {
	if idx == nil || line <= 0 {
		return false
	}
	target := normaliseDiffPath(path)
	if target == "" {
		return false
	}
	for diffPath, lines := range idx {
		if !pathSuffixMatch(target, diffPath) {
			continue
		}
		if _, ok := lines[line]; ok {
			return true
		}
	}
	return false
}

// FilterToAddedLines returns a new Report containing only Secrets /
// Vulnerabilities whose (File, Line) falls on an added line in the
// supplied unified diff. FilesScanned is preserved so callers can
// still report "we looked at N files" alongside the scoped finding
// counts.
//
// Empty diff or parse failure returns the original report unchanged
// — the function never errors out. The auto-audit hook would rather
// over-report than skip warnings because the diff parser tripped on
// an exotic header.
func (r Report) FilterToAddedLines(unifiedDiff string) Report {
	if strings.TrimSpace(unifiedDiff) == "" {
		return r
	}
	idx := IndexAddedLines(unifiedDiff)
	if len(idx) == 0 {
		return r
	}
	out := Report{FilesScanned: r.FilesScanned}
	for _, s := range r.Secrets {
		if idx.Has(s.File, s.Line) {
			out.Secrets = append(out.Secrets, s)
		}
	}
	for _, v := range r.Vulnerabilities {
		if idx.Has(v.File, v.Line) {
			out.Vulnerabilities = append(out.Vulnerabilities, v)
		}
	}
	return out
}

// IndexAddedLines parses a unified diff into an AddedLineIndex. The
// parser walks line-by-line and tracks state in a tiny state machine:
//
//   - "+++ b/<path>" sets the current file (and resets the line
//     counter when the next hunk header arrives).
//   - "@@ -... +N,M @@" sets the new-file line counter to N.
//   - "+<body>" lines (not "+++") record an added line at the current
//     counter and advance it.
//   - " <body>" (context) and "-<body>" (removed) lines just advance
//     the counter / skip respectively.
//
// Lines that don't match any of the above (the diff prologue, "diff
// --git" headers, "index ..." lines, "\ No newline" markers) are
// ignored — they don't contribute added content.
func IndexAddedLines(unifiedDiff string) AddedLineIndex {
	idx := AddedLineIndex{}
	scanner := bufio.NewScanner(strings.NewReader(unifiedDiff))
	// Diff lines occasionally exceed the default 64KB token cap on
	// generated patches (large vendored files). Bump the buffer
	// modestly so a long line doesn't truncate the entire parse.
	const maxLine = 1 << 20
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)

	currentFile := ""
	newLine := 0
	inHunk := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			currentFile = stripDiffPrefix(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			inHunk = false
			if currentFile != "" {
				if _, ok := idx[currentFile]; !ok {
					idx[currentFile] = map[int]struct{}{}
				}
			}
		case strings.HasPrefix(line, "--- "):
			// Old-side header — ignored; we only track the new file.
		case strings.HasPrefix(line, "@@"):
			if start, ok := parseHunkNewStart(line); ok {
				newLine = start
				inHunk = true
			}
		case currentFile == "" || !inHunk:
			// Prologue, between-file content, or unparsable hunk —
			// nothing to record.
		case strings.HasPrefix(line, "+"):
			// Skip the false-positive `+++` (file header) — the
			// HasPrefix check above runs after it because of the
			// switch order, so this branch only sees real `+body`
			// lines.
			idx[currentFile][newLine] = struct{}{}
			newLine++
		case strings.HasPrefix(line, "-"):
			// Removed line — does not advance new-file counter.
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — skip without advancing.
		default:
			// Context line (typically " body" with a leading space).
			newLine++
		}
	}
	// Drop entries that ended up with no added lines so callers can
	// rely on `len(idx[path]) > 0` meaning "this path got real
	// additions".
	for k, v := range idx {
		if len(v) == 0 {
			delete(idx, k)
		}
	}
	return idx
}

// parseHunkNewStart pulls the new-file start line out of a hunk
// header like "@@ -10,5 +12,7 @@". Returns 0/false on malformed
// input; the caller should treat that as "skip this hunk".
func parseHunkNewStart(header string) (int, bool) {
	// Find the "+" range. Format is always
	//   @@ -<old> +<new> @@[ optional function context]
	plus := strings.Index(header, "+")
	if plus < 0 {
		return 0, false
	}
	rest := header[plus+1:]
	// rest looks like "12,7 @@ ..." or "12 @@ ...".
	end := strings.IndexAny(rest, " ,@")
	if end < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// stripDiffPrefix removes the leading "a/" or "b/" from a diff path
// header and any "\t<timestamp>" trailer (some `diff -u` flavours
// append a tab-separated mtime). Returns slash-form.
func stripDiffPrefix(path string) string {
	if i := strings.IndexAny(path, "\t"); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimSpace(path)
	if path == "" || path == "/dev/null" {
		return ""
	}
	path = filepath.ToSlash(path)
	for _, p := range []string{"a/", "b/"} {
		if rest, ok := strings.CutPrefix(path, p); ok {
			path = rest
			break
		}
	}
	return path
}

// normaliseDiffPath canonicalises a finding's file path so it can be
// compared against entries in AddedLineIndex. Slash-form, no leading
// "./", no trailing slash. Absolute paths are kept verbatim — the
// suffix match handles the "abs vs project-relative" mismatch.
func normaliseDiffPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	if rest, ok := strings.CutPrefix(p, "./"); ok {
		p = rest
	}
	return strings.TrimRight(p, "/")
}

// pathSuffixMatch returns true when the longer path ends with the
// shorter one at a slash boundary, OR they are equal. Lets a
// finding stored with an absolute path match a diff entry stored
// project-relative without forcing callers to normalise both sides
// to the same shape.
func pathSuffixMatch(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	// a is now the shorter; check b ends with "/<a>" or just <a>.
	return strings.HasSuffix(b, "/"+a)
}
