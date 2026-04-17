package cli

import (
	"testing"
)

func TestLooksLikeCommandTypo_ShortSingleToken(t *testing.T) {
	if !looksLikeCommandTypo("docter", nil) {
		t.Fatal("bare short alpha token should be treated as a possible typo")
	}
	if !looksLikeCommandTypo("anal", nil) {
		t.Fatal("short alpha token should qualify")
	}
}

func TestLooksLikeCommandTypo_RejectsLongQuestion(t *testing.T) {
	if looksLikeCommandTypo("what", []string{"is", "the", "current", "provider", "and", "how", "is", "it", "configured"}) {
		t.Fatal("a full sentence must pass through to `ask` without typo-checking")
	}
}

func TestLooksLikeCommandTypo_RejectsLongToken(t *testing.T) {
	if looksLikeCommandTypo("extremelylongword", nil) {
		t.Fatal("a >14-char token is unlikely to be a command typo")
	}
}

func TestLooksLikeCommandTypo_RejectsNonAlpha(t *testing.T) {
	if looksLikeCommandTypo("1+1", nil) {
		t.Fatal("numeric / punctuated tokens should not be typo-checked")
	}
	if looksLikeCommandTypo("hi!", nil) {
		t.Fatal("punctuation should skip the typo path")
	}
}

func TestSuggestCLICommand_PrefixBeats(t *testing.T) {
	got := suggestCLICommand("anal")
	if got != "analyze" {
		t.Fatalf("`anal` should suggest `analyze`, got %q", got)
	}
}

func TestSuggestCLICommand_OneEditDistance(t *testing.T) {
	cases := map[string]string{
		"docter": "doctor",
		"intit":  "init",
		"memroy": "memory",
	}
	for typo, want := range cases {
		if got := suggestCLICommand(typo); got != want {
			t.Errorf("suggestCLICommand(%q) = %q, want %q", typo, got, want)
		}
	}
}

func TestSuggestCLICommand_ReturnsEmptyWhenFar(t *testing.T) {
	// "zzz" is nothing like any command.
	if got := suggestCLICommand("zzzz"); got != "" {
		t.Fatalf("far-off input should return empty, got %q", got)
	}
}

func TestEditDistanceAtMost(t *testing.T) {
	cases := []struct {
		a, b     string
		max      int
		expected bool
	}{
		{"docter", "doctor", 1, true},
		{"intit", "init", 1, true},
		{"memroy", "memory", 1, true},
		{"abc", "xyz", 1, false},
		{"abc", "abcd", 1, true},
		{"abcd", "abc", 1, true},
		{"abc", "abcde", 1, false},
		{"hello", "hello", 0, true},
	}
	for _, c := range cases {
		if got := editDistanceAtMost(c.a, c.b, c.max); got != c.expected {
			t.Errorf("editDistanceAtMost(%q, %q, %d) = %v, want %v", c.a, c.b, c.max, got, c.expected)
		}
	}
}

func TestKnownCLICommands_IncludesCoreVerbs(t *testing.T) {
	cmds := knownCLICommands()
	want := []string{"ask", "chat", "analyze", "doctor", "init", "tool", "memory", "conversation", "status"}
	seen := map[string]bool{}
	for _, c := range cmds {
		seen[c] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("knownCLICommands missing %q", w)
		}
	}
}
