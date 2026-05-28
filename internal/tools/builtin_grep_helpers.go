package tools

// builtin_grep_helpers.go — RE2-error self-teaching hints (turns the
// model's PCRE habit into a one-line "use this RE2 form" suggestion)
// and the small top-level-only `.gitignore` reader. Companion
// siblings:
//
//   - builtin_grep.go      GrepCodebaseTool + Execute + walker +
//                          splitGlobList + anyGlobMatches +
//                          formatGrepBlock + matchHit + constants
//   - builtin_grep_scan.go grepFileMatches (no-context fast path) +
//                          grepFileMatchesWithContext (sliding
//                          before/after window with deferred-block
//                          machinery)

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func formatGrepRegexError(pattern string, err error) error {
	original := err.Error()
	hint := grepRE2Hint(pattern, original)
	if hint == "" {
		return fmt.Errorf("invalid regex pattern %q: %w. grep_codebase uses Go RE2 syntax (https://github.com/google/re2/wiki/Syntax) — Perl/PCRE features like lookbehind, backrefs, possessive quantifiers, and named groups `(?P<name>...)` are NOT supported", pattern, err)
	}
	return fmt.Errorf("invalid regex pattern %q: %w. %s", pattern, err, hint)
}

// grepRE2Hint maps the most common "model wrote PCRE" mistakes to a
// one-line "use this RE2 form instead" suggestion. Empty when the
// pattern doesn't match a known footgun — caller falls back to the
// generic RE2 link.
// knownCatastrophicRE tracks regex patterns that cause exponential
// backtracking in RE2. These are self-DOS vectors — the model
// generating them would only hurt its own session.
var knownCatastrophicRE = []string{
	`\(.\+\)\+\$`,     // (a+)+$
	`\(.\+\)\*\$`,     // (a+)*$
	`\(.\+\)\?\$`,     // (a+)?$
	`\.\*\(.\+\)\+\$`, // .*(a+)+$
}

// isLikelyCatastrophic returns true if pattern contains constructs
// known to cause exponential backtracking in RE2. The check is cheap
// and prevents the model from hanging its own session with a crafted
// pattern like "^(a+)+$".
func isLikelyCatastrophic(pattern string) bool {
	for _, c := range knownCatastrophicRE {
		if strings.Contains(pattern, c) {
			return true
		}
	}
	return false
}

func grepRE2Hint(pattern, errMsg string) string {
	switch {
	case strings.Contains(pattern, "(?P<"):
		return `RE2 uses (?P<name>...) the same way Python does — but if you're seeing this error you may have nested or unsupported group flags. Try the unnamed (...) form, then index by group number.`
	case strings.Contains(pattern, "(?<=") || strings.Contains(pattern, "(?<!"):
		return `Lookbehind ((?<=...) / (?<!...)) is NOT supported in RE2. Restructure: match the surrounding context with a capturing group instead, or filter the matches in a follow-up step.`
	case strings.Contains(pattern, "(?=") || strings.Contains(pattern, "(?!"):
		return `Lookahead ((?=...) / (?!...)) is NOT supported in RE2. Match the full sequence and post-filter, or use a non-capturing group (?:...) where you don't need consumption.`
	case strings.Contains(pattern, `\1`) || strings.Contains(pattern, `\2`) || strings.Contains(pattern, `\3`):
		return `Backreferences (\1, \2, ...) are NOT supported in RE2 — RE2 guarantees linear-time matching, which precludes them. Match the candidates and check equality in a follow-up.`
	case strings.Contains(pattern, "*+") || strings.Contains(pattern, "++") || strings.Contains(pattern, "?+"):
		return `Possessive quantifiers (*+, ++, ?+) are NOT supported in RE2. Use the regular greedy quantifier — RE2's matching algorithm doesn't backtrack so possessives are unnecessary.`
	case strings.Contains(errMsg, "missing closing"):
		return `An opening bracket / parenthesis is unclosed. Check that every (, [, {, "..." has a matching close.`
	case strings.Contains(errMsg, "invalid character class") || strings.Contains(errMsg, "invalid escape sequence"):
		return `An escape inside a character class or in the body is invalid. RE2 supports \d \w \s \b but not \K \z \Z \v in the same way as Perl. Stick to literals + \d \w \s [a-z] [^...] for portability.`
	}
	return ""
}

// gitignoreMatcher is a small, top-level-only `.gitignore` reader. It
// handles the common shapes: blank/comment lines skipped, trailing `/`
// flags directory-only patterns, leading `/` anchors to the root, `**`
// for any-depth, and a basename match for bare patterns. Negation (`!`)
// and per-subdir `.gitignore` files are NOT handled — the cost of a full
// gitignore implementation isn't worth it for an LLM grep helper. The
// hardcoded skip set (.git, node_modules, vendor, bin, dist) catches the
// 90% case anyway; this layer adds project-specific ignores on top.
type gitignoreMatcher struct {
	dirPatterns  []string
	filePatterns []string
}

func loadGitignore(root string) *gitignoreMatcher {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	m := &gitignoreMatcher{}
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			// Negation — skip; full gitignore semantics aren't worth
			// implementing for the LLM grep path.
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		// Strip leading `/` (anchored) — we always match from the project
		// root anyway, so the anchor doesn't change behaviour for our
		// use case.
		line = strings.TrimPrefix(line, "/")
		if dirOnly {
			m.dirPatterns = append(m.dirPatterns, line)
		} else {
			m.filePatterns = append(m.filePatterns, line)
		}
	}
	return m
}

func (m *gitignoreMatcher) matchDir(relSlash string) bool {
	if m == nil {
		return false
	}
	base := filepath.Base(relSlash)
	for _, p := range m.dirPatterns {
		if matchGitignorePattern(p, relSlash, base) {
			return true
		}
	}
	for _, p := range m.filePatterns {
		// Bare patterns also catch directories with the same name.
		if matchGitignorePattern(p, relSlash, base) {
			return true
		}
	}
	return false
}

func (m *gitignoreMatcher) matchFile(relSlash string) bool {
	if m == nil {
		return false
	}
	base := filepath.Base(relSlash)
	for _, p := range m.filePatterns {
		if matchGitignorePattern(p, relSlash, base) {
			return true
		}
	}
	return false
}

func matchGitignorePattern(pattern, relSlash, base string) bool {
	pattern = filepath.ToSlash(pattern)
	doublestar := strings.Contains(pattern, "**")
	if globMatch(pattern, relSlash, doublestar) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		// Bare basename pattern matches anywhere in the tree.
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
	}
	return false
}
