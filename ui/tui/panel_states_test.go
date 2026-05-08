package tui

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestApplyDefaults_SetsCorrectInitialValues(t *testing.T) {
	state := newDiagnosticPanelsState()

	// memory panel defaults — Phase H item 2: opens on Working tier
	// (recent scratchpad), not the merged All view.
	if state.memory.tier != string(types.MemoryWorking) {
		t.Errorf("memory.tier: expected %q, got %q", types.MemoryWorking, state.memory.tier)
	}

	// codemap panel defaults
	if state.codemap.view != codemapViewOverview {
		t.Errorf("codemap.view: expected %q, got %q", codemapViewOverview, state.codemap.view)
	}

	// security panel defaults
	if state.security.view != securityViewSecrets {
		t.Errorf("security.view: expected %q, got %q", securityViewSecrets, state.security.view)
	}
}

func TestApplyDefaults_DoesNotOverwriteNonEmptyValues(t *testing.T) {
	state := &diagnosticPanelsState{}
	state.memory.tier = "episodic"
	state.codemap.view = "hotspots"
	state.security.view = "vulnerabilities"
	state.applyDefaults()

	if state.memory.tier != "episodic" {
		t.Errorf("memory.tier should not be overwritten: got %q", state.memory.tier)
	}
	if state.codemap.view != "hotspots" {
		t.Errorf("codemap.view should not be overwritten: got %q", state.codemap.view)
	}
	if state.security.view != "vulnerabilities" {
		t.Errorf("security.view should not be overwritten: got %q", state.security.view)
	}
}

func TestApplyDefaults_NilReceiver(t *testing.T) {
	var state *diagnosticPanelsState
	state.applyDefaults() // should not panic
}

func TestNewDiagnosticPanelsState_ReturnsNonNil(t *testing.T) {
	state := newDiagnosticPanelsState()
	if state == nil {
		t.Fatal("newDiagnosticPanelsState returned nil")
	}
	if state.memory.tier == "" {
		t.Error("memory.tier should be initialized to a non-empty value")
	}
}
