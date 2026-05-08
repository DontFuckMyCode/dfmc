package storage

// store_conversation.go — JSONL log + JSON state persistence for one
// conversation, plus the path validator that prevents convID values
// from escaping the artifact tree. Save/Load go through writeFileAtomic
// (tmp+sync+rename+syncDir) so a crash mid-write never leaves a torn
// or zero-length log on disk. Sibling to backups (store_backups.go)
// and the open/lifecycle/schema-migration core in store.go.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (s *Store) SaveConversationLog(convID string, messages []types.Message) error {
	if err := validateConvID(convID); err != nil {
		return err
	}

	dir := filepath.Join(s.artifactDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}

	path := conversationLogPath(dir, convID)

	// Encode in-memory first, then atomically rename into place. The
	// previous os.Create approach truncated the existing file up-front
	// — a crash or signal mid-write would leave the user's conversation
	// history truncated (or zero-length, if nothing had been flushed).
	// Buffering + temp-then-rename guarantees the on-disk file is
	// either the old full log OR the new full log, never a torn
	// in-between state.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
	}
	if err := writeFileAtomic(path, buf.Bytes(), "."+convID+".jsonl.dfmc-tmp-*"); err != nil {
		return fmt.Errorf("save conversation log: %w", err)
	}
	return nil
}

func (s *Store) SaveConversationState(convID string, state any) error {
	if err := validateConvID(convID); err != nil {
		return err
	}
	dir := filepath.Join(s.artifactDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode conversation state: %w", err)
	}
	if err := writeFileAtomic(conversationStatePath(dir, convID), data, "."+convID+".json.dfmc-tmp-*"); err != nil {
		return fmt.Errorf("save conversation state: %w", err)
	}
	return nil
}

func (s *Store) LoadConversationState(convID string, dst any) error {
	if err := validateConvID(convID); err != nil {
		return err
	}
	if dst == nil {
		return fmt.Errorf("conversation state destination is nil")
	}
	path := conversationStatePath(filepath.Join(s.artifactDir, "conversations"), convID)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode conversation state: %w", err)
	}
	return nil
}

func (s *Store) LoadConversationLog(convID string) ([]types.Message, error) {
	if err := validateConvID(convID); err != nil {
		return nil, err
	}

	path := conversationLogPath(filepath.Join(s.artifactDir, "conversations"), convID)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var messages []types.Message
	sc := bufio.NewScanner(f)
	// bufio.Scanner's default line limit is 64 KiB (MaxScanTokenSize).
	// A single tool-output message — a long `run_command` stdout, a
	// pasted patch, a big code block — easily exceeds that and used to
	// fail the whole load with "token too long". 8 MiB covers
	// essentially any realistic message while still capping the
	// per-line memory grab so a corrupted file can't pull unbounded
	// RAM.
	const maxLineBytes = 8 * 1024 * 1024
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg types.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("decode message: %w", err)
		}
		messages = append(messages, msg)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	return messages, nil
}

func conversationLogPath(dir, convID string) string {
	return filepath.Join(dir, convID+".jsonl")
}

func conversationStatePath(dir, convID string) string {
	return filepath.Join(dir, convID+".json")
}

func writeFileAtomic(path string, data []byte, pattern string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	// Best-effort durability: flush the parent directory entry after the
	// rename so a sudden power loss is less likely to lose the new name.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		// Some filesystems do not support directory sync. The rename still
		// gave us atomic replacement, so do not fail persistence outright.
		return nil
	}
	return nil
}

// validateConvID rejects conversation IDs that would escape the
// artifactDir/conversations directory when concatenated into a path.
// Pre-fix the IDs were joined raw, so a `convID="../../etc/passwd"`
// would write/read outside the project's artifact tree — a path
// traversal flagged as C1 in the 2026-04-17 review. Conversation IDs
// are allowed to contain dashes, dots, alphanumerics, and underscores
// only; anything else is treated as a synthetic / hostile value.
func validateConvID(id string) error {
	if id == "" {
		return fmt.Errorf("conversation id is required")
	}
	if filepath.IsAbs(id) {
		return fmt.Errorf("invalid conversation id %q: must be a relative basename", id)
	}
	// `..` segments and any path separator make the value capable of
	// escaping the artifact dir. Both POSIX `/` and Windows `\` count.
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("invalid conversation id %q: must not contain path separators", id)
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("invalid conversation id %q: must not contain `..`", id)
	}
	// Reject control bytes and the NUL byte — these can split paths on
	// some filesystems and they're never legitimate in a conversation
	// identifier we generate ourselves.
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid conversation id: contains control character U+%04X (%q)", r, string(r))
		}
	}
	return nil
}
