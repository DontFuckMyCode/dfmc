package tui

import (
	"strings"
	"testing"
)

// TestResolveDriveRunID covers the prefix-matching contract that
// makes /drive stop and /drive resume usable from a copy/pasted
// short ID. Exact match wins; unique prefix matches; ambiguous and
// unmatched prefixes return descriptive errors so the user can
// recover without re-running /drive list.
func TestResolveDriveRunID(t *testing.T) {
	candidates := []string{
		"drv-1234abcd-5678-90ef",
		"drv-1234ffff-aaaa-bbbb",
		"drv-9876xxxx-yyyy-zzzz",
	}

	t.Run("exact match", func(t *testing.T) {
		got, ok, _ := resolveDriveRunID("drv-1234abcd-5678-90ef", candidates)
		if !ok || got != "drv-1234abcd-5678-90ef" {
			t.Errorf("exact match: got (%q, %v)", got, ok)
		}
	})
	t.Run("unique prefix", func(t *testing.T) {
		got, ok, _ := resolveDriveRunID("drv-9876", candidates)
		if !ok || got != "drv-9876xxxx-yyyy-zzzz" {
			t.Errorf("unique prefix: got (%q, %v)", got, ok)
		}
	})
	t.Run("case insensitive prefix", func(t *testing.T) {
		got, ok, _ := resolveDriveRunID("DRV-9876", candidates)
		if !ok || got != "drv-9876xxxx-yyyy-zzzz" {
			t.Errorf("case insensitive: got (%q, %v)", got, ok)
		}
	})
	t.Run("ambiguous prefix", func(t *testing.T) {
		_, ok, msg := resolveDriveRunID("drv-1234", candidates)
		if ok {
			t.Errorf("ambiguous: should not resolve, got %q", msg)
		}
		if !strings.Contains(msg, "ambiguous") {
			t.Errorf("ambiguous: msg should explain, got %q", msg)
		}
	})
	t.Run("no match", func(t *testing.T) {
		_, ok, msg := resolveDriveRunID("drv-zzz", candidates)
		if ok {
			t.Errorf("no match: should not resolve, got %q", msg)
		}
		if !strings.Contains(msg, "no run matches") {
			t.Errorf("no match: msg should explain, got %q", msg)
		}
	})
	t.Run("empty input", func(t *testing.T) {
		_, ok, msg := resolveDriveRunID("", candidates)
		if ok {
			t.Errorf("empty: should not resolve")
		}
		if msg == "" {
			t.Errorf("empty: should explain")
		}
	})
	t.Run("empty candidates", func(t *testing.T) {
		_, ok, _ := resolveDriveRunID("drv-1234", nil)
		if ok {
			t.Errorf("empty candidates: should not resolve")
		}
	})
}
