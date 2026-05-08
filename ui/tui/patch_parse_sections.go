// patch_parse_sections.go — section + hunk extraction for unified
// diffs. Sibling of patch_parse.go which keeps the small per-text
// helpers (patchSectionPaths, totalPatchHunks, patchLineCounts,
// extractPatchedFiles), the git-shellout pair
// (gitWorkingDiff, applyUnifiedDiff), and the assistant-message
// diff-extraction surface (latestAssistantUnifiedDiff,
// extractUnifiedDiff, looksLikeUnifiedDiff).
//
// Splitting the section/hunk walkers out keeps patch_parse.go
// scoped to "what is a patch" while this file owns "given a
// multi-file unified diff string, slice it into per-file sections
// and per-hunk sub-records the patch panel renders one card per."
// Both walkers share the same flush-on-marker bookkeeping pattern.

package tui

import (
	"path/filepath"
	"strings"
)

func parseUnifiedDiffSections(patch string) []patchSection {
	text := strings.ReplaceAll(strings.TrimSpace(patch), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	sections := make([]patchSection, 0, 8)
	current := patchSection{}
	currentLines := make([]string, 0, 32)

	flush := func() {
		if len(currentLines) == 0 {
			return
		}
		current.Content = strings.Join(currentLines, "\n")
		current.Hunks = extractPatchHunks(current.Content)
		if len(current.Hunks) > 0 {
			current.HunkCount = len(current.Hunks)
		}
		if strings.TrimSpace(current.Path) == "" {
			paths := extractPatchedFiles(current.Content)
			if len(paths) > 0 {
				current.Path = paths[0]
			}
		}
		if strings.TrimSpace(current.Path) != "" {
			sections = append(sections, current)
		}
		current = patchSection{}
		currentLines = currentLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") && len(currentLines) > 0 {
			flush()
		}
		currentLines = append(currentLines, line)
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current.Path = normalizePatchPath(parts[3])
			}
		case strings.HasPrefix(line, "+++ "):
			path := normalizePatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			if path != "" {
				current.Path = path
			}
		case strings.HasPrefix(line, "@@"):
			current.HunkCount++
		}
	}
	flush()
	return sections
}

func normalizePatchPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	if path == "" || path == "/dev/null" || path == "dev/null" {
		return ""
	}
	return path
}

func extractPatchHunks(diff string) []patchHunk {
	text := strings.ReplaceAll(strings.TrimSpace(diff), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	prefix := make([]string, 0, 8)
	hunks := make([]patchHunk, 0, 8)
	current := patchHunk{}
	currentLines := make([]string, 0, 16)
	inHunk := false

	flush := func() {
		if !inHunk || len(currentLines) == 0 {
			return
		}
		current.Content = strings.Join(currentLines, "\n")
		hunks = append(hunks, current)
		current = patchHunk{}
		currentLines = currentLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			flush()
			inHunk = true
			current = patchHunk{Header: strings.TrimSpace(line)}
			currentLines = append(currentLines[:0], prefix...)
			currentLines = append(currentLines, line)
			continue
		}
		if !inHunk {
			prefix = append(prefix, line)
			continue
		}
		currentLines = append(currentLines, line)
	}
	flush()
	return hunks
}
