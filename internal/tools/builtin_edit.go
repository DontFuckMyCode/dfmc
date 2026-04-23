// edit_file tool: exact-string replace with CRLF-tolerant matching,
// self-teaching miss/ambiguity errors (so the agent can recover
// without burning tool rounds), and round-trip preservation of the
// file's original per-line newline style. Extracted from builtin.go.

package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type EditFileTool struct{}

func NewEditFileTool() *EditFileTool { return &EditFileTool{} }
func (t *EditFileTool) Name() string { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Apply exact string replacement on a text file."
}
func (t *EditFileTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", "")
	oldStr := asString(req.Params, "old_string", "")
	newStr := asString(req.Params, "new_string", "")
	replaceAll := asBool(req.Params, "replace_all", false)

	if strings.TrimSpace(oldStr) == "" {
		return Result{}, missingParamError("edit_file", "old_string", req.Params,
			`{"path":"main.go","old_string":"return nil","new_string":"return ctx.Err()"}`,
			`old_string must be the EXACT text already in the file (whitespace, indentation, line endings included). Read the file first, then copy the unique anchor you want to replace.`)
	}
	if strings.TrimSpace(path) == "" {
		return Result{}, missingParamError("edit_file", "path", req.Params,
			`{"path":"internal/engine/engine.go","old_string":"<exact match>","new_string":"<replacement>"}`,
			`path is the file to edit (relative to project root).`)
	}
	if oldStr == newStr {
		return Result{}, fmt.Errorf(`edit_file: old_string and new_string are identical — nothing to do. Either change new_string, or use read_file if you only wanted to view the section.`)
	}

	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{}, err
	}
	src := string(data)

	// Normalize both haystack and needle to LF for matching so an agent
	// running on Linux can edit a file written on Windows (CRLF) without
	// the match silently failing. The normalized forms are used for the
	// replace; afterward we re-apply the file's original newline style
	// to the rewritten content so we don't flip line endings as a
	// side-effect of the edit.
	wasCRLF := strings.Contains(src, "\r\n")
	normSrc := strings.ReplaceAll(src, "\r\n", "\n")
	normOld := strings.ReplaceAll(oldStr, "\r\n", "\n")
	normNew := strings.ReplaceAll(newStr, "\r\n", "\n")

	n := strings.Count(normSrc, normOld)
	if n == 0 {
		return Result{}, editFileMissMessage(absPath, normSrc, normOld, wasCRLF != strings.Contains(oldStr, "\r\n"))
	}
	if n > 1 && !replaceAll {
		return Result{}, editFileAmbiguityMessage(normSrc, normOld, n)
	}

	replacedN := 1
	updatedNorm := strings.Replace(normSrc, normOld, normNew, 1)
	if replaceAll {
		replacedN = n
		updatedNorm = strings.ReplaceAll(normSrc, normOld, normNew)
	}

	// Restore the file's original per-line newline style so the edit
	// stays a diff of content, not an accidental whole-file EOL rewrite.
	updated := updatedNorm
	if wasCRLF {
		updated = restoreOriginalLineEndings(src, updatedNorm)
	}

	if err := writeFileAtomic(absPath, []byte(updated), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("file edited (%d replacement%s)", replacedN, plural(replacedN)),
		Data: map[string]any{
			"path":         PathRelativeToRoot(req.ProjectRoot, absPath),
			"replacements": replacedN,
		},
	}, nil
}

// editFileMissMessage crafts a specific "old_string not found" error
// that actually tells the agent *why* the match failed. Zero-context
// errors burn tool rounds — the agent retries the same input and fails
// identically. The hints here (whitespace-trim fuzzy match, CRLF
// mismatch, unique-prefix anchor) steer the retry toward the real
// problem.
func editFileMissMessage(absPath, haystack, needle string, crlfMismatch bool) error {
	var hints []string

	trimmedNeedle := strings.TrimSpace(needle)
	if trimmedNeedle != needle && trimmedNeedle != "" {
		if strings.Contains(haystack, trimmedNeedle) {
			hints = append(hints, "a trimmed form of old_string matches — leading/trailing whitespace in old_string differs from the file")
		}
	}

	// Check whether the first non-trivial line of the needle appears
	// anywhere — helps the agent anchor the retry.
	firstLine := ""
	for _, line := range strings.Split(needle, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			firstLine = line
			break
		}
	}
	if firstLine != "" && !strings.Contains(haystack, firstLine) {
		hints = append(hints, "first non-empty line of old_string doesn't appear verbatim — the indentation may be off")
	}

	if crlfMismatch {
		hints = append(hints, "file uses CRLF line endings; supply old_string with the same line endings or rely on the tool's auto-normalization (already attempted)")
	}

	if strings.Contains(haystack, "\t") && !strings.Contains(needle, "\t") {
		hints = append(hints, "file contains tab indentation; old_string may be using spaces")
	}

	base := fmt.Sprintf("old_string not found in %s", absPath)
	if len(hints) == 0 {
		return fmt.Errorf("%s — re-read the file and copy the exact lines you want to replace", base)
	}
	return fmt.Errorf("%s: %s", base, strings.Join(hints, "; "))
}

// editFileAmbiguityMessage tells the agent exactly how many matches
// were found and the line numbers of the first few, so the retry can
// either pick a more specific old_string or set replace_all=true
// intentionally.
func editFileAmbiguityMessage(haystack, needle string, count int) error {
	offsets := make([]int, 0, 3)
	seen := map[int]struct{}{}
	idx := 0
	for len(offsets) < 3 {
		hit := strings.Index(haystack[idx:], needle)
		if hit < 0 {
			break
		}
		lineNum := 1 + strings.Count(haystack[:idx+hit], "\n")
		if _, ok := seen[lineNum]; !ok {
			seen[lineNum] = struct{}{}
			offsets = append(offsets, lineNum)
		}
		idx += hit + len(needle)
	}
	locations := make([]string, 0, len(offsets))
	for _, l := range offsets {
		locations = append(locations, fmt.Sprintf("line %d", l))
	}
	loc := strings.Join(locations, ", ")
	if loc != "" {
		loc = " (" + loc + ")"
	}
	return fmt.Errorf("old_string is not unique: %d matches%s — extend it with surrounding lines for a unique anchor, or pass replace_all=true", count, loc)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func restoreOriginalLineEndings(original, updatedNorm string) string {
	_, endings := splitLinesAndEndings(original)
	if len(endings) == 0 {
		return strings.ReplaceAll(updatedNorm, "\n", "\r\n")
	}
	defaultEnding := dominantLineEnding(endings)
	updatedLines, trailingNewline := splitNormalizedLines(updatedNorm)
	if len(updatedLines) == 0 {
		return ""
	}

	var b strings.Builder
	for i, line := range updatedLines {
		b.WriteString(line)
		if i == len(updatedLines)-1 && !trailingNewline {
			continue
		}
		ending := defaultEnding
		if i < len(endings) && endings[i] != "" {
			ending = endings[i]
		}
		if ending == "" {
			ending = defaultEnding
		}
		b.WriteString(ending)
	}
	return b.String()
}

func splitLinesAndEndings(s string) ([]string, []string) {
	if s == "" {
		return nil, nil
	}
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	endings := make([]string, 0, cap(lines))
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		end := i
		ending := "\n"
		if i > start && s[i-1] == '\r' {
			end = i - 1
			ending = "\r\n"
		}
		lines = append(lines, s[start:end])
		endings = append(endings, ending)
		start = i + 1
	}
	if start < len(s) {
		lines = append(lines, s[start:])
		endings = append(endings, "")
	}
	return lines, endings
}

func splitNormalizedLines(s string) ([]string, bool) {
	if s == "" {
		return []string{""}, false
	}
	trailingNewline := strings.HasSuffix(s, "\n")
	trimmed := strings.TrimSuffix(s, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines, trailingNewline
}

func dominantLineEnding(endings []string) string {
	crlf := 0
	lf := 0
	for _, ending := range endings {
		switch ending {
		case "\r\n":
			crlf++
		case "\n":
			lf++
		}
	}
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
}
