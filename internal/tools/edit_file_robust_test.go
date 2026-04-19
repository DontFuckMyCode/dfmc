// Robustness tests for edit_file — the tool that an agentic loop leans
// on hardest. Every weak error path translates to a wasted round-trip,
// so we pin the behavior that separates a flaky Edit from a usable one.

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// readThenEdit satisfies the engine's "read before edit" guard and
// returns the engine + abs path so tests can keep focused on edit_file
// semantics instead of replaying setup.
func readThenEdit(t *testing.T, contents string, filename string) (*Engine, string, string) {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, filename)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": filename},
	}); err != nil {
		t.Fatalf("seed read_file: %v", err)
	}
	return eng, tmp, filename
}

func TestEditFile_CRLFFilesMatchLFNeedle(t *testing.T) {
	// Agent running on Linux / LLM output almost always has LF only.
	// File on disk (written on Windows or checked out with autocrlf)
	// has CRLF. Pre-rewrite this silently said "not found" and burned
	// a tool round. The normalized matcher must succeed here.
	src := "line one\r\nline two\r\nline three\r\n"
	eng, tmp, name := readThenEdit(t, src, "windows.txt")

	if _, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "line two",
			"new_string": "LINE TWO",
		},
	}); err != nil {
		t.Fatalf("CRLF file with LF needle should match, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(tmp, name))
	if !strings.Contains(string(got), "LINE TWO") {
		t.Fatalf("edit did not take effect: %q", string(got))
	}
	// Line endings MUST be preserved — we didn't ask to switch styles.
	if !strings.Contains(string(got), "\r\n") {
		t.Fatalf("CRLF file was rewritten as LF — line endings must be preserved: %q", string(got))
	}
}

func TestEditFile_LFFilesMatchCRLFNeedle(t *testing.T) {
	// The inverse — file is LF, agent happened to send CRLF in old_string.
	// Should still match and file should stay LF afterward.
	src := "alpha\nbeta\ngamma\n"
	eng, tmp, name := readThenEdit(t, src, "unix.txt")

	if _, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "beta\r\n",
			"new_string": "BETA\r\n",
		},
	}); err != nil {
		t.Fatalf("LF file with CRLF needle should match, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(tmp, name))
	if strings.Contains(string(got), "\r\n") {
		t.Fatalf("LF file was rewritten with CRLF: %q", string(got))
	}
}

func TestEditFile_MissErrorMentionsWhitespaceHint(t *testing.T) {
	// Agent sent old_string with extra leading whitespace; the trimmed
	// form matches. Error must flag this so the retry corrects course.
	src := "func Foo() {\n    return 1\n}\n"
	eng, tmp, name := readThenEdit(t, src, "miss.go")

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "  return 1  ", // extra whitespace
			"new_string": "return 2",
		},
	})
	if err == nil {
		t.Fatal("expected error on whitespace mismatch")
	}
	if !strings.Contains(err.Error(), "trimmed") && !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("error should hint at whitespace, got: %v", err)
	}
}

func TestEditFile_MissErrorMentionsIndentation(t *testing.T) {
	// Agent sent wrong indentation (tabs vs spaces). Error should say so.
	src := "func Foo() {\n\treturn 1\n}\n"
	eng, tmp, name := readThenEdit(t, src, "indent.go")

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "    return 1", // spaces, file uses tabs
			"new_string": "    return 2",
		},
	})
	if err == nil {
		t.Fatal("expected error on indentation mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tab") && !strings.Contains(msg, "indent") {
		t.Fatalf("error should mention tabs or indentation, got: %v", err)
	}
}

func TestEditFile_AmbiguityErrorIncludesLineNumbers(t *testing.T) {
	// Non-unique old_string must tell the agent *where* the matches
	// are so it can anchor the retry.
	src := "var x = 1\nvar y = 2\nvar x = 3\n"
	eng, tmp, name := readThenEdit(t, src, "dup.go")

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "var x",
			"new_string": "var q",
		},
	})
	if err == nil {
		t.Fatal("expected uniqueness error")
	}
	if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("ambiguity error should list matching line numbers, got: %v", err)
	}
	if !strings.Contains(err.Error(), "2 matches") {
		t.Fatalf("ambiguity error should include match count, got: %v", err)
	}
}

func TestEditFile_AmbiguityMultilineNeedleListsOnlyRealStarts(t *testing.T) {
	src := "foo\nzzz\nfoo\nbar\nfoo\nbar\n"
	eng, tmp, name := readThenEdit(t, src, "multi.txt")

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "foo\nbar",
			"new_string": "baz\nbar",
		},
	})
	if err == nil {
		t.Fatal("expected multiline uniqueness error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "line 3") || !strings.Contains(msg, "line 5") {
		t.Fatalf("expected real multiline start lines, got: %v", err)
	}
	if strings.Contains(msg, "line 1") {
		t.Fatalf("ambiguity error must not report partial-line false positives, got: %v", err)
	}
}

func TestEditFile_RejectsIdenticalOldNewString(t *testing.T) {
	src := "hello world\n"
	eng, tmp, name := readThenEdit(t, src, "identity.txt")

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "hello",
			"new_string": "hello",
		},
	})
	if err == nil {
		t.Fatal("edit_file must reject identical old/new strings")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "identical") {
		t.Fatalf("error should mention identical strings, got: %v", err)
	}
}

func TestEditFile_OutputReportsReplacementCount(t *testing.T) {
	src := "foo\nfoo\nbar\n"
	eng, tmp, name := readThenEdit(t, src, "count.txt")

	res, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":        name,
			"old_string":  "foo",
			"new_string":  "baz",
			"replace_all": true,
		},
	})
	if err != nil {
		t.Fatalf("replace_all: %v", err)
	}
	if !strings.Contains(res.Output, "2 replacements") {
		t.Fatalf("output should report replacement count, got: %q", res.Output)
	}
}

func TestEditFile_PreservesMixedLineEndings(t *testing.T) {
	src := "alpha\r\nbeta\ngamma\r\n"
	eng, tmp, name := readThenEdit(t, src, "mixed.txt")

	if _, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       name,
			"old_string": "beta",
			"new_string": "BETA",
		},
	}); err != nil {
		t.Fatalf("edit_file on mixed-endings file: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(tmp, name))
	if string(got) != "alpha\r\nBETA\ngamma\r\n" {
		t.Fatalf("mixed line endings should be preserved per-line, got %q", string(got))
	}
}
