package drive

// scheduler_scope.go — file-scope conflict machinery + lane / exclusivity
// rules used by readyBatch's parallel pick. The conflict checker keeps
// two TODOs that target the same file from running in parallel (race
// on edit_file/write_file plus a torn read-before-mutate snapshot);
// the lane checker enforces per-lane caps and the "verify-class TODOs
// run exclusively" rule. Sibling to scheduler.go which owns the picker
// itself (readyBatch / readyBatchWithPolicy / depsReady / skip /
// runFinished).

import (
	"path"
	"strings"
)

// scopeSet is a normalized representation of file_scope used by the
// conflict checks. Forward-slash normalization is applied on insert
// so `internal\foo.go` and `internal/foo.go` collide. The sentinel
// scopeAny ("") means "the TODO did not declare scope — treat as
// exclusive owner of every file" (used by the empty-scope rule).
type scopeSet map[string]struct{}

const scopeAny = ""

// collectScopes builds a scopeSet from every TODO matching `match`.
// When any matching TODO has empty FileScope, scopeAny is added
// (which makes every subsequent conflict check return true) unless the
// TODO is explicitly read-only.
func collectScopes(todos []Todo, match func(Todo) bool) scopeSet {
	out := scopeSet{}
	for _, t := range todos {
		if !match(t) {
			continue
		}
		if len(t.FileScope) == 0 {
			if !todoReadOnly(t) {
				out[scopeAny] = struct{}{}
			}
			continue
		}
		for _, f := range t.FileScope {
			out[normalizeScope(f)] = struct{}{}
		}
	}
	return out
}

// mergeScopes adds the normalized entries from todo into base and
// returns base. When FileScope is empty, scopeAny is recorded unless
// the TODO is read-only.
func mergeScopes(base scopeSet, todo Todo) scopeSet {
	if len(todo.FileScope) == 0 {
		if todoReadOnly(todo) {
			return base
		}
		base[scopeAny] = struct{}{}
		return base
	}
	for _, f := range todo.FileScope {
		base[normalizeScope(f)] = struct{}{}
	}
	return base
}

// scopeConflicts reports whether candidate's file set intersects the
// set in held. The rules:
//   - Either side containing scopeAny is a conflict (unscoped owns all).
//   - Otherwise, conflict iff any candidate path *overlaps* a held path,
//     where overlap means equal OR one is a directory ancestor of the
//     other (see pathsOverlap). Plain exact-equality missed the common
//     planner shape of a directory scope ("internal/auth") declared
//     alongside a file inside it ("internal/auth/service.go"): the two
//     normalize to different strings, so the scheduler would dispatch
//     both at once and race on service.go — the exact corruption
//     readyBatch promises to prevent.
func scopeConflicts(candidate Todo, held scopeSet) bool {
	if len(held) == 0 {
		return false
	}
	if _, any := held[scopeAny]; any {
		// held has an unscoped owner — conflicts with anyone.
		return true
	}
	if len(candidate.FileScope) == 0 {
		if todoReadOnly(candidate) {
			return false
		}
		// candidate is unscoped but held is non-empty (scoped) —
		// candidate would conflict with all held files; treat as
		// conflict so unscoped TODOs queue up behind scoped ones.
		return true
	}
	for _, f := range candidate.FileScope {
		cf := normalizeScope(f)
		for h := range held {
			if h == scopeAny {
				continue // handled above; never an overlap target
			}
			if pathsOverlap(cf, h) {
				return true
			}
		}
	}
	return false
}

// pathsOverlap reports whether two normalized scope paths touch the same
// filesystem region: identical, the repo root ("." owns everything), or
// one a directory ancestor of the other. Ancestry is matched on a path
// SEGMENT boundary (via the trailing "/") so "internal/auth" contains
// "internal/auth/service.go" but NOT "internal/authz/service.go" — a
// plain strings.HasPrefix would wrongly serialize the latter pair.
func pathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	if a == "." || b == "." {
		// Repo root scope subsumes every path in the tree.
		return true
	}
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

func todoReadOnly(t Todo) bool {
	if t.ReadOnly {
		return true
	}
	if len(t.AllowedTools) > 0 {
		return !allowsMutatingTools(t.AllowedTools)
	}
	switch strings.ToLower(strings.TrimSpace(t.Kind)) {
	case "survey":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(t.WorkerClass)) {
	case "planner", "researcher":
		return true
	default:
		return false
	}
}

// normalizeScope maps a FileScope entry onto a canonical form so
// semantically-equal paths ("main.go", "./main.go", "main.go" with a
// Windows backslash, "a//b.go") collapse into the same scopeSet key.
// Without this pass a planner that emits "./pkg/foo.go" for one TODO
// and "pkg/foo.go" for another would have both run in parallel and
// corrupt each other's writes. Empty input is preserved (not rewritten
// to path.Clean's "." sentinel) because callers use "" as an explicit
// "no scope" marker.
func normalizeScope(s string) string {
	if s == "" {
		return ""
	}
	slashed := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			c = '/'
		}
		slashed = append(slashed, c)
	}
	cleaned := path.Clean(string(slashed))
	if cleaned == "." {
		// path.Clean turns bare "./" / "" into "." — keep as "." since
		// that's a legitimate scope (the repo root itself).
		return "."
	}
	return cleaned
}

func todoLane(t Todo) string {
	switch strings.ToLower(strings.TrimSpace(t.WorkerClass)) {
	case "planner", "researcher":
		return "discovery"
	case "reviewer":
		return "review"
	case "tester", "security":
		return "verify"
	case "synthesizer":
		return "synthesize"
	}
	switch strings.ToLower(strings.TrimSpace(t.ProviderTag)) {
	case "research", "plan":
		return "discovery"
	case "review":
		return "review"
	case "test":
		return "verify"
	case "synthesize":
		return "synthesize"
	default:
		return "code"
	}
}

func laneCap(policy SchedulerPolicy, lane string) int {
	if len(policy.LaneCaps) == 0 {
		return 0
	}
	return policy.LaneCaps[strings.ToLower(strings.TrimSpace(lane))]
}

func countRunningLanes(todos []Todo) map[string]int {
	out := map[string]int{}
	for _, t := range todos {
		if t.Status != TodoRunning {
			continue
		}
		out[todoLane(t)]++
	}
	return out
}

func todoNeedsExclusiveSlot(t Todo) bool {
	if strings.EqualFold(strings.TrimSpace(t.Kind), "verify") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(t.WorkerClass)) {
	case "reviewer", "tester", "security":
		return true
	default:
		return false
	}
}

func containsLane(items []string, needle string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func hasAnyRunningTodo(todos []Todo) bool {
	for _, t := range todos {
		if t.Status == TodoRunning {
			return true
		}
	}
	return false
}

func hasRunningExclusiveTodo(todos []Todo) bool {
	for _, t := range todos {
		if t.Status == TodoRunning && todoNeedsExclusiveSlot(t) {
			return true
		}
	}
	return false
}
