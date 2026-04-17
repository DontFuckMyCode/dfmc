// Typo suggestion for the top-level CLI dispatch. The default branch
// used to route any unknown first token to `dfmc ask`, which turned a
// simple typo like `dfmc docter` into a silent LLM query about the
// word "docter". This file adds a narrow typo guard: when the first
// token looks like it's *trying* to be a command verb, we print a
// suggestion and exit non-zero so the user can correct course instead
// of burning tokens on a nonsense question.

package cli

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/commands"
)

// knownCLICommands is the master list used both for typo suggestions and
// the "does this look like a verb" heuristic. Keep in sync with the
// switch in Run(); `internal/commands` registry is the authoritative
// source but the switch adds a few TUI-originated aliases too.
func knownCLICommands() []string {
	seen := map[string]struct{}{}
	reg := commands.DefaultRegistry()
	for _, c := range reg.All() {
		if name := strings.TrimSpace(strings.ToLower(c.Name)); name != "" {
			seen[name] = struct{}{}
		}
		for _, alias := range c.Aliases {
			if a := strings.TrimSpace(strings.ToLower(alias)); a != "" {
				seen[a] = struct{}{}
			}
		}
	}
	// CLI-side synonyms that don't live in the shared registry.
	for _, extra := range []string{"conv", "providers", "model", "provider"} {
		seen[extra] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// looksLikeCommandTypo answers "should we bother typo-checking this
// input?" We only want to guard single-token, short, alphabetic inputs
// — a long multi-word question is clearly meant for `ask` and should
// pass through without noise.
//
// The heuristic: first token is <= 14 chars, alphabetic-ish, and either
// (a) there are no more tokens (user typed `dfmc dcter`) or (b) the
// whole input is dominated by that first token (no quoted question,
// no special characters). Anything longer or punctuated flows through
// to the LLM the way it always did.
func looksLikeCommandTypo(cmd string, rest []string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || len(cmd) > 14 {
		return false
	}
	for _, r := range cmd {
		// Allow letters and the single hyphen — nothing exotic.
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '-':
		default:
			return false
		}
	}
	// Prior-art guard: if the user actually wrote a full sentence the
	// first token would be followed by a lot more content, and the
	// intent is "ask a question". We only typo-check when the first
	// token is the entire input OR the remainder is very short (user
	// typed `dfmc docter file.go` meaning `dfmc doctor file.go`).
	if len(rest) <= 2 {
		return true
	}
	return false
}

// suggestCLICommand returns the closest known CLI verb by simple prefix
// / substring / one-edit-distance matching. Returns "" when nothing
// plausible is within reach — the caller treats that as "pass through
// to ask". We deliberately keep the heuristic conservative to avoid
// false positives on short real questions ("go", "run", "test" etc.).
func suggestCLICommand(cmd string) string {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if cmd == "" {
		return ""
	}
	candidates := knownCLICommands()
	// Prefix match beats everything — `dfmc anal` → analyze.
	for _, c := range candidates {
		if strings.HasPrefix(c, cmd) && c != cmd {
			return c
		}
	}
	// Substring — `dfmc ocator` shouldn't match, but `dfmc octor` should.
	for _, c := range candidates {
		if strings.Contains(c, cmd) && len(cmd) >= 3 {
			return c
		}
	}
	// One-edit-distance rescue: `dfmc docter` → `doctor`, `dfmc intit`
	// → `init`. Limit to cmd length 4+ so 3-char inputs don't explode
	// into random matches.
	if len(cmd) >= 4 {
		for _, c := range candidates {
			if editDistanceAtMost(cmd, c, 1) && c != cmd {
				return c
			}
		}
	}
	return ""
}

// editDistanceAtMost reports whether a and b differ by at most maxEdits
// single-character insertions, deletions, substitutions, or adjacent
// transpositions (Damerau-style for maxEdits=1, the common case).
// Tailored to our typo heuristic so we don't ship a full Damerau-
// Levenshtein matrix for what's effectively a one-edit check.
func editDistanceAtMost(a, b string, maxEdits int) bool {
	if maxEdits < 0 {
		return false
	}
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > maxEdits || lb-la > maxEdits {
		return false
	}
	// Adjacent transposition short-circuit — "memroy" ↔ "memory" differs
	// by a single swap, which a naive edit-distance walk over-counts as
	// two substitutions. Only consider this when lengths match; other
	// length mismatches already route through the insert/delete logic
	// below.
	if la == lb && maxEdits >= 1 {
		if i, ok := firstDiffIndex(a, b); ok && i+1 < la {
			if a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:] {
				return true
			}
		}
	}
	// Walk both strings until we hit a divergence, then decide whether
	// the remaining tails can be reconciled with one edit.
	i, j, edits := 0, 0, 0
	for i < la && j < lb {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > maxEdits {
			return false
		}
		switch {
		case la > lb:
			// a has an extra char — skip it.
			i++
		case lb > la:
			// b has an extra char — skip it.
			j++
		default:
			// Substitution — advance both.
			i++
			j++
		}
	}
	// Trailing characters count as edits.
	edits += (la - i) + (lb - j)
	return edits <= maxEdits
}

// firstDiffIndex returns the first position at which a and b differ, or
// (0, false) when they are identical. Used by the adjacent-transposition
// shortcut in editDistanceAtMost.
func firstDiffIndex(a, b string) (int, bool) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i, true
		}
	}
	return 0, false
}
