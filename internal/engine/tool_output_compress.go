package engine

// tool_output_compress.go — RTK-inspired (Rust Token Killer) noise filter
// applied to every tool_result payload before it lands back in the native
// tool loop. The goal is to strip the formatting cruft LLMs don't need
// (progress bars, spinner frames, ANSI escapes, repeated lines) while
// preserving every piece of signal — error messages, file paths, hashes,
// counts, diff hunks.
//
// All rules are conservative: when in doubt, keep the line. False drops
// cost more than false keeps — a missed warning can hide a real bug.

import (
	"regexp"
	"strings"
)

// ansiEscapeRE matches the common CSI/SGR color+cursor escape sequences
// (`ESC [ ... m`, `ESC [ ... K`, etc.) that TUI-friendly tools embed even
// when stdout isn't a TTY. The agent loop doesn't render these, so they
// burn tokens for nothing.
var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// noiseLineREs are patterns that, when they match *alone* on a line,
// identify progress/metadata chatter that carries no debugging value. We
// match full lines (anchored with trimmed content) so a real error like
// "Counting objects failed because disk full" still gets through.
var noiseLineREs = []*regexp.Regexp{
	regexp.MustCompile(`^Enumerating objects:\s+\d+`),
	regexp.MustCompile(`^Counting objects:\s+\d+%`),
	regexp.MustCompile(`^Compressing objects:\s+\d+%`),
	regexp.MustCompile(`^Writing objects:\s+\d+%`),
	regexp.MustCompile(`^Delta compression using up to \d+ threads?`),
	regexp.MustCompile(`^Total \d+ \(delta \d+\), reused \d+ \(delta \d+\)`),
	regexp.MustCompile(`^remote: (?:Enumerating|Counting|Compressing|Total) `),
	// npm/yarn/pnpm progress frames that survive when TERM=dumb is absent.
	regexp.MustCompile(`^\[#+\s*\]\s+.+\s+\d+%$`),
	regexp.MustCompile(`^Progress: resolved \d+, reused \d+`),
	// Spinner residue — unicode or ascii frames with a short trailing label.
	regexp.MustCompile(`^[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏|/\-\\]\s+\S`),
}

// compressToolResult runs the full compression pass on a tool output string.
// It is idempotent — running twice produces the same result as running once.
// Returns the cleaned text; an empty input maps to empty output.
func compressToolResult(raw string) string {
	if raw == "" {
		return ""
	}
	// Normalize line endings so later regex + split steps see one format.
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	// A bare `\r` is a carriage return used for in-place progress updates.
	// Replace with `\n` so the noise-filter can drop the spinner line.
	text = strings.ReplaceAll(text, "\r", "\n")
	text = ansiEscapeRE.ReplaceAllString(text, "")

	lines := strings.Split(text, "\n")
	// First pass: drop exact noise matches, keep everything else verbatim.
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			filtered = append(filtered, "")
			continue
		}
		if isNoiseLine(trimmed) {
			continue
		}
		filtered = append(filtered, line)
	}

	// Second pass: collapse runs of identical trimmed lines (≥3 in a row)
	// into `line (×N)`. Threshold ≥3 is intentional — a pair of identical
	// lines might be a stutter the model needs to see ("warning X" twice =
	// two spots in code), but ten repeats are always spam.
	compacted := make([]string, 0, len(filtered))
	i := 0
	for i < len(filtered) {
		cur := filtered[i]
		j := i + 1
		for j < len(filtered) && strings.TrimSpace(filtered[j]) == strings.TrimSpace(cur) && strings.TrimSpace(cur) != "" {
			j++
		}
		run := j - i
		if run >= 3 {
			compacted = append(compacted, strings.TrimRight(cur, " \t")+" (×"+itoa(run)+")")
		} else {
			compacted = append(compacted, filtered[i:j]...)
		}
		i = j
	}

	// Third pass: collapse ≥3 consecutive blank lines to 1 blank. One gap
	// is often semantically meaningful (section break); three in a row is
	// just whitespace the model doesn't need.
	squeezed := make([]string, 0, len(compacted))
	blankRun := 0
	for _, line := range compacted {
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun > 1 {
				continue
			}
		} else {
			blankRun = 0
		}
		squeezed = append(squeezed, line)
	}

	// Strip trailing blanks — they contribute nothing and cost bytes.
	for len(squeezed) > 0 && strings.TrimSpace(squeezed[len(squeezed)-1]) == "" {
		squeezed = squeezed[:len(squeezed)-1]
	}
	return strings.Join(squeezed, "\n")
}

func isNoiseLine(trimmed string) bool {
	for _, re := range noiseLineREs {
		if re.MatchString(trimmed) {
			return true
		}
	}
	return false
}

// itoa is a tiny int formatter used to avoid the strconv import on a hot
// path. Only called with small positive integers (run lengths).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
