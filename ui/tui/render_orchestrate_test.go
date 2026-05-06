package tui

import (
	"strings"
	"testing"
	"time"
)

// TestOrchestrateView_RendersAllSections — the orchestrate panel
// must always show every section header so a user opening it for
// the first time sees the full hierarchy taxonomy even on an idle
// session. Sections gated to "skip when empty" would mean the user
// could miss a category that's about to populate.
func TestOrchestrateView_RendersAllSections(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderOrchestrateView(100))
	for _, want := range []string{
		"Orchestrate",
		"MAIN AGENT",
		"SUBAGENTS",
		"TODOS",
		"DRIVE RUN",
		"TOKENS",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("orchestrate view missing section %q. Got:\n%s", want, view)
		}
	}
}

// TestOrchestrateView_IdleSubagentsCopy — when no subagents are
// running the section should say so explicitly so the user knows
// "0" means "main agent works solo right now", not "feature broken".
func TestOrchestrateView_IdleSubagentsCopy(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderOrchestrateView(100))
	if !strings.Contains(view, "main agent works solo") {
		t.Errorf("idle subagents copy missing. Got:\n%s", view)
	}
}

// TestOrchestrateView_RendersSubagentsWithProviderModel — the user
// explicitly asked to see "which model is doing which job". Pin
// the rendering shape so a future refactor can't silently drop the
// provider/model column.
func TestOrchestrateView_RendersSubagentsWithProviderModel(t *testing.T) {
	m := newCoverageModel(t)
	m.telemetry.activeSubagentCount = 2
	m.telemetry.subagents = map[string]subagentRuntimeItem{
		"code|refactor auth": {
			Key:       "code|refactor auth",
			Role:      "code",
			Task:      "refactor auth/token.go",
			Provider:  "deepseek",
			Model:     "deepseek-chat",
			Status:    "subagent-running",
			Rounds:    5,
			StartedAt: time.Now().Add(-45 * time.Second),
		},
		"test|fixtures": {
			Key:       "test|fixtures",
			Role:      "test",
			Task:      "regenerate fixtures",
			Provider:  "openai",
			Model:     "o1-mini",
			Status:    "subagent-running",
			Rounds:    2,
			StartedAt: time.Now().Add(-18 * time.Second),
		},
	}
	view := stripANSI(m.renderOrchestrateView(120))
	for _, want := range []string{
		"deepseek/deepseek-chat",
		"openai/o1-mini",
		"refactor auth/token.go",
		"regenerate fixtures",
		"5 rounds",
		"2 rounds",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("subagent row missing %q. Got:\n%s", want, view)
		}
	}
}

// TestOrchestrateView_RendersMainAgentMomentum pins the per-turn
// momentum block (compacts, cache, edits, errors, running duration)
// so a user opening the panel mid-turn sees the agent's pressure
// at a glance.
func TestOrchestrateView_RendersMainAgentMomentum(t *testing.T) {
	m := newCoverageModel(t)
	m.agentLoop.active = true
	m.agentLoop.provider = "anthropic"
	m.agentLoop.model = "claude-opus-4-7"
	m.agentLoop.phase = "tool-call"
	m.agentLoop.step = 12
	m.agentLoop.maxToolStep = 30
	m.agentLoop.toolRounds = 8
	m.agentLoop.liveLoopTokens = 47000
	m.agentLoop.liveLoopBudgetCap = 250000
	m.agentLoop.compactsThisTurn = 2
	m.agentLoop.compactReclaimedTurn = 28000
	m.agentLoop.cacheHitsThisTurn = 4
	m.agentLoop.toolErrorsThisTurn = 1
	m.agentLoop.turnEditedFiles = []string{"a.go", "b.go", "c.go"}
	m.agentLoop.turnStartedAt = time.Now().Add(-90 * time.Second)

	view := stripANSI(m.renderOrchestrateView(120))
	for _, want := range []string{
		"anthropic / claude-opus-4-7",
		"calling tool", // humanized phase
		"12 / 30",
		"rounds 8",
		"47.0k / 250.0k",
		"compacts ×2",
		"-28.0k reclaimed",
		"cache ×4",
		"errs ×1",
		"edits ×3 files",
		"running 1m",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in main agent section. Got:\n%s", want, view)
		}
	}
}

// TestOrchestrateView_RendersTokensSection covers the token block
// with both context fill and session totals + cost.
func TestOrchestrateView_RendersTokensSection(t *testing.T) {
	m := newCoverageModel(t)
	m.telemetry.sessionInputTokens = 35000
	m.telemetry.sessionOutputTokens = 18000
	m.telemetry.sessionTotalTokens = 53000

	view := stripANSI(m.renderOrchestrateView(120))
	for _, want := range []string{
		"Session:",
		"in 35.0k",
		"out 18.0k",
		"total 53.0k",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in tokens section. Got:\n%s", want, view)
		}
	}
}

// TestOrchestrateView_TabActivationViaAltR pins the Alt+R keybinding
// so the orchestrate panel is reachable from any tab — including
// chat where alt+r isn't shadowed by a chat command.
func TestOrchestrateView_TabActivationViaAltR(t *testing.T) {
	m := newCoverageModel(t)
	idx := m.activityTabIndex("Orchestrate")
	if idx < 0 {
		t.Fatal("Orchestrate tab not registered in tabs slice")
	}
	got := m.activateDiagnosticTab("Orchestrate")
	if got.activeTab != idx {
		t.Errorf("activateDiagnosticTab(Orchestrate) didn't switch tabs: got %d want %d",
			got.activeTab, idx)
	}
}
