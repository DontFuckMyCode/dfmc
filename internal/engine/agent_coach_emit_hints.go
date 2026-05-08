package engine

// agent_coach_emit_hints.go — pure helpers used by the coach observer:
// validation/tighten/retrieval hint recommenders, identifier/path
// classification, project-type detection (cached), and small
// shell-quote/string utilities.
//
// Sibling of agent_coach_emit.go which keeps the Engine method receivers
// (coachObserver / emitCoachNotes / buildCoachSnapshot /
// projectRootForCoach) plus the coach package wiring. Splitting helpers
// out keeps the lifecycle file focused on the Engine→coach handshake.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// recommendCoachValidationHint returns a one-liner suggesting the smallest
// validation command to run after a mutating turn. Project-type aware
// (Go/Rust/Node/Python); falls back to "re-open the touched file" when
// the project type is unknown.
func recommendCoachValidationHint(projectRoot string, contextChunks []types.ContextChunk, mutations []string) string {
	root := strings.TrimSpace(projectRoot)
	firstMutation := firstNonEmptyPath(mutations)
	projectType := detectCoachProjectType(root)

	if projectType == "go" {
		if firstMutation != "" {
			dir := normalizeCoachPath(filepath.Dir(firstMutation))
			if dir != "." && dir != "" {
				return fmt.Sprintf("run `go test %s -count=1`", shellQuoteIfNeeded("./"+strings.TrimPrefix(dir, "./")+"/..."))
			}
		}
		return "run `go test ./... -count=1`"
	}
	if projectType == "rust" {
		return "run `cargo test`"
	}
	if projectType == "node" {
		return "run `npm test`"
	}
	if projectType == "python" {
		if firstMutation != "" && strings.HasSuffix(strings.ToLower(firstMutation), ".py") {
			return fmt.Sprintf("run `pytest %s -q`", shellQuoteIfNeeded(firstMutation))
		}
		return "run `pytest -q`"
	}

	if path := firstContextPath(contextChunks); path != "" {
		return fmt.Sprintf("re-open `[[file:%s]]` and run the smallest relevant verification command", path)
	}
	return ""
}

// recommendCoachTightenHint suggests a narrower follow-up when the turn
// was sprawling — either the touched file, the best context-chunk path,
// or "split the task" when the token spend crossed the high-token mark.
func recommendCoachTightenHint(question string, contextChunks []types.ContextChunk, mutations []string, tokensUsed int, identifiers []string) string {
	if path := firstNonEmptyPath(mutations); path != "" {
		return narrowCoachReviewHint(path, identifiers)
	}
	if path := bestCoachContextPath(contextChunks); path != "" {
		return narrowCoachReviewHint(path, identifiers)
	}
	if tokensUsed > coachHighTokenThreshold && strings.TrimSpace(question) != "" {
		return "use `/split` to break the task into smaller passes"
	}
	return ""
}

// recommendCoachRetrievalHint nudges the user toward re-asking with a
// concrete [[file:...]] marker (and optionally a symbol name) so the
// next turn does not re-do the discovery work.
func recommendCoachRetrievalHint(question string, contextChunks []types.ContextChunk, symbol string) string {
	if coachQuestionHasFileMarker(question) {
		if symbol != "" {
			return fmt.Sprintf("retry with the existing [[file:...]] marker plus exact symbol name `%s`", symbol)
		}
		return ""
	}
	if path := bestCoachContextPath(contextChunks); path != "" {
		if symbol != "" {
			return fmt.Sprintf("retry with `review [[file:%s]] %s`", path, symbol)
		}
		return fmt.Sprintf("retry with `review [[file:%s]]` plus the exact symbol name", path)
	}
	return ""
}

func coachQuestionHasFileMarker(question string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(question)), "[[file:")
}

func narrowCoachReviewHint(path string, identifiers []string) string {
	if path == "" {
		return ""
	}
	if symbol := firstCoachIdentifier(identifiers); symbol != "" {
		return fmt.Sprintf("ask `review [[file:%s]] %s`", path, symbol)
	}
	return fmt.Sprintf("ask `review [[file:%s]]`", path)
}

// firstCoachIdentifier returns the first identifier from the user's query
// that looks like a code symbol — skipping common verbs ("review",
// "fix", ...) and path-shaped tokens that wouldn't be useful as a
// symbol name in a follow-up search.
func firstCoachIdentifier(identifiers []string) string {
	skip := map[string]struct{}{
		"review": {}, "explain": {}, "inspect": {}, "check": {}, "look": {},
		"analyze": {}, "analyse": {}, "fix": {}, "update": {}, "add": {},
		"remove": {}, "write": {}, "read": {}, "open": {}, "show": {},
		"file": {}, "path": {}, "line": {}, "lines": {}, "symbol": {},
	}
	for _, ident := range identifiers {
		if ident = strings.TrimSpace(ident); ident == "" {
			continue
		}
		if looksLikeCoachPathToken(ident) {
			continue
		}
		if _, blocked := skip[strings.ToLower(ident)]; blocked {
			continue
		}
		if ident != "" {
			return ident
		}
	}
	return ""
}

func looksLikeCoachPathToken(ident string) bool {
	if strings.ContainsAny(ident, `/\`) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(ident))
	if strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".json") {
		return true
	}
	return false
}

// bestCoachContextPath picks the most informative context-chunk path,
// preferring symbol-match / marker / query-match / graph-neighborhood /
// hotspot in that order before falling back to the first non-empty.
func bestCoachContextPath(chunks []types.ContextChunk) string {
	priorities := []string{"symbol-match", "marker", "query-match", "graph-neighborhood", "hotspot"}
	for _, source := range priorities {
		for _, ch := range chunks {
			if !strings.EqualFold(strings.TrimSpace(ch.Source), source) {
				continue
			}
			if path := normalizeCoachPath(ch.Path); path != "" {
				return path
			}
		}
	}
	return firstContextPath(chunks)
}

func firstNonEmptyPath(paths []string) string {
	for _, path := range paths {
		if path = normalizeCoachPath(path); path != "" {
			return path
		}
	}
	return ""
}

func firstContextPath(chunks []types.ContextChunk) string {
	for _, ch := range chunks {
		if path := normalizeCoachPath(ch.Path); path != "" {
			return path
		}
	}
	return ""
}

func normalizeCoachPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// detectCoachProjectType inspects the project root for a known build
// manifest (go.mod / Cargo.toml / package.json / pyproject.toml /
// requirements.txt / setup.py). Result is cached per root for the
// process lifetime.
func detectCoachProjectType(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if cached, ok := coachProjectTypeCache.Load(root); ok {
		if projectType, ok := cached.(string); ok {
			return projectType
		}
	}
	projectType := ""
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		projectType = "go"
	case fileExists(filepath.Join(root, "Cargo.toml")):
		projectType = "rust"
	case fileExists(filepath.Join(root, "package.json")):
		projectType = "node"
	case fileExists(filepath.Join(root, "pyproject.toml")) ||
		fileExists(filepath.Join(root, "requirements.txt")) ||
		fileExists(filepath.Join(root, "setup.py")):
		projectType = "python"
	}
	coachProjectTypeCache.Store(root, projectType)
	return projectType
}

func shellQuote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// shellQuoteIfNeeded skips quoting when every rune is a "safe" shell
// character (alphanumeric or any of `._/-:`). Anything else falls back
// to single-quote escaping via shellQuote.
func shellQuoteIfNeeded(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "''"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._/-:", r):
		default:
			return shellQuote(s)
		}
	}
	return s
}

func stringArg(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
