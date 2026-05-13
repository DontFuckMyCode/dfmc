package providerlog

import (
	"strings"
	"testing"
)

func TestRecord_PersistsAndTails(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir)
	if err != nil || l == nil {
		t.Fatalf("New: %v / %v", l, err)
	}
	l.Record(map[string]any{
		"provider":          "anthropic",
		"model":             "claude-opus-4-6",
		"input_tokens":      1234,
		"output_tokens":     567,
		"total_tokens":      1801,
		"source":            "ask",
		"user_preview":      "fix the bug",
		"assistant_preview": "found the off-by-one in foo()",
	})
	got := l.Tail(10)
	if len(got) != 1 {
		t.Fatalf("Tail count: want 1, got %d", len(got))
	}
	r := got[0]
	if r.Provider != "anthropic" || r.Model != "claude-opus-4-6" {
		t.Errorf("provider/model: %q/%q", r.Provider, r.Model)
	}
	if r.InputTokens != 1234 || r.OutputTokens != 567 || r.TotalTokens != 1801 {
		t.Errorf("tokens in/out/total: %d/%d/%d", r.InputTokens, r.OutputTokens, r.TotalTokens)
	}
	if !strings.Contains(r.UserPreview, "fix the bug") {
		t.Errorf("user_preview lost: %q", r.UserPreview)
	}
	if !strings.Contains(r.AssistantPreview, "off-by-one") {
		t.Errorf("assistant_preview lost: %q", r.AssistantPreview)
	}
}

func TestRecord_NilSafeOnEmptyPayload(t *testing.T) {
	dir := t.TempDir()
	l, _ := New(dir)
	l.Record(nil)
	l.Record(map[string]any{}) // entirely useless — should be silently dropped
	if got := l.Tail(10); len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestNew_RejectsEmptyDir(t *testing.T) {
	l, err := New("")
	if l != nil || err != nil {
		t.Errorf("New(\"\") should return nil,nil; got %v/%v", l, err)
	}
}

func TestNilLogger_TailDirCloseAreSafe(t *testing.T) {
	var l *Logger
	if got := l.Tail(5); got != nil {
		t.Errorf("nil Tail: want nil, got %v", got)
	}
	if got := l.Dir(); got != "" {
		t.Errorf("nil Dir: want empty, got %q", got)
	}
	if err := l.Close(); err != nil {
		t.Errorf("nil Close: want nil err, got %v", err)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"abcdefghij", 10, "abcdefghij"},
		{"abcdefghijklm", 10, "abcdefghi…"},
		{"unicode köy", 8, "unicode…"},
		{"x", 0, "x"}, // max<=0 passes through
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q,%d)=%q want %q", c.in, c.max, got, c.want)
		}
	}
}
