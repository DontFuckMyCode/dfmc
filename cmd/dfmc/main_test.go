package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/storage"
)

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
	// Global flags before the command should not prevent degraded startup.
	// Note: --provider and --model take a value argument that is NOT in the
	// degraded-allow list, so they cannot precede a degraded command without
	// the real command being consumed as the flag value.
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
	// Any non-whitelisted command must NOT allow degraded startup.
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
	// Empty args slice means no command was given — treat as degraded
	// (run() prints help and exits gracefully).
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
	// Only flags, no command — degraded (prints help).
	if !allowsDegradedStartup([]string{"--verbose", "--json"}) {
		t.Error("allowsDegradedStartup(flags only) should return true")
	}
}

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
	// storage.ErrStoreLocked may be wrapped with additional context.
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

func TestFormatInitError_GenericWrapped(t *testing.T) {
	inner := errors.New("inner cause")
	err := errors.Join(errors.New("outer wrapper"), inner)
	got := formatInitError(err)
	// Should return the top-level error message unchanged.
	if got == "" {
		t.Error("formatInitError returned empty for wrapped error")
	}
	if !strings.Contains(got, "outer wrapper") {
		t.Errorf("expected outer wrapper in output, got %q", got)
	}
}
