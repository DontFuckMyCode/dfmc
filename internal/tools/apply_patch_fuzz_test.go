package tools

import (
	"strings"
	"testing"
)

// FuzzApplyPatchPipeline drives the unified-diff parser and hunk applier over
// arbitrary (patch, content) inputs. Patches come straight from the model, so
// the whole pipeline must be panic-proof on malformed input: bad @@ headers,
// truncated hunks, huge/negative line numbers, mixed CRLF, unterminated files.
// Invariants:
//   - parseUnifiedDiff never panics and never returns a hunk whose declared
//     counts are negative,
//   - applyHunks never panics, reports applied+rejected == len(hunks) for an
//     existing file, and never claims to apply more hunks than it was given.
func FuzzApplyPatchPipeline(f *testing.F) {
	seeds := []struct{ patch, content string }{
		{"--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-old\n+new\n", "old\n"},
		{"@@ -1 +1 @@\n-a\n+b\n", "a\n"},
		{"--- a/x\n+++ b/x\n@@ -1,2 +1,3 @@\n ctx\n-rm\n+add1\n+add2\n", "ctx\nrm\n"},
		{"garbage not a diff", "whatever"},
		{"@@ -99999999999999999999,1 +1,1 @@\n-x\n+y\n", "x\n"},
		{"@@ -1,1 +1,1 @@", ""},
		{"--- a/n\n+++ b/n\n@@ -0,0 +1,1 @@\n+brand new\n", ""},
		{"--- a/x\r\n+++ b/x\r\n@@ -1 +1 @@\r\n-a\r\n+b\r\n", "a\r\n"},
	}
	for _, s := range seeds {
		f.Add(s.patch, s.content)
	}

	f.Fuzz(func(t *testing.T, patch, content string) {
		files, err := parseUnifiedDiff(patch)
		if err != nil {
			return // a rejected malformed patch is a fine outcome
		}
		for _, df := range files {
			for _, h := range df.Hunks {
				if h.OldCount < 0 || h.NewCount < 0 {
					t.Fatalf("parsed hunk with negative count: old=%d new=%d", h.OldCount, h.NewCount)
				}
			}

			out, applied, rejected, fuzzyOffsets, aerr := applyHunks(content, df.Hunks, df.IsNew)
			if aerr != nil {
				continue
			}
			if applied < 0 || rejected < 0 {
				t.Fatalf("negative counts: applied=%d rejected=%d", applied, rejected)
			}
			if applied > len(df.Hunks) {
				t.Fatalf("applied=%d exceeds hunk count %d", applied, len(df.Hunks))
			}
			// For an existing file every hunk is either applied or rejected.
			if !df.IsNew && applied+rejected != len(df.Hunks) {
				t.Fatalf("applied(%d)+rejected(%d) != hunks(%d)", applied, rejected, len(df.Hunks))
			}
			if len(fuzzyOffsets) > applied {
				t.Fatalf("more fuzzy offsets (%d) than applied hunks (%d)", len(fuzzyOffsets), applied)
			}
			// A non-new-file apply with no applied hunks must leave content
			// byte-identical (nothing spliced).
			if !df.IsNew && applied == 0 && out != content {
				t.Fatalf("zero hunks applied but content changed:\n in=%q\nout=%q", content, out)
			}
			_ = strings.Count(out, "\n")
		}
	})
}
