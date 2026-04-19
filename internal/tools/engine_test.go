package tools

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestReadFileToolBoundary(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "a.txt",
			"line_start": 2,
			"line_end":   3,
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if !strings.Contains(res.Output, "line2") {
		t.Fatalf("expected line2 in output: %q", res.Output)
	}

	_, err = eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "../outside.txt",
		},
	})
	if err == nil {
		t.Fatal("expected boundary error")
	}
}

// TestReadFileToolOutOfRangeLineStartDoesNotPanic pins a previous crash where
// a model passed line_start beyond EOF (e.g. 400 on a 213-line file) and the
// tool panicked with "slice bounds out of range". The tool must degrade to an
// empty segment with a sane line range instead.
func TestReadFileToolOutOfRangeLineStartDoesNotPanic(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "small.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "small.txt",
			"line_start": 400,
			"line_end":   500,
		},
	})
	if err != nil {
		t.Fatalf("out-of-range read_file should not error, got: %v", err)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("expected empty segment for out-of-range line_start, got: %q", res.Output)
	}
}

func TestReadFileTool_BinaryHeuristicMetadataAndLateNUL(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "late-nul.txt")
	data := append([]byte(strings.Repeat("a", readFileBinaryCheckBytes+32)), 0)
	data = append(data, []byte("\ntrailer\n")...)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "late-nul.txt"},
	})
	if err != nil {
		t.Fatalf("late NUL beyond heuristic window should not be rejected, got: %v", err)
	}
	if got, _ := res.Data["binary_heuristic"].(string); got != "nul-in-first-window" {
		t.Fatalf("unexpected binary heuristic metadata: %#v", res.Data["binary_heuristic"])
	}
	if got, _ := res.Data["binary_check_bytes"].(int); got != readFileBinaryCheckBytes {
		t.Fatalf("expected binary_check_bytes=%d, got %#v", readFileBinaryCheckBytes, res.Data["binary_check_bytes"])
	}
}

func TestTrackFailureEvictsOldestDeterministically(t *testing.T) {
	eng := New(*config.DefaultConfig())
	for i := 0; i < maxRecentFailures+1; i++ {
		key := filepath.ToSlash(filepath.Join("tool", "k", strconv.Itoa(i)))
		eng.trackFailure(key)
	}
	if _, ok := eng.recentFailures[filepath.ToSlash(filepath.Join("tool", "k", "0"))]; ok {
		t.Fatal("oldest failure key should have been evicted first")
	}
	for i := maxRecentFailures/2 + 1; i <= maxRecentFailures; i++ {
		key := filepath.ToSlash(filepath.Join("tool", "k", strconv.Itoa(i)))
		if _, ok := eng.recentFailures[key]; !ok {
			t.Fatalf("newer failure key %q should still be present", key)
		}
	}
	if len(eng.recentFailures) != maxRecentFailures/2 {
		t.Fatalf("expected eviction down to %d entries, got %d", maxRecentFailures/2, len(eng.recentFailures))
	}
}

func TestReadFileTool_UTF16WithBOMIsReadAsText(t *testing.T) {
	t.Run("utf16le", func(t *testing.T) {
		testReadFileToolUTF16WithBOM(t, binary.LittleEndian, []byte{0xFF, 0xFE}, readFileEncodingUTF16LEBOM)
	})
	t.Run("utf16be", func(t *testing.T) {
		testReadFileToolUTF16WithBOM(t, binary.BigEndian, []byte{0xFE, 0xFF}, readFileEncodingUTF16BEBOM)
	})
}

func testReadFileToolUTF16WithBOM(t *testing.T, order binary.ByteOrder, bom []byte, wantEncoding string) {
	t.Helper()

	tmp := t.TempDir()
	file := filepath.Join(tmp, "utf16.txt")
	payload := append([]byte{}, bom...)
	payload = append(payload, encodeUTF16StringForTest(order, "line1\nline2\n")...)
	if err := os.WriteFile(file, payload, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "utf16.txt"},
	})
	if err != nil {
		t.Fatalf("utf16 read_file should succeed, got: %v", err)
	}
	if !strings.Contains(res.Output, "line2") {
		t.Fatalf("expected decoded UTF-16 text in output, got: %q", res.Output)
	}
	if got, _ := res.Data["encoding"].(string); got != wantEncoding {
		t.Fatalf("expected encoding=%q, got %#v", wantEncoding, res.Data["encoding"])
	}
	if got, _ := res.Data["binary_heuristic"].(string); got != "nul-in-first-window" {
		t.Fatalf("unexpected binary heuristic metadata: %#v", res.Data["binary_heuristic"])
	}
}

func encodeUTF16StringForTest(order binary.ByteOrder, s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, len(units)*2)
	for i, unit := range units {
		order.PutUint16(buf[i*2:], unit)
	}
	return buf
}

func TestGrepTool(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	src := "package main\nfunc main(){}\n// TODO: improve\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern": "TODO",
		},
	})
	if err != nil {
		t.Fatalf("execute grep: %v", err)
	}
	if !strings.Contains(res.Output, "TODO") {
		t.Fatalf("expected TODO in grep output: %q", res.Output)
	}
}

func TestGrepTool_QueryAliasAccepted(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	src := "package main\nfunc main(){}\n// TODO: improve\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "TODO",
		},
	})
	if err != nil {
		t.Fatalf("execute grep with query alias: %v", err)
	}
	if !strings.Contains(res.Output, "TODO") {
		t.Fatalf("expected TODO in grep output: %q", res.Output)
	}
}

// Real-world failure (TUI 2026-04-18 session): the model wrote a Perl
// regex (lookbehind / backref / etc) and got the bare error
// "invalid regex pattern: error parsing regexp: invalid or unsupported
// Perl syntax" — useless for self-correction. Post-fix the error names
// the offending construct + suggests the RE2 alternative so the next
// call self-corrects in a single round.
func TestGrepCodebase_PerlRegexErrorIsActionable(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	cases := []struct {
		name        string
		pattern     string
		wantInError []string // every substring must appear
	}{
		{
			"lookbehind",
			`(?<=func )Foo`,
			[]string{`Lookbehind`, `NOT supported in RE2`, `(?<=func )Foo`},
		},
		{
			"lookahead",
			`Foo(?=Bar)`,
			[]string{`Lookahead`, `NOT supported in RE2`, `Foo(?=Bar)`},
		},
		{
			"backreference",
			`(\w+)=\1`,
			[]string{`Backreferences`, `NOT supported in RE2`, `linear-time matching`},
		},
		{
			"unknown perl construct",
			`\K`,
			[]string{`invalid regex pattern`, `\K`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "grep_codebase", Request{
				ProjectRoot: tmp,
				Params:      map[string]any{"pattern": c.pattern},
			})
			if err == nil {
				t.Fatalf("Perl-syntax pattern %q must reject", c.pattern)
			}
			msg := err.Error()
			for _, want := range c.wantInError {
				if !strings.Contains(msg, want) {
					t.Fatalf("error should mention %q\n  pattern: %q\n  got: %q", want, c.pattern, msg)
				}
			}
		})
	}
}

func TestToolOutputCompressionBySandboxLimit(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "big.txt")
	body := strings.Repeat("line with repetitive content for compression test\n", 400)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Security.Sandbox.MaxOutput = "400B"
	eng := New(*cfg)

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "big.txt",
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true for large tool output")
	}
	if len([]byte(res.Output)) > 400 {
		t.Fatalf("expected compressed output <= 400 bytes, got %d", len([]byte(res.Output)))
	}
}

func TestToolOutputCompressionPreservesRelevantLines(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "big.txt")
	var b strings.Builder
	for i := 0; i < 260; i++ {
		if i == 180 {
			b.WriteString("THIS_IS_MAGIC_MATCH line\n")
		} else {
			b.WriteString("ordinary filler line\n")
		}
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Security.Sandbox.MaxOutput = "1KB"
	eng := New(*cfg)

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":  "big.txt",
			"query": "magic_match",
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true for large tool output")
	}
	if !strings.Contains(strings.ToLower(res.Output), "magic_match") {
		t.Fatalf("expected compressed output to keep relevant line, got: %q", res.Output)
	}
}

func TestToolParamsNormalizeReadFileDefaultRange(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "huge.txt")
	var b strings.Builder
	for i := 0; i < 600; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "huge.txt",
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	start, _ := res.Data["line_start"].(int)
	end, _ := res.Data["line_end"].(int)
	if start != 1 {
		t.Fatalf("expected line_start=1, got %d", start)
	}
	if end != 200 {
		t.Fatalf("expected normalized line_end=200, got %d", end)
	}
}

func TestToolParamsNormalizeGrepMaxResultsClamp(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	var b strings.Builder
	for i := 0; i < 900; i++ {
		b.WriteString("// TODO: item\n")
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern":     "TODO",
			"max_results": 9999,
		},
	})
	if err != nil {
		t.Fatalf("execute grep: %v", err)
	}
	if n, _ := res.Data["count"].(int); n > 500 {
		t.Fatalf("expected max_results clamp <= 500, got %d", n)
	}
}

func TestToolParamsNormalizeAliases(t *testing.T) {
	tmp := t.TempDir()
	readTarget := filepath.Join(tmp, "alias.txt")
	if err := os.WriteFile(readTarget, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("write alias file: %v", err)
	}

	runCases := []struct {
		name       string
		tool       string
		params     map[string]any
		assertFunc func(t *testing.T, res Result)
	}{
		{
			name: "read_file_aliases",
			tool: "read_file",
			params: map[string]any{
				"file":  "alias.txt",
				"start": 2,
				"end":   2,
			},
			assertFunc: func(t *testing.T, res Result) {
				t.Helper()
				if !strings.Contains(res.Output, "line2") {
					t.Fatalf("expected aliased read_file output to contain line2, got %q", res.Output)
				}
			},
		},
		{
			name: "list_dir_aliases",
			tool: "list_dir",
			params: map[string]any{
				"dir":   ".",
				"limit": 1,
			},
			assertFunc: func(t *testing.T, res Result) {
				t.Helper()
				if entries, _ := res.Data["entries"].([]map[string]any); len(entries) > 1 {
					t.Fatalf("expected aliased list_dir limit to apply, got %d entries", len(entries))
				}
			},
		},
		{
			name: "grep_aliases",
			tool: "grep_codebase",
			params: map[string]any{
				"regex": "line2",
				"limit": 1,
			},
			assertFunc: func(t *testing.T, res Result) {
				t.Helper()
				if !strings.Contains(res.Output, "line2") {
					t.Fatalf("expected aliased grep output to contain match, got %q", res.Output)
				}
				if count, _ := res.Data["count"].(int); count > 1 {
					t.Fatalf("expected aliased grep limit to apply, got count=%d", count)
				}
			},
		},
		{
			name: "glob_aliases",
			tool: "glob",
			params: map[string]any{
				"query": "*.txt",
				"root":  ".",
			},
			assertFunc: func(t *testing.T, res Result) {
				t.Helper()
				if !strings.Contains(res.Output, "alias.txt") {
					t.Fatalf("expected aliased glob output to mention alias.txt, got %q", res.Output)
				}
			},
		},
		{
			name: "run_command_aliases",
			tool: "run_command",
			params: map[string]any{
				"cmd":     "go",
				"argv":    []any{"version"},
				"workdir": tmp,
				"timeout": 10000,
			},
			assertFunc: func(t *testing.T, res Result) {
				t.Helper()
				if !strings.Contains(strings.ToLower(res.Output), "go version") {
					t.Fatalf("expected aliased run_command output, got %q", res.Output)
				}
			},
		},
	}

	for _, tc := range runCases {
		t.Run(tc.name, func(t *testing.T) {
			eng := New(*config.DefaultConfig())
			res, err := eng.Execute(context.Background(), tc.tool, Request{
				ProjectRoot: tmp,
				Params:      tc.params,
			})
			if err != nil {
				t.Fatalf("execute %s with aliases: %v", tc.tool, err)
			}
			tc.assertFunc(t, res)
		})
	}
}

func TestToolFailureGuardAfterRepeatedErrors(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	args := Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "../outside.txt",
		},
	}
	for i := 0; i < 2; i++ {
		if _, err := eng.Execute(context.Background(), "read_file", args); err == nil {
			t.Fatalf("expected boundary error at attempt %d", i+1)
		}
	}
	_, err := eng.Execute(context.Background(), "read_file", args)
	if err == nil {
		t.Fatal("expected repeated-failure guard error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "failed repeatedly") {
		t.Fatalf("expected repeated failure message, got: %v", err)
	}
}

func TestWriteFileRequiresPriorReadForExistingFile(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":    "a.txt",
			"content": "new",
		},
	})
	if err == nil {
		t.Fatalf("expected prior-read guard error, got nil")
	}
	{
		msg := err.Error()
		if !strings.Contains(msg, "no prior read_file snapshot") {
			t.Fatalf("expected 'no prior read_file snapshot' in error, got: %v", err)
		}
		if !strings.Contains(msg, `{"name":"read_file","args":{"path":`) {
			t.Fatalf("expected actionable read_file recovery example in error, got: %v", err)
		}
	}

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "a.txt",
		},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":      "a.txt",
			"content":   "new",
			"overwrite": true,
		},
	}); err != nil {
		t.Fatalf("write_file after read should succeed: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestWriteFileOverwriteReturnsPreviousHashMetadata(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	oldContent := "old value\n"
	if err := os.WriteFile(file, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "a.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	res, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":      "a.txt",
			"content":   "new value\n",
			"overwrite": true,
		},
	})
	if err != nil {
		t.Fatalf("write_file overwrite: %v", err)
	}
	if overwrote, _ := res.Data["overwrote_existing"].(bool); !overwrote {
		t.Fatalf("expected overwrote_existing=true, got %#v", res.Data["overwrote_existing"])
	}
	if prevHash, _ := res.Data["previous_hash"].(string); len(prevHash) != 64 {
		t.Fatalf("expected 64-char previous_hash, got %#v", res.Data["previous_hash"])
	}
	if prevBytes, _ := res.Data["previous_bytes"].(int); prevBytes != len([]byte(oldContent)) {
		t.Fatalf("expected previous_bytes=%d, got %#v", len([]byte(oldContent)), res.Data["previous_bytes"])
	}
	if scope, _ := res.Data["previous_hash_scope"].(string); scope != "best_effort_prewrite" {
		t.Fatalf("expected best-effort hash scope, got %#v", res.Data["previous_hash_scope"])
	}
	if verified, _ := res.Data["previous_hash_verified"].(bool); verified {
		t.Fatalf("previous_hash must not claim atomic verification")
	}
}

func TestReadSnapshotsEvictLeastRecentlyRead(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.readMu.Lock()
	for i := 0; i < maxReadSnapshots; i++ {
		path := filepath.Join("root", fmt.Sprintf("f-%03d.txt", i))
		eng.readSnapshots[path] = "hash"
		eng.touchReadSnapshotLocked(path)
	}
	// Re-read the oldest entry so it becomes most recently used.
	keep := filepath.Join("root", "f-000.txt")
	eng.touchReadSnapshotLocked(keep)
	newest := filepath.Join("root", "f-new.txt")
	eng.readSnapshots[newest] = "hash"
	eng.touchReadSnapshotLocked(newest)
	_, kept := eng.readSnapshots[keep]
	_, evicted := eng.readSnapshots[filepath.Join("root", "f-001.txt")]
	eng.readMu.Unlock()
	if !kept {
		t.Fatal("most recently touched snapshot should not be evicted")
	}
	if evicted {
		t.Fatal("least recently read snapshot should have been evicted")
	}
}

func TestTouchReadSnapshotLockedBoundsCountWithStaleLRUEntries(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.readMu.Lock()
	for i := 0; i < maxReadSnapshots; i++ {
		path := filepath.Join("root", fmt.Sprintf("f-%03d.txt", i))
		eng.readSnapshots[path] = "hash"
		eng.touchReadSnapshotLocked(path)
	}
	stale := filepath.Join("root", "f-000.txt")
	delete(eng.readSnapshots, stale)
	newest := filepath.Join("root", "f-new.txt")
	eng.readSnapshots[newest] = "hash"
	eng.touchReadSnapshotLocked(newest)
	gotSnapshots := len(eng.readSnapshots)
	eng.readMu.Unlock()
	if gotSnapshots > maxReadSnapshots {
		t.Fatalf("snapshot count must stay bounded at %d, got %d", maxReadSnapshots, gotSnapshots)
	}
}

func TestTouchReadSnapshotLockedEvictsToHalfCapacity(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.readMu.Lock()
	for i := 0; i < maxReadSnapshots; i++ {
		path := filepath.Join("root", fmt.Sprintf("f-%03d.txt", i))
		eng.readSnapshots[path] = "hash"
		eng.touchReadSnapshotLocked(path)
	}
	overflow := filepath.Join("root", "f-overflow.txt")
	eng.readSnapshots[overflow] = "hash"
	eng.touchReadSnapshotLocked(overflow)
	gotSnapshots := len(eng.readSnapshots)
	gotLRU := len(eng.readSnapshotLRU)
	_, keptOverflow := eng.readSnapshots[overflow]
	eng.readMu.Unlock()
	wantTarget := maxReadSnapshots / 2
	if gotSnapshots != wantTarget {
		t.Fatalf("expected snapshot count to compact to %d, got %d", wantTarget, gotSnapshots)
	}
	if gotLRU < gotSnapshots {
		t.Fatalf("expected LRU to retain at least surviving entries, got lru=%d snapshots=%d", gotLRU, gotSnapshots)
	}
	if !keptOverflow {
		t.Fatal("newly touched overflow snapshot should survive compaction")
	}
}

func TestTouchReadSnapshotLockedStaleLRUCompactsToHalfCapacity(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.readMu.Lock()
	for i := 0; i < maxReadSnapshots; i++ {
		path := filepath.Join("root", fmt.Sprintf("f-%03d.txt", i))
		eng.readSnapshots[path] = "hash"
		eng.touchReadSnapshotLocked(path)
	}
	for i := 0; i < maxReadSnapshots/4; i++ {
		delete(eng.readSnapshots, filepath.Join("root", fmt.Sprintf("f-%03d.txt", i)))
	}
	for i := 0; i < maxReadSnapshots/4+1; i++ {
		path := filepath.Join("root", fmt.Sprintf("fresh-%03d.txt", i))
		eng.readSnapshots[path] = "hash"
		eng.touchReadSnapshotLocked(path)
	}
	gotSnapshots := len(eng.readSnapshots)
	eng.readMu.Unlock()
	wantMax := maxReadSnapshots / 2
	if gotSnapshots > wantMax {
		t.Fatalf("expected stale-LRU compaction to keep at most %d snapshots, got %d", wantMax, gotSnapshots)
	}
}

func TestToolFailureKeyCanonicalizesNestedMaps(t *testing.T) {
	left := toolFailureKey("tool_call", map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "a.go", "line_end": 10, "line_start": 1},
	})
	right := toolFailureKey("tool_call", map[string]any{
		"name": "read_file",
		"args": map[string]any{"line_start": 1, "path": "a.go", "line_end": 10},
	})
	if left != right {
		t.Fatalf("nested map order should not affect failure key:\nleft:  %q\nright: %q", left, right)
	}
}

func TestWriteFileAllowsNewFileWithoutRead(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	target := filepath.Join(tmp, "new.txt")

	if _, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":    "new.txt",
			"content": "hello",
		},
	}); err != nil {
		t.Fatalf("write_file new file: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected new file content: %q", string(got))
	}
}

func TestWriteAndEditFileAcceptCommonAliases(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "alias-write.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"file": "alias-write.txt",
		},
	}); err != nil {
		t.Fatalf("read_file with alias: %v", err)
	}

	if _, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"file": "alias-write.txt",
			"old":  "before",
			"new":  "after",
		},
	}); err != nil {
		t.Fatalf("edit_file with aliases: %v", err)
	}

	if _, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"file":  "alias-write.txt",
			"text":  "rewritten\n",
			"force": true,
		},
	}); err != nil {
		t.Fatalf("write_file with aliases: %v", err)
	}

	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != "rewritten\n" {
		t.Fatalf("unexpected aliased write_file content: %q", string(got))
	}
}

func TestEditFileRequiresReadAndUniqueness(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(file, []byte("var x = 1\nvar x = 2\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	eng := New(*config.DefaultConfig())

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "main.go",
			"old_string": "var x",
			"new_string": "var y",
		},
	})
	if err == nil {
		t.Fatalf("expected prior-read guard error, got nil")
	}
	{
		msg := err.Error()
		if !strings.Contains(msg, "no prior read_file snapshot") {
			t.Fatalf("expected 'no prior read_file snapshot' in error, got: %v", err)
		}
		if !strings.Contains(msg, `{"name":"read_file","args":{"path":`) {
			t.Fatalf("expected actionable read_file recovery example in error, got: %v", err)
		}
	}

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "main.go"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	_, err = eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "main.go",
			"old_string": "var x",
			"new_string": "var y",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not unique") {
		t.Fatalf("expected uniqueness error, got: %v", err)
	}

	if _, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":        "main.go",
			"old_string":  "var x",
			"new_string":  "var y",
			"replace_all": true,
		},
	}); err != nil {
		t.Fatalf("edit_file replace_all: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if strings.Contains(string(got), "var x") {
		t.Fatalf("expected replacement, got: %q", string(got))
	}
}

func TestEditFileFailsIfChangedSinceRead(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	eng := New(*config.DefaultConfig())

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "a.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if err := os.WriteFile(file, []byte("beta"), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "a.txt",
			"old_string": "beta",
			"new_string": "gamma",
		},
	})
	if err == nil {
		t.Fatalf("expected changed-since-read guard error, got nil")
	}
	{
		msg := err.Error()
		if !strings.Contains(msg, "changed on disk since your last read_file") {
			t.Fatalf("expected 'changed on disk since your last read_file' in error, got: %v", err)
		}
		if !strings.Contains(msg, `{"name":"read_file","args":{"path":`) {
			t.Fatalf("expected actionable read_file recovery example in error, got: %v", err)
		}
	}
}

func TestRunCommandToolRunsDirectCommand(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command":    "go",
			"args":       "version",
			"timeout_ms": 10000,
		},
	})
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	if !strings.Contains(strings.ToLower(res.Output), "go version") {
		t.Fatalf("expected go version output, got %q", res.Output)
	}
	if changed, _ := res.Data["workspace_changed"].(bool); changed {
		t.Fatalf("expected go version to avoid workspace changes, got %#v", res.Data)
	}
}

// Real-world failure mode (TUI 2026-04-18 session, glm-5.1): the model
// packed a whole shell line into `command` —
//
//	{command: "cd D:\\repo && go build ./...", args: ""}
//
// — because the tool's prompt used to say "chain with `&&` inside a
// SINGLE run_command". Pre-fix the executor treated the entire string
// as a path (it contains `\`) and failed with the opaque "file does
// not exist" — telling the model nothing about how to recover. The
// new shell-syntax detector returns an actionable error naming the
// offender and showing the correct command/args shape.
func TestRunCommandToolRejectsShellSyntaxWithActionableError(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	cases := []struct {
		name        string
		command     string
		wantInError string // substring that MUST appear in error
	}{
		{"and-chain", `cd D:\repo && go build ./...`, "&&"},
		{"background-chain", `go test & echo done`, "&"},
		{"or-chain", `go build || true`, "||"},
		{"semicolon", `go vet; go test`, ";"},
		{"pipe", `go test | grep FAIL`, "|"},
		{"redirect", `go test > out.log`, ">"},
		{"stderr-merge", `go build 2>&1`, "2>&1"},
		{"leading-cd", `cd subdir`, "cd "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "run_command", Request{
				ProjectRoot: tmp,
				Params: map[string]any{
					"command": c.command,
				},
			})
			if err == nil {
				t.Fatalf("shell-syntax %q must be rejected", c.command)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.wantInError) {
				t.Fatalf("error should name the offender %q; got %q", c.wantInError, msg)
			}
			// The error must teach the model the right shape so the
			// next tool call self-corrects. Minimum cues: that there's
			// no shell, and the {command, args} JSON example.
			if !strings.Contains(msg, "does not invoke a shell") {
				t.Fatalf("error should explain there's no shell; got %q", msg)
			}
			if !strings.Contains(msg, `"command":"go"`) {
				t.Fatalf("error should show the {command,args} example; got %q", msg)
			}
		})
	}
}

func TestRunCommandRejectsShellSubstitutionInArgs(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	cases := []struct {
		name  string
		args  any
		token string
	}{
		{name: "dollar-paren", args: []any{"$(pwd)"}, token: "$("},
		{name: "backticks", args: []any{"`pwd`"}, token: "`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "run_command", Request{
				ProjectRoot: tmp,
				Params: map[string]any{
					"command": "echo",
					"args":    c.args,
				},
			})
			if err == nil {
				t.Fatal("shell substitution in args must be rejected")
			}
			msg := err.Error()
			if !strings.Contains(msg, "passed literally") {
				t.Fatalf("error should explain literal argv behavior, got %q", msg)
			}
			if !strings.Contains(msg, c.token) {
				t.Fatalf("error should name offending shell token %q, got %q", c.token, msg)
			}
		})
	}
}

// TestRunCommandShellSyntaxSuggestsDirRecovery pins the targeted recovery
// hint for the `cd <dir> && <cmd>` footgun. The model passed an entire
// shell line into `command` because `dir` wasn't surfaced as the actual
// fix. The error must now show the literal {command, args, dir} shape so
// the next round self-corrects in one step instead of looping on the
// generic "use args" hint.
func TestRunCommandShellSyntaxSuggestsDirRecovery(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	cases := []struct {
		name     string
		command  string
		wantBin  string
		wantArg  string // first arg, must appear in args array
		wantDir  string // unquoted directory after `cd `
		wantSep  string // shell separator that should be reported
		dirParam string // the `dir` value as it should appear in the JSON example (forward slashes)
	}{
		{"and-chain-windows-path", `cd D:\Codebox\PROJECTS\DFMC && go vet ./internal/tools/...`, "go", "vet", `D:\Codebox\PROJECTS\DFMC`, "&&", "D:/Codebox/PROJECTS/DFMC"},
		{"semicolon-unix-path", `cd /repo/sub ; npm test`, "npm", "test", `/repo/sub`, ";", "/repo/sub"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "run_command", Request{
				ProjectRoot: tmp,
				Params:      map[string]any{"command": c.command},
			})
			if err == nil {
				t.Fatalf("expected rejection of %q", c.command)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.wantSep) {
				t.Fatalf("error must name the separator %q; got %q", c.wantSep, msg)
			}
			// The JSON example must use the dir parameter and split bin/args.
			wantCmd := `"command":"` + c.wantBin + `"`
			if !strings.Contains(msg, wantCmd) {
				t.Fatalf("error must contain %s; got %q", wantCmd, msg)
			}
			wantArgs := `"args":["` + c.wantArg + `"`
			if !strings.Contains(msg, wantArgs) {
				t.Fatalf("error must contain %s; got %q", wantArgs, msg)
			}
			wantDir := `"dir":"` + c.dirParam + `"`
			if !strings.Contains(msg, wantDir) {
				t.Fatalf("error must contain %s; got %q", wantDir, msg)
			}
		})
	}
}

// TestRunCommandRejectsBinaryArgsPacking pins the second LLM packing
// footgun: passing `command:"go build ./..."` with no `args`. That has
// no shell metas (so the shell-syntax detector lets it through) but
// becomes a bogus argv[0] that exec rejects with a useless "executable
// not found". The error must teach the split shape so the next round
// self-corrects in one step.
func TestRunCommandRejectsBinaryArgsPacking(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	cases := []struct {
		name        string
		command     string
		wantBin     string
		wantArg     string // first arg in the example
		wantInError string // substring that must appear
	}{
		{"go-build-pack", "go build ./...", "go", "build", `"command":"go"`},
		{"npm-test-pack", "npm test --watch=false", "npm", "test", `"command":"npm"`},
		{"git-log-pack", "git log -n 5", "git", "log", `"command":"git"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "run_command", Request{
				ProjectRoot: tmp,
				Params:      map[string]any{"command": c.command},
			})
			if err == nil {
				t.Fatalf("expected rejection of packed command %q", c.command)
			}
			msg := err.Error()
			if !strings.Contains(msg, "binary+arguments packed together") {
				t.Fatalf("error must explain the packing problem; got: %v", err)
			}
			if !strings.Contains(msg, c.wantInError) {
				t.Fatalf("error must include %s; got: %v", c.wantInError, err)
			}
			wantArgs := `"args":["` + c.wantArg + `"`
			if !strings.Contains(msg, wantArgs) {
				t.Fatalf("error must contain %s; got: %v", wantArgs, err)
			}
		})
	}
}

// TestRunCommandAllowsLegitimateSpacedPaths makes sure the packing
// detector does not false-positive on quoted paths or paths containing
// path separators (Windows `C:\Program Files\...\foo.exe`, Unix
// `/usr/local/my dir/bin`). When `args` is provided we also trust the
// model. The point is to detect *only* the bin+args-in-one-string case.
func TestRunCommandAllowsLegitimateSpacedPaths(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	// Quoted command — packing detector must skip it. Will fail later
	// in execution because the binary doesn't exist, but NOT with the
	// "binary+arguments packed together" error.
	_, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"command": `"foo bar.exe"`},
	})
	if err != nil && strings.Contains(err.Error(), "binary+arguments packed together") {
		t.Fatalf("quoted command must not be flagged as packed: %v", err)
	}

	// Path-with-spaces — packing detector must skip when the binary
	// token contains a separator.
	_, err = eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"command": `C:\Program Files\Go\bin\go.exe version`},
	})
	if err != nil && strings.Contains(err.Error(), "binary+arguments packed together") {
		t.Fatalf("path-with-spaces command must not be flagged as packed: %v", err)
	}

	// args is set — even with whitespace in command, trust the caller.
	_, err = eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command": "go test",
			"args":    "./...",
		},
	})
	if err != nil && strings.Contains(err.Error(), "binary+arguments packed together") {
		t.Fatalf("when args is set, command should not be flagged as packed: %v", err)
	}
}

func TestRunCommandToolBlocksShellInterpreter(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	_, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command": "powershell",
			"args":    "-Command Get-ChildItem",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "shell interpreters are blocked") {
		t.Fatalf("expected shell interpreter block, got %v", err)
	}
}

func TestRunCommandToolBlocksShellInterpreterInArgs(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	_, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command": "env",
			"args":    []any{"bash", "-lc", "echo nope"},
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "shell interpreters are blocked") {
		t.Fatalf("expected shell interpreter-in-args block, got %v", err)
	}
}

func TestRunCommandToolHonorsAllowShellFalse(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Security.Sandbox.AllowShell = false
	eng := New(*cfg)

	_, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command": "go",
			"args":    "version",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "allow_shell=false") {
		t.Fatalf("expected allow_shell=false error, got %v", err)
	}
}

// --- ensureCommandAllowed (block policy) ----------------------------
//
// The old substring approach false-positived legitimate commands
// like `go build -o format ./...` because "format " appeared inside
// the joined command string. Token-based matching fixes that. These
// tests pin BOTH behaviours — the new blocks that must fire AND the
// old false positives that must now succeed.

func TestEnsureCommandAllowed_BlocksDestructiveBinaries(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
	}{
		{"rm", "rm", []string{"-rf", "/tmp/foo"}},
		{"rm_with_exe", "rm.exe", []string{"-rf", "."}},
		{"rm_absolute_path", "/usr/bin/rm", []string{"whatever"}},
		{"mkfs", "mkfs", []string{"/dev/sda"}},
		{"dd", "dd", []string{"if=/dev/zero", "of=/dev/sda"}},
		{"sudo", "sudo", []string{"apt-get", "update"}},
		{"su", "su", []string{"-c", "whoami"}},
		{"shutdown", "shutdown", []string{"-r", "now"}},
		{"reboot", "reboot", nil},
		{"killall", "killall", []string{"sshd"}},
		{"pkill", "pkill", []string{"-9", "nginx"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureCommandAllowed(c.command, c.args, nil)
			if err == nil {
				t.Fatalf("expected block for %s %v, got nil", c.command, c.args)
			}
			if !strings.Contains(err.Error(), "blocked by policy") {
				t.Fatalf("error missing policy marker: %v", err)
			}
		})
	}
}

// These are commands the *old* substring policy wrongly blocked —
// the new token-based approach must let them through. This is the
// core regression guard.
func TestEnsureCommandAllowed_NoFalsePositivesOnLegitArgs(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
	}{
		// `go build -o format ./...` — old rule matched "format " in joined line.
		{"go_build_output_named_format", "go", []string{"build", "-o", "format", "./..."}},
		// `echo "mkfs is cool"` — old rule matched "mkfs" anywhere.
		{"echo_containing_mkfs", "echo", []string{"mkfs is cool"}},
		// `git log --grep "rm -rf"` — old rule matched the grep pattern.
		{"git_log_grep_destructive_string", "git", []string{"log", "--grep", "rm -rf"}},
		// Actual legitimate git flows.
		{"git_status", "git", []string{"status"}},
		{"git_diff_cached", "git", []string{"diff", "--cached"}},
		{"git_commit", "git", []string{"commit", "-m", "msg"}},
		// cat of a file named "format.go" — used to match "format ".
		{"cat_format_go", "cat", []string{"format.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ensureCommandAllowed(c.command, c.args, nil); err != nil {
				t.Fatalf("expected %s %v to be allowed, got %v", c.command, c.args, err)
			}
		})
	}
}

// Destructive git invocations must still be blocked by the
// structured arg-sequence check.
func TestEnsureCommandAllowed_GitDestructiveBlocked(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"reset_hard", []string{"reset", "--hard"}},
		{"reset_hard_with_ref", []string{"reset", "--hard", "HEAD~3"}},
		{"clean_fd", []string{"clean", "-fd"}},
		{"clean_fdx", []string{"clean", "-fdx"}},
		{"push_force", []string{"push", "--force"}},
		{"push_force_short", []string{"push", "-f", "origin", "main"}},
		{"push_force_with_lease", []string{"push", "--force-with-lease"}},
		{"checkout_discard", []string{"checkout", "--", "file.go"}},
		{"restore_source", []string{"restore", "--source", "HEAD"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureCommandAllowed("git", c.args, nil)
			if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
				t.Fatalf("git %v: expected block, got %v", c.args, err)
			}
		})
	}
}

// User-configured patterns remain substring-matched for back-compat
// with .dfmc/config.yaml — make sure a custom block still fires, and
// that an empty entry is harmless.
func TestEnsureCommandAllowed_UserPatternsStillSubstring(t *testing.T) {
	err := ensureCommandAllowed("curl", []string{"https://evil.example/install.sh"}, []string{"evil.example"})
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("user pattern should block, got %v", err)
	}
	if err := ensureCommandAllowed("curl", []string{"https://ok.example"}, []string{"", " "}); err != nil {
		t.Fatalf("empty user patterns should be no-op, got %v", err)
	}
}

// canonicalCommandBinary strips paths and .exe suffixes so the block
// check is platform-symmetric. If this classifier is ever changed, the
// Windows-parity guarantee for rm.exe / format.exe would silently
// break — pin it here.
func TestCanonicalCommandBinary(t *testing.T) {
	cases := map[string]string{
		"rm":               "rm",
		"RM":               "rm",
		"rm.exe":           "rm",
		"/usr/bin/rm":      "rm",
		"C:\\Windows\\rm":  "rm",
		" /usr/bin/rm.EXE": "rm",
		"sudo":             "sudo",
		"":                 "",
	}
	for in, want := range cases {
		if got := canonicalCommandBinary(in); got != want {
			t.Errorf("canonicalCommandBinary(%q) = %q, want %q", in, got, want)
		}
	}
}
