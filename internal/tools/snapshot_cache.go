package tools

// snapshot_cache.go — read-before-mutate gate and read-snapshot LRU.
//
// Mutating tools (write_file, edit_file, apply_patch) require a prior
// read_file snapshot of the target. Two enforcement modes:
//
//   strict  — snapshot must exist AND content hash must match the
//             current file. Used by write_file and apply_patch
//             because line-number-sensitive operations have no
//             anchor safety net; concurrent edits between read and
//             write would silently lose changes otherwise.
//   lenient — snapshot must exist but hash drift is tolerated.
//             Used by edit_file because its own old_string anchor
//             validation catches any unsafe edit. An editor or
//             formatter touching the file between read and edit
//             used to trip the strict gate even when the anchor
//             was still a perfectly safe unique match — lenient
//             mode fixes that without weakening safety.
//
// Snapshots are kept in an LRU bounded by maxReadSnapshots so a
// long session can't grow the map without bound; eviction follows
// least-recently-touched.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxReadSnapshots = 256

// readGateMode picks which read-before-mutate checks run for a given
// tool. "strict" enforces both the presence of a prior read_file
// snapshot AND hash equality; "lenient" only requires a prior snapshot
// and tolerates drift because the tool has its own per-call anchor
// validation (edit_file's exact-string match, for instance). "none"
// skips the gate entirely (tools that never touch existing files).
type readGateMode int

const (
	readGateNone readGateMode = iota
	readGateLenient
	readGateStrict
)

func readBeforeMutationMode(name string) readGateMode {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit_file":
		// edit_file refuses on its own when old_string doesn't match or
		// matches ambiguously. A hash-drift refusal at the gate added
		// noise without catching any case edit_file wouldn't already
		// catch — an editor/formatter touching the file between read
		// and edit tripped the gate even when the anchor was still a
		// perfectly safe unique match. Require a prior snapshot (so the
		// model has at least seen the file) but skip the hash check.
		return readGateLenient
	case "write_file":
		return readGateStrict
	case "apply_patch":
		// apply_patch calls EnsureReadBeforeMutation per target directly
		// (engine.EnsureReadBeforeMutation), bypassing this gate so it can
		// handle multi-file patches in one call. The switch entry here is
		// documentation — future mutating tools that handle multiple paths
		// should follow the same pattern and bypass this gate too.
		return readGateNone
	default:
		return readGateNone
	}
}

// EnsureReadBeforeMutation exposes the per-file read-before-mutate gate
// for tools that mutate multiple files in one call (e.g. apply_patch).
// The per-`path` dispatch in Execute() only handles single-target tools;
// multi-target ones must thread each path through this method explicitly
// so a fabricated diff can't bypass the read snapshot check that
// edit_file / write_file already enforce. Callers that don't have a
// per-tool mode use strict — apply_patch has line-number-sensitive
// hunks that genuinely need the hash check.
func (e *Engine) EnsureReadBeforeMutation(absPath string) error {
	return e.ensureReadBeforeMutationMode(absPath, readGateStrict)
}

func (e *Engine) ensureReadBeforeMutationMode(absPath string, mode readGateMode) error {
	if mode == readGateNone {
		return nil
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // Creating a new file does not require prior read.
		}
		return err
	}
	if info.IsDir() {
		// Directories can't have a read_file snapshot — let the tool
		// emit its own self-teaching "X is a directory" error rather
		// than confuse the model with a "no prior read_file snapshot"
		// guard message that points at an unreadable target.
		return nil
	}

	e.readMu.RLock()
	lastReadHash, ok := e.readSnapshots[absPath]
	e.readMu.RUnlock()
	if !ok {
		return readGuardError(absPath, "missing")
	}
	if mode == readGateLenient {
		return nil
	}

	hash, err := fileContentHash(absPath)
	if err != nil {
		return err
	}
	if lastReadHash != hash {
		return readGuardError(absPath, "drift")
	}
	return nil
}

// readGuardError builds the actionable refusal returned by the read-
// before-mutate gate. Pre-2026-04-18 the error was a bare "modifying
// existing file requires prior read_file: PATH" or "file changed since
// last read_file; read again before modifying: PATH". Models that
// hadn't seen the snapshot rule before just retried the same edit and
// looped — the screenshots from this session caught it on real
// sessions. Post-fix the message embeds the literal recovery tool_call
// the model can emit verbatim, so the next round self-corrects in one
// step instead of N. `kind` is "missing" (no prior read at all) or
// "drift" (file modified between read and edit).
func readGuardError(absPath, kind string) error {
	relHint := absPath
	if cwd, err := os.Getwd(); err == nil {
		if rel, err2 := filepath.Rel(cwd, absPath); err2 == nil && !strings.HasPrefix(rel, "..") {
			relHint = filepath.ToSlash(rel)
		}
	}
	example := fmt.Sprintf(`{"name":"read_file","args":{"path":%q}}`, relHint)
	switch kind {
	case "drift":
		return fmt.Errorf(
			"edit refused: %s changed on disk since your last read_file (an editor, formatter, "+
				"or another tool wrote to it). The snapshot you held is now stale — apply the diff "+
				"against the current bytes by re-reading first: %s. Then retry your edit/apply_patch "+
				"with the same arguments",
			relHint, example)
	default:
		return fmt.Errorf(
			"edit refused: %s has no prior read_file snapshot in this session. The engine requires "+
				"you to read a file before mutating it (so the model is editing what's actually on "+
				"disk, not a guess). Recover by calling: %s. Then retry your edit/apply_patch",
			relHint, example)
	}
}

func (e *Engine) recordReadSnapshot(name, projectRoot string, params map[string]any, res Result) {
	toolName := strings.ToLower(strings.TrimSpace(name))
	switch toolName {
	case "read_file", "write_file", "edit_file":
	default:
		return
	}
	p := strings.TrimSpace(asString(res.Data, "path", ""))
	if p == "" {
		p = strings.TrimSpace(asString(params, "path", ""))
	}
	if p == "" {
		return
	}
	abs, err := EnsureWithinRoot(projectRoot, p)
	if err != nil {
		return
	}

	// H1 fix: for read_file, hash the content already in memory to avoid
	// TOCTOU race and double I/O (M1). For write/edit, the file on disk
	// IS authoritative, so hash from disk.
	var hash string
	if toolName == "read_file" {
		// Prefer the full-file hash emitted by ReadFileTool over
		// sha256(res.Output). The Output field carries only the returned
		// line window (default 200 lines), so hashing it would produce a
		// slice-hash that can never match fileContentHash(abs) at the
		// strict gate - any write_file / apply_patch after a sliced read
		// would be refused as "drift" even when nothing had changed.
		if fullHash := asString(res.Data, "content_sha256", ""); fullHash != "" {
			hash = fullHash
		} else {
			sum := sha256.Sum256([]byte(res.Output))
			hash = hex.EncodeToString(sum[:])
		}
	} else {
		hash, err = fileContentHash(abs)
		if err != nil {
			return
		}
	}

	e.readMu.Lock()
	e.readSnapshots[abs] = hash
	e.touchReadSnapshotLocked(abs)
	e.readMu.Unlock()
}

func (e *Engine) touchReadSnapshotLocked(abs string) {
	if strings.TrimSpace(abs) == "" {
		return
	}
	for i, existing := range e.readSnapshotLRU {
		if existing == abs {
			e.readSnapshotLRU = append(e.readSnapshotLRU[:i], e.readSnapshotLRU[i+1:]...)
			break
		}
	}
	e.readSnapshotLRU = append(e.readSnapshotLRU, abs)
	cap := e.readSnapshotCap
	if cap <= 0 {
		cap = maxReadSnapshots
	}
	if len(e.readSnapshots) > cap {
		target := cap / 2
		if target <= 0 {
			target = 1
		}
		for len(e.readSnapshots) > target && len(e.readSnapshotLRU) > 0 {
			evict := e.readSnapshotLRU[0]
			e.readSnapshotLRU = e.readSnapshotLRU[1:]
			delete(e.readSnapshots, evict)
		}
	}
	// Keep the snapshot map bounded even if the LRU drifted due to a
	// direct test mutation or a future cleanup path that removed map
	// entries without touching the LRU order.
	for len(e.readSnapshots) > cap {
		for key := range e.readSnapshots {
			delete(e.readSnapshots, key)
			break
		}
	}
}
