// Tests for the NO_COLOR / DFMC_NO_COLOR / TERM=dumb policy.
// shouldDisableColor is a pure function of environment — no side
// effects on the lipgloss global — so tests are straightforward.

package tui

import (
	"os"
	"testing"
)

func TestShouldDisableColor_DFMCOverride(t *testing.T) {
	t.Setenv("DFMC_NO_COLOR", "1")
	unsetEnv(t, "NO_COLOR")
	t.Setenv("TERM", "xterm-256color")
	if !shouldDisableColor() {
		t.Fatal("DFMC_NO_COLOR=1 must disable color")
	}
}

func TestShouldDisableColor_DFMCBlankDoesNotForce(t *testing.T) {
	t.Setenv("DFMC_NO_COLOR", "")
	unsetEnv(t, "NO_COLOR")
	t.Setenv("TERM", "xterm-256color")
	if shouldDisableColor() {
		t.Fatal("empty DFMC_NO_COLOR alone must not disable color")
	}
}

func TestShouldDisableColor_NoColorAnyValueDisables(t *testing.T) {
	// The no-color.org spec: NO_COLOR being SET at all disables
	// color, regardless of value. We use LookupEnv to match that
	// semantic, not a truthy/length check.
	unsetEnv(t, "DFMC_NO_COLOR")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !shouldDisableColor() {
		t.Fatal("setting NO_COLOR (even empty) must disable color")
	}
}

func TestShouldDisableColor_TermDumb(t *testing.T) {
	unsetEnv(t, "DFMC_NO_COLOR")
	unsetEnv(t, "NO_COLOR")
	t.Setenv("TERM", "dumb")
	if !shouldDisableColor() {
		t.Fatal("TERM=dumb must disable color")
	}
}

func TestShouldDisableColor_HealthyTerminal(t *testing.T) {
	unsetEnv(t, "DFMC_NO_COLOR")
	unsetEnv(t, "NO_COLOR")
	t.Setenv("TERM", "xterm-256color")
	if shouldDisableColor() {
		t.Fatal("no opt-out signals; color should stay enabled")
	}
}

// unsetEnv is t.Setenv's missing twin. t.Setenv only SETS; to make
// sure a variable is absent regardless of the host environment we
// call os.Unsetenv and register a Cleanup that restores the original.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestDefaultStr(t *testing.T) {
	cases := []struct {
		s        string
		fallback string
		want     string
	}{
		{"hello", "fallback", "hello"},
		{"", "empty", "empty"},
		{"  ", "spaces", "spaces"},
		{"  hello  ", "fallback", "  hello  "},
		{"", "", ""},
		{"text", "", "text"},
	}
	for _, c := range cases {
		got := defaultStr(c.s, c.fallback)
		if got != c.want {
			t.Errorf("defaultStr(%q, %q) = %q, want %q", c.s, c.fallback, got, c.want)
		}
	}
}
