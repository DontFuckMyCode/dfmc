package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestApplyPatchTracksCumulativeOffsetAcrossHunks pins the fix for a
// silent-partial-patch bug in applyHunks. In a unified diff every hunk's
// `@@ -OldStart,OldCount +NewStart,NewCount @@` uses ORIGINAL-file line
// coordinates - if hunk 1 net-adds N lines, the true anchor for hunk 2
// lives at OldStart + N in the mutable buffer. findHunkAnchor tolerates
// +-10 lines of drift; anything larger failed the content match and the
// hunk was rejected with no error surfaced to the caller (the outer
// Result carried Success:true and hunks_applied=1, hunks_rejected=1 -
// easy to miss until the next compile blew up on a half-applied change).
//
// The fix threads a running offset through applyHunks: each successful
// splice adds (len(middle) - len(want)) to the hint for every subsequent
// hunk. 20-line shift below exceeds the +-10 fuzz window on purpose.
func TestApplyPatchTracksCumulativeOffsetAcrossHunks(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")

	var ob strings.Builder
	for i := 1; i <= 50; i++ {
		ob.WriteString("l")
		if i < 10 {
			ob.WriteString("0")
		}
		ob.WriteString(itoaMH(i))
		ob.WriteByte('\n')
	}
	if err := os.WriteFile(target, []byte(ob.String()), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	patch := `--- a/file.txt
+++ b/file.txt
@@ -1,6 +1,26 @@
 l01
 l02
 l03
+new01
+new02
+new03
+new04
+new05
+new06
+new07
+new08
+new09
+new10
+new11
+new12
+new13
+new14
+new15
+new16
+new17
+new18
+new19
+new20
 l04
 l05
 l06
@@ -38,3 +58,3 @@
 l38
 l39
-l40
+L40
 l41
`

	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "file.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	res, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}

	// The per-file summary must show BOTH hunks applied. Pre-fix this
	// was 1/2 with hunks_rejected=1 and the test below would still
	// "succeed" at the outer layer - that is exactly the silent-partial
	// surface we are pinning against.
	files, _ := res.Data["files"].([]map[string]any)
	if len(files) != 1 {
		t.Fatalf("expected 1 file entry in result data, got %d: %+v", len(files), res.Data)
	}
	if got, _ := files[0]["hunks_applied"].(int); got != 2 {
		t.Fatalf("expected hunks_applied=2 (both hunks land), got %d; data=%+v", got, files[0])
	}
	if got, _ := files[0]["hunks_rejected"].(int); got != 0 {
		t.Fatalf("expected hunks_rejected=0, got %d; data=%+v", got, files[0])
	}

	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("read after: %v", rerr)
	}
	body := string(got)
	if !strings.Contains(body, "new20") {
		t.Fatalf("first hunk did not apply: new20 missing")
	}
	if !strings.Contains(body, "L40") {
		t.Fatalf("second hunk did not apply: L40 missing - offset between hunks was not threaded")
	}
	// And we did not damage l40's neighbours.
	if !strings.Contains(body, "l39\nL40\nl41\n") {
		t.Fatalf("second hunk misplaced L40; file body:\n%s", body)
	}
}

// TestApplyPatchTracksCumulativeOffset_Deletions is the symmetric pin:
// when an earlier hunk net-REMOVES more than the fuzz window, later
// hunks still need the offset. Without threading, the second hunk
// scans +-10 above the hint and finds nothing; patch is rejected.
func TestApplyPatchTracksCumulativeOffset_Deletions(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")

	var ob strings.Builder
	for i := 1; i <= 60; i++ {
		ob.WriteString("l")
		if i < 10 {
			ob.WriteString("0")
		}
		ob.WriteString(itoaMH(i))
		ob.WriteByte('\n')
	}
	if err := os.WriteFile(target, []byte(ob.String()), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Hunk 1 deletes 20 lines (l04..l23). Hunk 2 edits original l50.
	// Original coord of l50 is line 50, but after hunk 1 it lives at
	// line 30 in the buffer. 20-line negative shift, well past +-10.
	var pb strings.Builder
	pb.WriteString("--- a/file.txt\n+++ b/file.txt\n")
	pb.WriteString("@@ -1,25 +1,5 @@\n")
	pb.WriteString(" l01\n l02\n l03\n")
	for i := 4; i <= 23; i++ {
		pb.WriteString("-l")
		if i < 10 {
			pb.WriteString("0")
		}
		pb.WriteString(itoaMH(i))
		pb.WriteByte('\n')
	}
	pb.WriteString(" l24\n l25\n")
	pb.WriteString("@@ -48,3 +28,3 @@\n")
	pb.WriteString(" l48\n l49\n-l50\n+L50\n l51\n")

	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "file.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	res, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": pb.String()},
	})
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}

	files, _ := res.Data["files"].([]map[string]any)
	if got, _ := files[0]["hunks_rejected"].(int); got != 0 {
		t.Fatalf("expected no rejected hunks, got %d; data=%+v", got, files[0])
	}

	body, _ := os.ReadFile(target)
	if !strings.Contains(string(body), "L50") {
		t.Fatalf("second hunk did not apply after deletion shift; body:\n%s", string(body))
	}
	if strings.Contains(string(body), "l10") {
		t.Fatalf("deletion hunk did not remove l10; body:\n%s", string(body))
	}
}

func itoaMH(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}
