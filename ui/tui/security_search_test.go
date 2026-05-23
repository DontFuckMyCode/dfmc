package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSecurityHitsChip(t *testing.T) {
	if !strings.Contains(ansi.Strip(securityHitsChip(0)), "0 hits") {
		t.Errorf("zero-hit chip lost label")
	}
	if !strings.Contains(ansi.Strip(securityHitsChip(4)), "4 hits") {
		t.Errorf("nonzero chip should report count")
	}
}

func TestRenderSecurityView_SurfacesLiveSearchAndHitChip(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	m.security.loaded = true
	m.security.view = securityViewSecrets
	m.security.searchActive = true
	m.security.query = "aws"
	view := ansi.Strip(m.renderSecurityViewSized(140, 30))
	if !strings.Contains(view, "Search:") {
		t.Errorf("expected live search box, got:\n%s", view)
	}
	if !strings.Contains(view, "1 hits") {
		t.Errorf("expected 1-hit chip for 'aws' substring, got:\n%s", view)
	}
}

func TestRenderSecurityView_StaticFilterLineWhenNotActive(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	m.security.loaded = true
	m.security.view = securityViewSecrets
	m.security.query = "api"
	view := ansi.Strip(m.renderSecurityViewSized(140, 30))
	if !strings.Contains(view, "filter") {
		t.Errorf("expected static filter line, got:\n%s", view)
	}
	if !strings.Contains(view, "press c to clear") {
		t.Errorf("expected clear hint, got:\n%s", view)
	}
}

func TestRenderSecurityView_ZeroHitChipOnMiss(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	m.security.loaded = true
	m.security.view = securityViewSecrets
	m.security.searchActive = true
	m.security.query = "definitely-nothing-matches"
	view := ansi.Strip(m.renderSecurityViewSized(140, 30))
	if !strings.Contains(view, "0 hits") {
		t.Errorf("expected 0-hit chip on miss, got:\n%s", view)
	}
}

func TestRenderSecurityView_HintLineUsesRealKeys(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	m.security.loaded = true
	view := ansi.Strip(m.renderSecurityViewSized(140, 30))
	// The stale "ctrl+f search" copy should be gone; the real keys
	// (slash, c, r, v, g/G) should appear.
	if strings.Contains(view, "ctrl+f search") {
		t.Errorf("stale ctrl+f hint should be removed, got:\n%s", view)
	}
	for _, want := range []string{"/ search", "c clear", "r rescan", "v toggle view", "g/G top/bottom"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected hint %q in affordance line, got:\n%s", want, view)
		}
	}
}
