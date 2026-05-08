package tools

// apply_patch_parse.go — unified-diff parser used by apply_patch.
// Produces []diffFile from a raw patch string, tolerating typical
// `git diff` headers ("diff --git", "index ...", "new file mode ...",
// trailing "\ No newline at end of file" markers). Companion siblings:
//
//   - apply_patch.go        ApplyPatchTool + Execute (request shape +
//                           per-target read-before-mutate gate +
//                           atomic write + Result formatting)
//   - apply_patch_hunks.go  applyHunks (anchor-based splice) +
//                           findHunkAnchor (±10 line fuzz) +
//                           splitKeepNewline byte-identity helper

import (
	"fmt"
	"math"
	"strings"
)

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
		default:
			const noNewlineMarker = "\\ No newline at end of file"
			if len(line) >= len(noNewlineMarker) && line[:len(noNewlineMarker)] == noNewlineMarker {
				// Ignore — we preserve original trailing-newline behavior.
				continue
			}
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
	var start, count int
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
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		if n > math.MaxInt/10 {
			return 0, fmt.Errorf("number overflow: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
