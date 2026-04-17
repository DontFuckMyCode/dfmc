// Near-duplicate code detector. Deterministic, offline, language-aware
// via the existing strip-strings-and-comments helpers. The approach
// is a rolling-hash over sliding windows of normalized non-blank
// source lines:
//
//   1. Strip strings + comments so trivial mentions don't match.
//   2. Collapse runs of whitespace, trim each line, drop blanks.
//   3. For each file, emit every window of `minLines` consecutive
//      normalized lines. Hash with FNV-1a 64.
//   4. Group by hash; bucket with >=2 entries is a duplicate.
//   5. Cluster: merge overlapping windows in the same group into the
//      longest contiguous run so the report doesn't list every
//      sub-window separately (a 12-line clone at min=6 otherwise
//      shows up as 7 overlapping hits per location).
//
// The detector is line-based, NOT AST-based, which means it catches
// copy-paste even when the surrounding syntax differs slightly — but
// misses clones that are semantically identical with renamed
// identifiers. That's an acceptable tradeoff for the first pass; a
// later pass can normalize identifiers for type-2 clone detection.

package engine

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Minimum run of non-blank normalized lines to consider a clone.
	// 6 is the pmd-cpd/SonarQube default — long enough that matches
	// aren't just "three imports and a brace", short enough to catch
	// real copy-paste.
	duplicationMinLines = 6
	// Hard cap on lines per file — gigantic generated files would
	// dominate the bucket map and aren't actionable anyway.
	duplicationMaxLinesPerFile = 20000
	// Upper bound on reported groups so JSON stays small.
	duplicationMaxGroups = 50
)

// windowEntry is one occurrence of a normalized window in some file.
type windowEntry struct {
	File      string
	StartLine int // original 1-indexed source line
	EndLine   int // inclusive original source line
}

type normalizedLine struct {
	origLine int // 1-indexed original source line
	text     string
}

func detectDuplication(paths []string, minLines int) DuplicationReport {
	if minLines <= 0 {
		minLines = duplicationMinLines
	}
	report := DuplicationReport{
		MinLines:     minLines,
		FilesScanned: 0,
	}
	if len(paths) == 0 {
		return report
	}

	buckets := map[uint64][]windowEntry{}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		report.FilesScanned++

		stripped := stripStringsAndComments(string(content), filepath.Ext(path))
		norm := normalizeForDuplication(stripped)
		if len(norm) > duplicationMaxLinesPerFile {
			norm = norm[:duplicationMaxLinesPerFile]
		}
		if len(norm) < minLines {
			continue
		}

		for i := 0; i+minLines <= len(norm); i++ {
			window := norm[i : i+minLines]
			h := hashNormalizedWindow(window)
			entry := windowEntry{
				File:      filepath.ToSlash(path),
				StartLine: window[0].origLine,
				EndLine:   window[len(window)-1].origLine,
			}
			buckets[h] = append(buckets[h], entry)
			report.WindowsHashed++
		}
	}

	groups := buildDuplicationGroups(buckets, minLines)
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Length != groups[j].Length {
			return groups[i].Length > groups[j].Length
		}
		if len(groups[i].Locations) != len(groups[j].Locations) {
			return len(groups[i].Locations) > len(groups[j].Locations)
		}
		if len(groups[i].Locations) == 0 || len(groups[j].Locations) == 0 {
			return false
		}
		return groups[i].Locations[0].File < groups[j].Locations[0].File
	})

	dupLines := 0
	for _, g := range groups {
		for _, loc := range g.Locations {
			dupLines += loc.EndLine - loc.StartLine + 1
		}
	}
	report.DuplicatedLines = dupLines

	if len(groups) > duplicationMaxGroups {
		groups = groups[:duplicationMaxGroups]
	}
	report.Groups = groups
	return report
}

// normalizeForDuplication collapses a file's lines into a slice of
// (origLine, normalizedText) pairs with blanks skipped. We keep the
// original line number so the final report can point at a real
// location in the source, even after comment stripping rewrote lines
// to whitespace.
func normalizeForDuplication(text string) []normalizedLine {
	lines := strings.Split(text, "\n")
	out := make([]normalizedLine, 0, len(lines))
	for i, raw := range lines {
		norm := collapseWhitespace(raw)
		if norm == "" {
			continue
		}
		out = append(out, normalizedLine{origLine: i + 1, text: norm})
	}
	return out
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // leading whitespace is dropped
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\r' {
			if prevSpace {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
			continue
		}
		b.WriteByte(c)
		prevSpace = false
	}
	return strings.TrimSuffix(b.String(), " ")
}

func hashNormalizedWindow(window []normalizedLine) uint64 {
	h := fnv.New64a()
	for i, line := range window {
		if i > 0 {
			_, _ = h.Write([]byte{'\n'})
		}
		_, _ = h.Write([]byte(line.text))
	}
	return h.Sum64()
}

// buildDuplicationGroups turns raw hash buckets into DuplicationGroups.
// Overlapping windows inside the same file are folded into one span,
// so a 12-line clone at minLines=6 shows up as one group with spans
// of length 12 — not seven overlapping sub-windows.
func buildDuplicationGroups(buckets map[uint64][]windowEntry, minLines int) []DuplicationGroup {
	groups := make([]DuplicationGroup, 0, len(buckets))
	for _, entries := range buckets {
		if len(entries) < 2 {
			continue
		}
		g := mergeBucketToGroup(entries)
		if len(g.Locations) < 2 {
			// All overlapping hits were in a single file — one copy.
			continue
		}
		length := 0
		for _, loc := range g.Locations {
			span := loc.EndLine - loc.StartLine + 1
			if span > length {
				length = span
			}
		}
		if length < minLines {
			length = minLines
		}
		g.Length = length
		groups = append(groups, g)
	}
	return groups
}

// mergeBucketToGroup folds overlapping / adjacent windows in the same
// file into their outer span. The caller decides whether the result
// qualifies as a clone (>= 2 locations).
func mergeBucketToGroup(entries []windowEntry) DuplicationGroup {
	byFile := map[string][]windowEntry{}
	for _, e := range entries {
		byFile[e.File] = append(byFile[e.File], e)
	}
	locs := make([]DuplicationLocation, 0, len(byFile))
	for file, es := range byFile {
		sort.Slice(es, func(i, j int) bool {
			return es[i].StartLine < es[j].StartLine
		})
		curStart := es[0].StartLine
		curEnd := es[0].EndLine
		for i := 1; i < len(es); i++ {
			if es[i].StartLine <= curEnd+1 {
				if es[i].EndLine > curEnd {
					curEnd = es[i].EndLine
				}
				continue
			}
			locs = append(locs, DuplicationLocation{File: file, StartLine: curStart, EndLine: curEnd})
			curStart = es[i].StartLine
			curEnd = es[i].EndLine
		}
		locs = append(locs, DuplicationLocation{File: file, StartLine: curStart, EndLine: curEnd})
	}
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		return locs[i].StartLine < locs[j].StartLine
	})
	return DuplicationGroup{Locations: locs}
}
