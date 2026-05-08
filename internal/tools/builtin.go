package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Param-coercion helpers (asString, asInt, asStringSlice,
// coerceStringSlice, asBool) and the maxIntValue/minIntValue
// constants live in builtin_coerce.go.

type WriteFileTool struct {
	engine *Engine
}

func NewWriteFileTool() *WriteFileTool { return &WriteFileTool{} }
func (t *WriteFileTool) Name() string  { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Write or create a text file."
}

// SetEngine wires the per-path lock so concurrent writes serialize correctly.
func (t *WriteFileTool) SetEngine(e *Engine) { t.engine = e }
func (t *WriteFileTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", "")
	content := asString(req.Params, "content", "")
	_, contentProvided := req.Params["content"]
	createDirs := asBool(req.Params, "create_dirs", true)
	overwrite := asBool(req.Params, "overwrite", false)

	// Self-teaching required-field validation. Without these the tool
	// used to silently write an empty file when the model passed only
	// `path`, or fail with a confusing path-resolution error when the
	// model dropped `path` entirely. missingParamError lists the keys
	// the call actually carried so the next round can self-correct.
	if strings.TrimSpace(path) == "" {
		return Result{}, missingParamError("write_file", "path", req.Params,
			`{"path":"docs/notes.md","content":"# Notes\n"}`,
			`path is the relative file location inside the project root.`)
	}
	if !contentProvided {
		return Result{}, missingParamError("write_file", "content", req.Params,
			`{"path":"docs/notes.md","content":"# Notes\n"}`,
			`content is the FULL file body. For small targeted changes prefer edit_file (old_string/new_string) — write_file replaces the entire file.`)
	}

	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	// Reject paths that resolve to an existing directory before we try
	// to mkdir-and-write — otherwise the model gets a confusing
	// "is a directory" syscall error instead of a self-teaching message.
	if info, err := os.Stat(absPath); err == nil && info.IsDir() {
		rel := PathRelativeToRoot(req.ProjectRoot, absPath)
		return Result{}, fmt.Errorf(
			"write_file refused: %q is a directory, not a file. Pick a path that points to a file (e.g. %q). "+
				`Recover: {"name":"write_file","args":{"path":"%s/new-file.txt","content":"..."}}`,
			rel, filepath.Join(rel, "new-file.txt"), rel)
	}
	if createDirs {
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return Result{}, err
		}
	}
	if !overwrite {
		if _, err := os.Stat(absPath); err == nil {
			rel := PathRelativeToRoot(req.ProjectRoot, absPath)
			return Result{}, fmt.Errorf(
				"write_file refused: %s already exists. "+
					"To replace it intentionally, set overwrite=true (and read it first via read_file so the engine knows the prior contents). "+
					"To make a small edit, use edit_file instead — it only needs old_string/new_string and preserves the rest. "+
					`Recover (overwrite shape): {"name":"write_file","args":{"path":%q,"content":"...","overwrite":true}}`,
				rel, rel)
		}
	}
	data := map[string]any{
		"path":               PathRelativeToRoot(req.ProjectRoot, absPath),
		"bytes":              len([]byte(content)),
		"overwrote_existing": false,
	}
	if overwrite {
		// Serialize BEFORE reading so the hash reflects the actual file
		// state at write time, not a stale snapshot from before the lock
		// was acquired (TOCTOU window: ReadFile → another writer → WriteFile).
		release := t.engine.LockPath(absPath)
		defer release()
		if oldContent, err := os.ReadFile(absPath); err == nil {
			sum := sha256.Sum256(oldContent)
			data["overwrote_existing"] = true
			data["previous_hash"] = hex.EncodeToString(sum[:])
			data["previous_bytes"] = len(oldContent)
			data["previous_hash_scope"] = "best_effort_prewrite"
			data["previous_hash_verified"] = false
		}
	} else {
		// Must still lock new-file creates: edit_file / apply_patch gate on
		// readSnapshots which track the parent directory; concurrent
		// write_file / apply_patch on a sibling file in the same dir
		// could race on the directory hash check.
		release := t.engine.LockPath(absPath)
		defer release()
	}
	if err := writeFileAtomic(absPath, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Output: "file written",
		Data:   data,
	}, nil
}

// missingParamError builds the actionable "<param> is required" reply
// for built-in tools. Pre-fix the error was just "pattern is required" —
// the model couldn't tell whether it had passed the wrong key, sent the
// path AS the pattern, or just forgotten the field. The 2026-04-18
// screenshot caught this exactly: the model hammered grep_codebase /
// glob with only `path: "D:/Codebox/PROJECTS/DFMC"` six times in a row
// because every reply just said "pattern is required" again.
//
// Post-fix we list the keys it ACTUALLY sent + the canonical example +
// (when applicable) the most likely confusion, so the next call can
// self-correct in one round instead of looping with the same bug.
//
// `confusionHint` is appended verbatim when non-empty; pass "" to skip.
func missingParamError(toolName, paramName string, params map[string]any, example, confusionHint string) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	got := "(empty)"
	if len(keys) > 0 {
		got = "[" + strings.Join(keys, ", ") + "]"
	}
	msg := fmt.Sprintf(
		"%s requires a `%s` field. Got params keys %s but no `%s`. Correct shape: %s",
		toolName, paramName, got, paramName, example)
	if hint := strings.TrimSpace(confusionHint); hint != "" {
		msg += " " + hint
	}
	return fmt.Errorf("%s", msg)
}

// valueLooksLikePath reports whether `s` is shaped like a filesystem
// path the model might have meant to put in `path` rather than
// `pattern`. Used to add a sharper "you put the path where the pattern
// goes" hint to the missing-pattern error — that was the exact mistake
// in the 2026-04-18 screenshot. Distinct from command.go's looksLikePath
// (which gates run_command's binary slot) because the heuristics differ:
// here a glob meta-char means it's a pattern, not a path.
func valueLooksLikePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, "*?[") {
		return false
	}
	if len(s) >= 2 && s[1] == ':' {
		return true
	}
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	return false
}

// formatGrepRegexError turns Go's bare RE2 compile error into an
// actionable message. The model often reaches for Perl/PCRE syntax
// (`\d`, `(?P<name>)`, `(?<=...)`, `\b` lookbehind, possessive `*+`)
// because that's what most regex tutorials teach. Go's `regexp` is
// pure RE2 — none of that works. Pre-fix the error was just
// "invalid regex pattern: error parsing regexp: invalid or unsupported
// Perl syntax" which gave the model nothing to recover from. Post-fix
// the error names the offending construct AND suggests the RE2
// equivalent so the next call self-corrects.

func truncateToolTextWithMarker(s string, maxBytes int, marker string) string {
	if maxBytes <= 0 {
		return marker
	}
	if len([]byte(s)) <= maxBytes {
		return s
	}
	markerBytes := len([]byte(marker))
	limit := maxBytes - markerBytes
	if limit < 0 {
		limit = 0
	}
	body := truncateUTF8ByBytes(s, limit)
	body = strings.TrimSuffix(body, "\n... [truncated]")
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return marker
	}
	return body + marker
}
