package context

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func TestTrimBundleToBudgetNil(t *testing.T) {
	result := trimBundleToBudget(nil, 1000)
	if result != nil {
		t.Error("expected nil result for nil bundle")
	}
}

func TestTrimBundleToBudgetZeroBudget(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "system", Text: "you are a helpful assistant", Cacheable: true},
		},
	}
	result := trimBundleToBudget(bundle, 0)
	if result != bundle {
		t.Error("expected unchanged bundle for zero budget")
	}
}

func TestTrimBundleToBudgetNegativeBudget(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "system", Text: "you are a helpful assistant", Cacheable: true},
		},
	}
	result := trimBundleToBudget(bundle, -100)
	if result != bundle {
		t.Error("expected unchanged bundle for negative budget")
	}
}

func TestTrimBundleToBudgetEmptySections(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{},
	}
	result := trimBundleToBudget(bundle, 1000)
	if len(result.Sections) != 0 {
		t.Error("expected empty sections")
	}
}

func TestTrimBundleToBudgetOnlyCacheable(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "system", Text: "policy text here", Cacheable: true},
			{Label: "more", Text: "more policy text", Cacheable: true},
		},
	}
	result := trimBundleToBudget(bundle, 1000)
	// With no dynamic sections, dynamicFloor will be 0, so all budget goes to stable
	// Both cacheable sections should be present since budget is large enough
	if len(result.Sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(result.Sections))
	}
}

func TestTrimBundleToBudgetCacheableTrimmed(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "system", Text: "very long policy text that should be trimmed", Cacheable: true},
		},
	}
	// tiny budget — the cacheable section should be trimmed
	result := trimBundleToBudget(bundle, 10)
	if len(result.Sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(result.Sections))
	}
	// trimmed text should be non-empty (unless budget is impossibly small)
	if result.Sections[0].Text == "" {
		t.Error("expected non-empty trimmed text")
	}
}

func TestTrimBundleToBudgetDynamicSection(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "system", Text: "policy", Cacheable: true},
			{Label: "query", Text: "user question here", Cacheable: false},
		},
	}
	result := trimBundleToBudget(bundle, 1000)
	// Both sections should be preserved with enough budget
	if len(result.Sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(result.Sections))
	}
}

func TestTrimBundleToBudgetDynamicTrimmed(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "query", Text: "very long user question that exceeds the dynamic allowance", Cacheable: false},
		},
	}
	// tiny budget — dynamic section should be trimmed
	result := trimBundleToBudget(bundle, 10)
	if len(result.Sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(result.Sections))
	}
	if result.Sections[0].Text == "" {
		t.Error("expected non-empty trimmed text for dynamic section")
	}
}

func TestTrimBundleToBudgetEmptyTextSection(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "empty", Text: "   ", Cacheable: true},
			{Label: "query", Text: "actual query", Cacheable: false},
		},
	}
	result := trimBundleToBudget(bundle, 1000)
	// Empty/whitespace sections should be skipped
	if len(result.Sections) != 1 {
		t.Errorf("expected 1 section (empty filtered out), got %d", len(result.Sections))
	}
	if result.Sections[0].Label != "query" {
		t.Errorf("expected query section, got %s", result.Sections[0].Label)
	}
}

func TestTrimBundleToBudgetDynamicFloor(t *testing.T) {
	// dynamicFloor = max(180, 25% of budget)
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "system", Text: "policy text", Cacheable: true},
			{Label: "query", Text: "user query", Cacheable: false},
		},
	}
	// budget=1000 → dynamicFloor=max(180,250)=250, stableBudget=750
	result := trimBundleToBudget(bundle, 1000)
	if len(result.Sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(result.Sections))
	}
	// query should survive since it's small
	if result.Sections[1].Text != "user query" {
		t.Errorf("expected 'user query', got %q", result.Sections[1].Text)
	}
}

func TestTrimBundleToBudgetStableAndDynamicMix(t *testing.T) {
	bundle := &promptlib.PromptBundle{
		Sections: []promptlib.PromptSection{
			{Label: "sys1", Text: "stable1", Cacheable: true},
			{Label: "dyn1", Text: "dynamic1", Cacheable: false},
			{Label: "sys2", Text: "stable2", Cacheable: true},
			{Label: "dyn2", Text: "dynamic2", Cacheable: false},
		},
	}
	result := trimBundleToBudget(bundle, 500)
	if len(result.Sections) != 4 {
		t.Errorf("expected 4 sections, got %d", len(result.Sections))
	}
}