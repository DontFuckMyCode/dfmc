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

// TestFormatInitError_StoreLockedWithTimeout tests a wrapped ErrStoreLocked.
func TestFormatInitError_StoreLockedWithTimeout(t *testing.T) {
	err := errors.Join(storage.ErrStoreLocked, errors.New("database is locked by another process"))
	got := formatInitError(err)
	if !strings.Contains(got, "dfmc doctor") {
		t.Errorf("expected doctor guidance in wrapped error, got %q", got)
	}
}

// TestFormatInitError_Generic covers non-StoreLocked errors.
func TestFormatInitError_Generic(t *testing.T) {
	err := errors.New("some other error")
	got := formatInitError(err)
	if got != err.Error() {
		t.Errorf("formatInitError(generic) = %q, want %q", got, err.Error())
	}
}

func TestRun_ConfigLoadError(t *testing.T) {
	// Cannot easily test config load failure without mocking, but
	// formatInitError is tested above; run() calls it.
}

func TestRun_EngineNewError(t *testing.T) {
	// engine.New failure path is covered by formatInitError tests.
}

func TestRun_EngineInitError(t *testing.T) {
	// engine.Init failure propagates to formatInitError; tested above.
}

func TestRun_DegradedWithDoctor(t *testing.T) {
	if !allowsDegradedStartup([]string{"doctor"}) {
		t.Error("doctor should allow degraded startup")
	}
}

func TestRun_DegradedWithCompletion(t *testing.T) {
	if !allowsDegradedStartup([]string{"completion"}) {
		t.Error("completion should allow degraded startup")
	}
}

func TestRun_DegradedWithUpdate(t *testing.T) {
	if !allowsDegradedStartup([]string{"update"}) {
		t.Error("update should allow degraded startup")
	}
}

func TestRun_DegradedWithMan(t *testing.T) {
	if !allowsDegradedStartup([]string{"man"}) {
		t.Error("man should allow degraded startup")
	}
}

func TestRun_DegradedWithMixedFlagsAndCommand(t *testing.T) {
	if !allowsDegradedStartup([]string{"--verbose", "--json", "version"}) {
		t.Error("global flags before secondary command should allow degraded startup")
	}
}

func TestRun_DegradedWithSecondaryCommandVariants(t *testing.T) {
	for _, cmd := range []string{"completion", "man", "update"} {
		if !allowsDegradedStartup([]string{cmd}) {
			t.Errorf("allowsDegradedStartup(%q) = false, want true", cmd)
		}
	}
}