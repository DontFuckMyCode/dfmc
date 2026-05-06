package cli

import (
	"strings"
	"testing"
)

func TestFormatConfigSubcommandError_ListsAllSubcommands(t *testing.T) {
	out := formatConfigSubcommandError("")
	for _, want := range []string{"list", "get", "set", "sync-models", "edit"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing subcommand %q in output:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "usage: dfmc config <subcommand>") {
		t.Errorf("missing usage line in output:\n%s", out)
	}
	if !strings.Contains(out, "dfmc help config") {
		t.Errorf("missing help pointer in output:\n%s", out)
	}
	if strings.Contains(out, "unknown subcommand") {
		t.Errorf("empty typo should not produce 'unknown subcommand' line:\n%s", out)
	}
}

func TestFormatConfigSubcommandError_TypoSuggestions(t *testing.T) {
	cases := []struct {
		typo, suggest string
	}{
		{"gett", "get"},
		{"lst", "list"},
		{"sync-model", "sync-models"},
		{"edt", "edit"},
		{"se", "set"}, // prefix match
	}
	for _, tc := range cases {
		out := formatConfigSubcommandError(tc.typo)
		if !strings.Contains(out, "unknown subcommand") {
			t.Errorf("%q: expected 'unknown subcommand' header, got:\n%s", tc.typo, out)
		}
		want := "Did you mean \"" + tc.suggest + "\""
		if !strings.Contains(out, want) {
			t.Errorf("%q: expected suggestion %q in output:\n%s", tc.typo, want, out)
		}
	}
}

func TestFormatConfigSubcommandError_NoSuggestionForGibberish(t *testing.T) {
	out := formatConfigSubcommandError("xyzqqqq")
	if !strings.Contains(out, "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' header:\n%s", out)
	}
	if strings.Contains(out, "Did you mean") {
		t.Errorf("gibberish should not produce a 'Did you mean' suggestion:\n%s", out)
	}
	for _, want := range []string{"list", "get", "set", "sync-models", "edit"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing subcommand %q in output:\n%s", want, out)
		}
	}
}

func TestSuggestConfigSub_PrefixOverDistance(t *testing.T) {
	subs := []struct{ name, summary string }{
		{"list", ""},
		{"get", ""},
		{"set", ""},
		{"sync-models", ""},
		{"edit", ""},
	}
	if got := suggestConfigSub("li", subs); got != "list" {
		t.Errorf("prefix 'li' -> want 'list', got %q", got)
	}
	if got := suggestConfigSub("zzz", subs); got != "" {
		t.Errorf("far typo -> want empty, got %q", got)
	}
}
