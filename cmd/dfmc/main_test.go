package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/storage"
)

func TestAllowsDegradedStartup(t *testing.T) {
	if !allowsDegradedStartup([]string{"doctor"}) {
		t.Fatal("expected doctor to allow degraded startup")
	}
	if !allowsDegradedStartup([]string{"--json", "version"}) {
		t.Fatal("expected version to allow degraded startup even after global flags")
	}
	if allowsDegradedStartup([]string{"tui"}) {
		t.Fatal("expected tui to require full init")
	}
}

func TestFormatInitErrorForStoreLock(t *testing.T) {
	err := errors.Join(storage.ErrStoreLocked, errors.New("timeout"))
	got := formatInitError(err)
	if !strings.Contains(got, "dfmc doctor") {
		t.Fatalf("expected doctor guidance in init error, got %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "locked") {
		t.Fatalf("expected lock wording in init error, got %q", got)
	}
}
