package tui

import (
	"testing"
)

func TestLooksLikeActionRequest_DetectsTurkishVerbs(t *testing.T) {
	m := newCoverageModel(t)
	cases := []struct {
		input string
		want  bool
	}{
		// With .go extension - should be true
		{"güncelle main.go", true},
		{"guncelle main.py", true},
		{"yaz test.ts", true},
		{"düzelt bug.go", true},
		{"değiştir config.go", true},
		{"ekle yeni.go", true},
		{"kaldır gereksiz.go", true},
		{"sil satır.go", true},
		{"refactor this file.py", true},
		{"edit the config.go", true},
		{"fix the bug.js", true},
		// With [[file:]] marker - should be true
		{"güncelle [[file:app.go]]", true},
		{"düzelt [[file:main.go]]", true},
		// No verb or no file target - should be false
		{"what is 2+2", false},
		{"explain this code", false},
		{"show me the docs", false},
		{"düzelt bug", false}, // has verb but no file target
	}
	for _, c := range cases {
		got := m.looksLikeActionRequest(c.input)
		if got != c.want {
			t.Errorf("looksLikeActionRequest(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestLooksLikeActionRequest_RequiresFileMarkerOrExtension(t *testing.T) {
	m := newCoverageModel(t)
	// Bare verb without file target should be false
	if m.looksLikeActionRequest("refactor") {
		t.Error("bare 'refactor' should not be action request without file")
	}
	// Verb with .go extension should be true
	if !m.looksLikeActionRequest("refactor server.go") {
		t.Error("'refactor server.go' should be action request")
	}
	// Verb with [[file:]] marker should be true
	if !m.looksLikeActionRequest("fix [[file:main.go]]") {
		t.Error("'fix [[file:main.go]]' should be action request")
	}
}

func TestLooksLikeActionRequest_EmptyInput(t *testing.T) {
	m := newCoverageModel(t)
	if m.looksLikeActionRequest("") {
		t.Error("empty input should not be action request")
	}
	if m.looksLikeActionRequest("   ") {
		t.Error("whitespace input should not be action request")
	}
}

func TestHasToolCapableProvider(t *testing.T) {
	m := newCoverageModel(t)
	// Without engine, should return false
	if m.hasToolCapableProvider() {
		t.Error("hasToolCapableProvider should be false without engine")
	}
}

func TestIntentChipLabel_EmptyWhenNeverFired(t *testing.T) {
	s := intentState{}
	if intentChipLabel(s) != "" {
		t.Error("intentChipLabel should be empty when lastDecisionAtMs is 0")
	}
}

func TestIntentChipLabel_EmptyWhenSourceNotLLM(t *testing.T) {
	s := intentState{
		lastDecisionAtMs: 1234567890,
		lastSource:        "fallback",
		lastIntent:       "resume",
	}
	if intentChipLabel(s) != "" {
		t.Error("intentChipLabel should be empty when source is not 'llm'")
	}
}

func TestIntentChipLabel_ReturnsIntentWhenValid(t *testing.T) {
	s := intentState{
		lastDecisionAtMs: 1234567890,
		lastSource:       "llm",
		lastIntent:       "resume",
	}
	got := intentChipLabel(s)
	if got != "resume" {
		t.Errorf("intentChipLabel = %q, want %q", got, "resume")
	}
}

func TestIntentChipLabel_ReturnsNew(t *testing.T) {
	s := intentState{
		lastDecisionAtMs: 1234567890,
		lastSource:       "llm",
		lastIntent:       "new",
	}
	got := intentChipLabel(s)
	if got != "new" {
		t.Errorf("intentChipLabel = %q, want %q", got, "new")
	}
}

func TestIntentChipLabel_ReturnsClarify(t *testing.T) {
	s := intentState{
		lastDecisionAtMs: 1234567890,
		lastSource:       "llm",
		lastIntent:       "clarify",
	}
	got := intentChipLabel(s)
	if got != "clarify" {
		t.Errorf("intentChipLabel = %q, want %q", got, "clarify")
	}
}

func TestSplitExecutableAndArgs(t *testing.T) {
	cases := []struct {
		raw     string
		wantCmd string
		wantArgs string
	}{
		{"", "", ""},
		{"  ", "", ""},
		{`"echo hello" world`, "echo hello", "world"},
		{`"multi word" arg1 arg2`, "multi word", "arg1 arg2"},
		{"single", "single", ""},
		{"cmd arg1 arg2", "cmd", "arg1 arg2"},
		{"  cmd  arg  ", "cmd", "arg"},
		{"echo hello world", "echo", "hello world"},
	}
	for _, c := range cases {
		cmd, args := splitExecutableAndArgs(c.raw)
		if cmd != c.wantCmd {
			t.Errorf("splitExecutableAndArgs(%q): cmd = %q, want %q", c.raw, cmd, c.wantCmd)
		}
		if args != c.wantArgs {
			t.Errorf("splitExecutableAndArgs(%q): args = %q, want %q", c.raw, args, c.wantArgs)
		}
	}
}
