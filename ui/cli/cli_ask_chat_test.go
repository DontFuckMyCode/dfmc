package cli

import (
	"context"
	"strings"
	"testing"
)

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "anthropic", []string{"anthropic"}},
		{"two", "anthropic,openai", []string{"anthropic", "openai"}},
		{"spaces", "anthropic, openai , deepseek", []string{"anthropic", "openai", "deepseek"}},
		{"empty_middle", "anthropic,,deepseek", []string{"anthropic", "deepseek"}},
		{"trailing_comma", "anthropic,", []string{"anthropic"}},
		{"leading_comma", ",anthropic", []string{"anthropic"}},
		{"all_empty", ",,,", nil},
	}
	for _, c := range cases {
		got := splitCSV(c.input)
		if !equalStringSlice(got, c.want) {
			t.Errorf("%s: splitCSV(%q)=%v want %v", c.name, c.input, got, c.want)
		}
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunAsk_NoQuestion(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runAsk(context.Background(), eng, []string{}, false)
	if code != 2 {
		t.Errorf("expected exit 2 for empty question, got %d", code)
	}
}

func TestRunAsk_TextQuestion(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runAsk(context.Background(), eng, []string{"what is 2+2"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAsk_MultipleArgs(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runAsk(context.Background(), eng, []string{"what", "is", "the", "time"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAsk_JSONMode(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runAsk(context.Background(), eng, []string{"hello"}, true)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "answer") {
		t.Errorf("expected 'answer' key in JSON output: %s", out)
	}
	if !containsJSONKey(out, "question") {
		t.Errorf("expected 'question' key in JSON output: %s", out)
	}
}

func TestRunAsk_RaceMode(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runAsk(context.Background(), eng, []string{"--race", "--race-providers", "offline", "what is 1+1"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "(won by") {
		t.Errorf("expected race winner in output: %s", out)
	}
}

func TestRunAsk_RaceModeJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runAsk(context.Background(), eng, []string{"--race", "--race-providers", "offline", "hello"}, true)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "winner") {
		t.Errorf("expected 'winner' key in race JSON output: %s", out)
	}
	if !containsJSONKey(out, "candidates") {
		t.Errorf("expected 'candidates' key in race JSON output: %s", out)
	}
	if !containsJSONKey(out, "mode") {
		t.Errorf("expected 'mode' key in race JSON output: %s", out)
	}
}

func TestRunAsk_RaceWithProviders(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		code := runAsk(context.Background(), eng, []string{"--race", "--race-providers", "offline", "what is 2+2"}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "(won by") {
		t.Errorf("expected race winner in output: %s", out)
	}
}

func TestRunAsk_UnknownFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runAsk(context.Background(), eng, []string{"--unknown-flag"}, false)
	if code != 2 {
		t.Errorf("expected exit 2 for unknown flag, got %d", code)
	}
}