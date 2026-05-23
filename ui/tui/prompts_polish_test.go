package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPromptsHitsChip(t *testing.T) {
	if !strings.Contains(ansi.Strip(promptsHitsChip(0)), "0 hits") {
		t.Errorf("zero-hit chip lost label")
	}
	if !strings.Contains(ansi.Strip(promptsHitsChip(2)), "2 hits") {
		t.Errorf("nonzero chip should report count")
	}
}

func TestRenderPromptsView_SurfacesLiveSearchAndHitChip(t *testing.T) {
	m := newPromptsTestModel()
	m.prompts.templates = samplePromptTemplates()
	m.prompts.loaded = true
	m.prompts.searchActive = true
	m.prompts.query = "review"
	view := ansi.Strip(m.renderPromptsViewSized(140, 30))
	if !strings.Contains(view, "Search:") {
		t.Errorf("expected live search box, got:\n%s", view)
	}
	if !strings.Contains(view, "1 hits") {
		t.Errorf("expected 1-hit chip for 'review', got:\n%s", view)
	}
	if !strings.Contains(view, "enter commit") {
		t.Errorf("expected typing-mode hint, got:\n%s", view)
	}
}

func TestRenderPromptsView_ZeroHitChipOnMiss(t *testing.T) {
	m := newPromptsTestModel()
	m.prompts.templates = samplePromptTemplates()
	m.prompts.loaded = true
	m.prompts.query = "nothing-matches-this"
	view := ansi.Strip(m.renderPromptsViewSized(140, 30))
	if !strings.Contains(view, "0 hits") {
		t.Errorf("expected 0-hit chip on miss, got:\n%s", view)
	}
}

func TestRenderPromptsView_StaticQueryEchoWhenNotActive(t *testing.T) {
	m := newPromptsTestModel()
	m.prompts.templates = samplePromptTemplates()
	m.prompts.loaded = true
	m.prompts.query = "explain"
	view := ansi.Strip(m.renderPromptsViewSized(140, 30))
	if !strings.Contains(view, "query explain") {
		t.Errorf("expected committed query echo, got:\n%s", view)
	}
	if strings.Contains(view, "Search:") {
		t.Errorf("live search box should NOT show when searchActive=false, got:\n%s", view)
	}
}
