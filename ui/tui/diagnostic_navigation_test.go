package tui

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func TestActivatePlansPanelSeedsAndBuildsPlan(t *testing.T) {
	m := newPlansTestModel()
	m = m.activatePlansPanel("map provider routing and document fallback policy", false)
	if m.activeTab != 12 {
		t.Fatalf("expected Plans tab active, got %d", m.activeTab)
	}
	if m.plans.query != "map provider routing and document fallback policy" {
		t.Fatalf("unexpected plans query: %q", m.plans.query)
	}
	if m.plans.plan == nil || len(m.plans.plan.Subtasks) == 0 {
		t.Fatalf("expected generated plan, got %+v", m.plans.plan)
	}
}

func TestActivatePlansPanelKeepsExistingQueryWhenSeedBlank(t *testing.T) {
	m := newPlansTestModel()
	m.plans.query = "existing task"
	plan := planning.SplitTask(m.plans.query)
	m.plans.plan = &plan
	m = m.activatePlansPanel("   ", false)
	if m.plans.query != "existing task" {
		t.Fatalf("expected existing query to survive blank seed, got %q", m.plans.query)
	}
}

func TestActivateContextPanelSeedsAndRunsPreview(t *testing.T) {
	m := newContextTestModel()
	m = m.activateContextPanel("explain provider retry context budget", false)
	if m.activeTab != 13 {
		t.Fatalf("expected Context tab active, got %d", m.activeTab)
	}
	if m.contextPanel.query != "explain provider retry context budget" {
		t.Fatalf("unexpected context query: %q", m.contextPanel.query)
	}
	if !strings.Contains(m.contextPanel.err, "engine") {
		t.Fatalf("expected preview to run and report missing engine, got %q", m.contextPanel.err)
	}
}

func TestActivateDiagnosticTabSelectsProviders(t *testing.T) {
	m := newProvidersTestModel()
	m.activeTab = 0
	m = m.activateDiagnosticTab("Providers")
	if m.activeTab != 14 {
		t.Fatalf("expected Providers tab active, got %d", m.activeTab)
	}
}

func TestActivateProvidersPanelFocusesRequestedProvider(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m = m.activateProvidersPanel("offline", false)
	if m.activeTab != 14 {
		t.Fatalf("expected Providers tab active, got %d", m.activeTab)
	}
	if m.providers.scroll != 2 {
		t.Fatalf("expected offline row focus, got scroll=%d", m.providers.scroll)
	}
}

func TestActivateProvidersPanelRefreshesWhenRowsMissing(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.err = ""
	m = m.activateProvidersPanel("", false)
	if m.activeTab != 14 {
		t.Fatalf("expected Providers tab active, got %d", m.activeTab)
	}
	if m.providers.err == "" {
		t.Fatal("expected provider refresh to derive engine-not-ready error")
	}
}
