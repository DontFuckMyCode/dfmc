package context

// trajectory_detect.go — pattern-detection helpers used by the
// TrajectoryHints rule engine. These functions look back across the
// running trace history and pick out repeated calls, repeated failures,
// and the running unvalidated-mutation streak. None of them mutate
// state — they read traces and return summaries the rule engine
// composes into hints. Small string-canonicalization helpers
// (errorFingerprint, canonicalArgFingerprint) live here too because
// they're only used by the detectors.
//
// Validation-verb recognition (isValidationVerb) lives here because
// it's coupled to countUnvalidatedMutations — both surfaces answer
// the same "did this command count as a verification pass?" question.

import (
	"fmt"
	"strings"
)

// detectRepeatedCalls returns the name of a tool that was called 3+ times
// in the last ~6 traces with overlapping argument values. Empty string
// when nothing looks repetitive.
func detectRepeatedCalls(all []TraceEntry) string {
	if len(all) < 3 {
		return ""
	}
	window := all
	if len(window) > 6 {
		window = window[len(window)-6:]
	}
	counts := map[string]int{}
	argSeen := map[string]map[string]struct{}{}
	for _, t := range window {
		if !t.Ok {
			continue
		}
		name := t.EffectiveTool()
		counts[name]++
		if argSeen[name] == nil {
			argSeen[name] = map[string]struct{}{}
		}
		argSeen[name][canonicalArgFingerprint(t.Args)] = struct{}{}
	}
	for name, n := range counts {
		if n < 3 {
			continue
		}
		// Only flag when there's argument overlap (same fingerprint ≥ twice).
		unique := len(argSeen[name])
		if unique <= n-1 {
			return name
		}
	}
	return ""
}

// repeatedFailure summarizes a "same tool keeps failing" pattern detected
// across the running history window. errSample is the first failure's
// error fingerprint (first ~80 chars) — naming the error class helps the
// model recognise the pattern instead of just being told "stop".
type repeatedFailure struct {
	tool      string
	count     int
	errSample string
}

// detectRepeatedFailures returns the effective tool name that failed 3+
// times across the last ~8 traces with similar error fingerprints. The
// window is wider than detectRepeatedCalls (6) because failure loops
// often interleave a recovery attempt or two, and we want to catch the
// pattern before it has burned 10+ rounds. Empty when no such pattern.
//
// "Similar error fingerprint" means the first ~30 chars of the error
// message (lowercased, whitespace-collapsed) are identical for at least
// 2 of the failures — this catches genuine "same-cause" loops without
// false-positiving on legitimate retries that hit different errors.
func detectRepeatedFailures(all []TraceEntry) repeatedFailure {
	if len(all) < 3 {
		return repeatedFailure{}
	}
	window := all
	if len(window) > 8 {
		window = window[len(window)-8:]
	}
	type bucket struct {
		count    int
		errs     map[string]int
		firstErr string
	}
	buckets := map[string]*bucket{}
	for _, t := range window {
		if t.Ok {
			continue
		}
		name := t.EffectiveTool()
		if name == "" {
			continue
		}
		b, ok := buckets[name]
		if !ok {
			b = &bucket{errs: map[string]int{}}
			buckets[name] = b
		}
		b.count++
		fp := errorFingerprint(t.Err)
		b.errs[fp]++
		if b.firstErr == "" {
			b.firstErr = firstLine(t.Err)
		}
	}
	for name, b := range buckets {
		if b.count < 3 {
			continue
		}
		// At least one fingerprint must repeat — otherwise the failures
		// are unrelated and a "stop retrying" hint would be wrong advice.
		repeats := false
		for _, n := range b.errs {
			if n >= 2 {
				repeats = true
				break
			}
		}
		if !repeats {
			continue
		}
		sample := b.firstErr
		if sample == "" {
			sample = "unknown error"
		}
		return repeatedFailure{tool: name, count: b.count, errSample: sample}
	}
	return repeatedFailure{}
}

// countUnvalidatedMutations walks the running trace history and returns
// the number of distinct successful edits since the last successful
// validation command (build/test/vet/etc), plus the per-file path list
// (de-duped, in first-seen order). Walking history-wide rather than the
// single round is exactly what makes the rule state-aware: a model
// editing one file per round across 5 rounds will see count=5 even
// though each round's `fresh` only has one mutation.
//
// "Validation command" is recognised by the leading verb of the
// run_command call's `command` arg — kept in rough sync with the TUI's
// isValidationCommand. Any successful run_command matching this set
// resets the count, so the helper effectively returns "edits since the
// most recent good test pass". When no validation has ever happened
// the count covers every successful mutation in the history.
func countUnvalidatedMutations(all []TraceEntry) (int, []string) {
	if len(all) == 0 {
		return 0, nil
	}
	// Walk forward, but reset on each validation. Final count = edits
	// since last validation event (or beginning, whichever later).
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(all))
	for _, t := range all {
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file", "write_file", "apply_patch":
			path := strings.TrimSpace(argAsString(t.Args, "path"))
			if path == "" {
				path = strings.TrimSpace(argAsString(t.Args, "file"))
			}
			// apply_patch may not carry a single path arg; fall back to
			// a synthetic placeholder so the count is still accurate.
			if path == "" {
				path = fmt.Sprintf("(patch@step%d)", t.Step)
			}
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		case "run_command":
			cmd := strings.TrimSpace(argAsString(t.Args, "command"))
			if isValidationVerb(cmd) {
				seen = map[string]struct{}{}
				paths = paths[:0]
			}
		}
	}
	return len(paths), paths
}

// isValidationVerb mirrors the TUI's isValidationCommand. Kept here so
// the trajectory layer is self-contained (no UI imports). Both surfaces
// answer the same question — "did this command count as a verification
// pass?" — so update them together when adding a new test runner.
func isValidationVerb(cmd string) bool {
	cmd = strings.TrimSpace(strings.ToLower(cmd))
	if cmd == "" {
		return false
	}
	multi := []string{
		"go test", "go vet", "go build",
		"npm test", "pnpm test", "yarn test",
		"npm run test", "pnpm run test", "yarn run test",
		"cargo test", "cargo check", "cargo build", "cargo clippy",
	}
	for _, prefix := range multi {
		if cmd == prefix || strings.HasPrefix(cmd, prefix+" ") {
			return true
		}
	}
	single := []string{"pytest", "tsc", "eslint", "biome", "mypy", "ruff", "make"}
	for _, prefix := range single {
		if cmd == prefix || strings.HasPrefix(cmd, prefix+" ") {
			return true
		}
	}
	return false
}

// errorFingerprint normalizes an error message for repeat-detection. We
// keep the leading prefix (the error CLASS — "file not found", "no such
// file", "permission denied") and drop trailing path/value tails that
// vary per call. Lowercase + collapsed whitespace makes "File not Found"
// and "file not  found" hash the same.
func errorFingerprint(err string) string {
	s := strings.ToLower(strings.TrimSpace(err))
	if s == "" {
		return ""
	}
	// Collapse runs of whitespace to single spaces.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// canonicalArgFingerprint returns a stable string for similar-looking args.
// We deliberately strip long values so "read file A lines 1-20" and
// "read file A lines 40-60" count as the same fingerprint when the path is
// the same (repeated file-bouncing is a bad pattern either way).
func canonicalArgFingerprint(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := []string{"path", "file", "pattern", "query", "command", "name"}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(argAsString(args, k))
		if v == "" {
			continue
		}
		if len(v) > 48 {
			v = v[:48]
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "|")
}
