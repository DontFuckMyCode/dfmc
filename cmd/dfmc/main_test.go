package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/storage"
)

// --- allowsDegradedStartup ---

func TestAllowsDegradedStartup_HelpVariants(t *testing.T) {
	for _, cmd := range []string{"help", "-h", "--help"} {
		if !allowsDegradedStartup([]string{cmd}) {
			t.Errorf("allowsDegradedStartup(%q) = false, want true", cmd)
		}
	}
}

func TestAllowsDegradedStartup_SecondaryCommands(t *testing.T) {
	for _, cmd := range []string{"version", "completion", "man", "update"} {
		if !allowsDegradedStartup([]string{cmd}) {
			t.Errorf("allowsDegradedStartup(%q) = false, want true", cmd)
		}
	}
}

func TestAllowsDegradedStartup_Doctor(t *testing.T) {
	if !allowsDegradedStartup([]string{"doctor"}) {
		t.Error("allowsDegradedStartup([doctor]) = false, want true")
	}
}

func TestAllowsDegradedStartup_GlobalFlagsThenCommand(t *testing.T) {
	cases := [][]string{
		{"--json", "version"},
		{"--no-color", "--json", "version"},
		{"-v", "help"},
		{"--verbose", "--json", "completion"},
	}
	for _, args := range cases {
		if !allowsDegradedStartup(args) {
			t.Errorf("allowsDegradedStartup(%v) = false, want true", args)
		}
	}
}

func TestAllowsDegradedStartup_NonDegradedCommand(t *testing.T) {
	cases := [][]string{
		{"tui"},
		{"ask"},
		{"chat"},
		{"analyze"},
		{"review"},
		{"drive"},
		{"serve"},
		{"scan"},
		{"init"},
		{"tui", "--json"},
		{"ask", "--json", "hello"},
	}
	for _, args := range cases {
		if allowsDegradedStartup(args) {
			t.Errorf("allowsDegradedStartup(%v) = true, want false", args)
		}
	}
}

func TestAllowsDegradedStartup_EmptyArgs(t *testing.T) {
	if !allowsDegradedStartup([]string{}) {
		t.Error("allowsDegradedStartup({}) = false, want true for empty-args (help) path")
	}
}

func TestAllowsDegradedStartup_WhitespaceOnly(t *testing.T) {
	if !allowsDegradedStartup([]string{"", " ", "  "}) {
		t.Error("allowsDegradedStartup with only whitespace should return true")
	}
}

func TestAllowsDegradedStartup_FlagsOnly(t *testing.T) {
	if !allowsDegradedStartup([]string{"--verbose", "--json"}) {
		t.Error("allowsDegradedStartup(flags only) should return true")
	}
}

// --- formatInitError ---

func TestFormatInitError_Nil(t *testing.T) {
	got := formatInitError(nil)
	if got != "" {
		t.Errorf("formatInitError(nil) = %q, want \"\"", got)
	}
}

func TestFormatInitError_StoreLocked(t *testing.T) {
	err := storage.ErrStoreLocked
	got := formatInitError(err)
	if got == "" {
		t.Fatal("formatInitError(storage.ErrStoreLocked) returned empty string")
	}
	if !strings.Contains(got, "dfmc doctor") {
		t.Errorf("expected 'dfmc doctor' guidance, got %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "locked") {
		t.Errorf("expected 'locked' wording, got %q", got)
	}
}

func TestFormatInitError_StoreLockedWithTimeout(t *testing.T) {
	err := errors.Join(storage.ErrStoreLocked, errors.New("database is locked by another process"))
	got := formatInitError(err)
	if !strings.Contains(got, "dfmc doctor") {
		t.Errorf("expected doctor guidance in wrapped error, got %q", got)
	}
}

func TestFormatInitError_Generic(t *testing.T) {
	err := errors.New("some other error")
	got := formatInitError(err)
	if got != err.Error() {
		t.Errorf("formatInitError(generic) = %q, want %q", got, err.Error())
	}
}

// --- suppressInitWarning ---

// Quiet meta commands: they never touch the store so the lock warning
// would be an irrelevant distraction.
func TestSuppressInitWarning_QuietMetaCommands(t *testing.T) {
	for _, cmd := range []string{"help", "version", "completion", "man"} {
		if !suppressInitWarning([]string{cmd}) {
			t.Errorf("suppressInitWarning(%q) = false, want true", cmd)
		}
	}
}

// Diagnostic commands keep the warning — the lock state IS the signal.
func TestSuppressInitWarning_KeepsDiagnostics(t *testing.T) {
	for _, cmd := range []string{"doctor", "update"} {
		if suppressInitWarning([]string{cmd}) {
			t.Errorf("suppressInitWarning(%q) = true, want false (diagnostic should still print warning)", cmd)
		}
	}
}

// Real commands (`ask`, `chat`, ...) never reach this path — they hit
// the non-degraded `init error` branch instead — but if they did, the
// warning must NOT be suppressed.
func TestSuppressInitWarning_KeepsForRealCommands(t *testing.T) {
	for _, cmd := range []string{"ask", "chat", "review", "drive"} {
		if suppressInitWarning([]string{cmd}) {
			t.Errorf("suppressInitWarning(%q) = true, want false", cmd)
		}
	}
}

// Global flags before a quiet command don't change the verdict.
func TestSuppressInitWarning_FlagsBeforeCommand(t *testing.T) {
	if !suppressInitWarning([]string{"--verbose", "--json", "help"}) {
		t.Error("flags before help should still suppress")
	}
}

// Only leading non-flag args determine the verdict.
func TestSuppressInitWarning_FlagsBeforeRealCommand(t *testing.T) {
	if suppressInitWarning([]string{"--verbose", "ask", "--json"}) {
		t.Error("flags before a real command should NOT suppress")
	}
}

// Empty args defaults to help-path: suppress.
func TestSuppressInitWarning_Empty(t *testing.T) {
	if !suppressInitWarning([]string{}) {
		t.Error("suppressInitWarning({}) = false, want true")
	}
}

// Whitespace-only args: treated same as empty.
func TestSuppressInitWarning_WhitespaceOnly(t *testing.T) {
	if !suppressInitWarning([]string{"", " ", "  "}) {
		t.Error("whitespace-only args should suppress")
	}
}

// --- extractDataDir ---

func TestExtractDataDir_NotPresent(t *testing.T) {
	if got := extractDataDir([]string{"ask", "hello"}); got != "" {
		t.Errorf("extractDataDir([ask, hello]) = %q, want \"\"", got)
	}
}

func TestExtractDataDir_ExplicitFlag(t *testing.T) {
	if got := extractDataDir([]string{"--data-dir", "/custom/path"}); got != "/custom/path" {
		t.Errorf("extractDataDir([--data-dir, /custom/path]) = %q, want %q", got, "/custom/path")
	}
}

func TestExtractDataDir_EqualsForm(t *testing.T) {
	if got := extractDataDir([]string{"--data-dir=/custom/path"}); got != "/custom/path" {
		t.Errorf("extractDataDir([--data-dir=/custom/path]) = %q, want %q", got, "/custom/path")
	}
}

func TestExtractDataDir_WithLeadingFlags(t *testing.T) {
	got := extractDataDir([]string{"--verbose", "--data-dir=/my/data", "ask", "hello"})
	if got != "/my/data" {
		t.Errorf("extractDataDir with leading flags = %q, want %q", got, "/my/data")
	}
}

func TestExtractDataDir_TrailingFlags(t *testing.T) {
	got := extractDataDir([]string{"ask", "--data-dir=/my/data", "--json"})
	if got != "/my/data" {
		t.Errorf("extractDataDir with trailing flags = %q, want %q", got, "/my/data")
	}
}

func TestExtractDataDir_LastPosition(t *testing.T) {
	got := extractDataDir([]string{"--verbose", "--no-color", "--data-dir=/data"})
	if got != "/data" {
		t.Errorf("extractDataDir at last position = %q, want %q", got, "/data")
	}
}


