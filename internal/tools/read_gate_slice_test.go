package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestWriteFileAfterSlicedRead pins a silent-failure class the gate
// used to produce: read_file with line_start/line_end (or the default
// 200-line cap normalizeToolParams injects on bare reads) stored a hash
// of ONLY the returned segment, while write_file's strict gate compared
// against a disk-bytes hash. Any file taller than the returned window
// produced a "drift" refusal even though no one had touched the file.
// The fix is to store the full-file hash in Data["content_sha256"] and
// have recordReadSnapshot use it, so slice reads don't poison the gate.
func TestWriteFileAfterSlicedRead(t *testing.T) {
	tmp := t.TempDir()
	big := filepath.Join(tmp, "big.go")

	// 500 lines - comfortably past the 200-line default window, so a
	// bare read_file returns only the first 200 lines after
	// normalizeToolParams injects line_end = 200.
	var b strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&b, "// line %d\n", i)
	}
	if err := os.WriteFile(big, []byte(b.String()), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	eng := New(*config.DefaultConfig())

	// bare read_file - normalize injects line_start=1, line_end=200.
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "big.go"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	// write_file overwrite - strict gate compares against full-file
	// disk hash. Before the fix this refused with "drift" because the
	// stored hash was sha256(first 200 lines), not sha256(whole file).
	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":      "big.go",
			"content":   "// rewritten\n",
			"overwrite": true,
		},
	})
	if err != nil {
		t.Fatalf("write_file after sliced read refused by gate: %v", err)
	}
}

// TestWriteFileAfterExplicitSlicedRead covers the explicit line_start/
// line_end path - a user asking "read lines 10-40 of foo.go" then
// overwriting the whole file should not be blocked by a hash mismatch
// that has nothing to do with whether the file changed on disk.
func TestWriteFileAfterExplicitSlicedRead(t *testing.T) {
	tmp := t.TempDir()
	fp := filepath.Join(tmp, "mid.go")
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "// line %d\n", i)
	}
	if err := os.WriteFile(fp, []byte(b.String()), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	eng := New(*config.DefaultConfig())

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "mid.go", "line_start": 10, "line_end": 40},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":      "mid.go",
			"content":   "// rewritten\n",
			"overwrite": true,
		},
	})
	if err != nil {
		t.Fatalf("write_file after explicit sliced read refused by gate: %v", err)
	}
}

// TestWriteFileStillRefusesOnActualDrift is the negative: if something
// OTHER than our own slice-hash bug causes the on-disk bytes to differ
// from the snapshot, the strict gate must still refuse. Fix must not
// turn the gate into a rubber stamp.
func TestWriteFileStillRefusesOnActualDrift(t *testing.T) {
	tmp := t.TempDir()
	fp := filepath.Join(tmp, "drifty.go")
	if err := os.WriteFile(fp, []byte("original\n"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	eng := New(*config.DefaultConfig())

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "drifty.go"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	// External writer touches the file between read and write.
	if err := os.WriteFile(fp, []byte("mutated\n"), 0600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":      "drifty.go",
			"content":   "from model\n",
			"overwrite": true,
		},
	})
	if err == nil {
		t.Fatalf("expected drift refusal after external write, got nil")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("expected drift-style error, got: %v", err)
	}
}
