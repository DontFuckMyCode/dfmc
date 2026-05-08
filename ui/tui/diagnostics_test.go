package tui

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestNewDiagnosticPanelsStateAppliesDefaults(t *testing.T) {
	state := newDiagnosticPanelsState()
	if state == nil {
		t.Fatal("expected diagnostic state")
	}
	if state.memory.tier != string(types.MemoryWorking) {
		t.Fatalf("expected memory tier %q, got %q", string(types.MemoryWorking), state.memory.tier)
	}
	if state.codemap.view != codemapViewOverview {
		t.Fatalf("expected codemap view %q, got %q", codemapViewOverview, state.codemap.view)
	}
	if state.security.view != securityViewSecrets {
		t.Fatalf("expected security view %q, got %q", securityViewSecrets, state.security.view)
	}
}

func TestEnsureDiagnosticsRestoresDefaults(t *testing.T) {
	m := Model{}
	m.ensureDiagnostics()
	if m.diagnosticPanelsState == nil {
		t.Fatal("expected diagnostic state to be initialized")
	}
	if m.memory.tier != string(types.MemoryWorking) {
		t.Fatalf("expected memory tier %q, got %q", string(types.MemoryWorking), m.memory.tier)
	}
	if m.codemap.view != codemapViewOverview {
		t.Fatalf("expected codemap view %q, got %q", codemapViewOverview, m.codemap.view)
	}
	if m.security.view != securityViewSecrets {
		t.Fatalf("expected security view %q, got %q", securityViewSecrets, m.security.view)
	}

	m.diagnosticPanelsState = &diagnosticPanelsState{}
	m.ensureDiagnostics()
	if m.memory.tier != string(types.MemoryWorking) {
		t.Fatalf("expected memory tier %q after repair, got %q", string(types.MemoryWorking), m.memory.tier)
	}
	if m.codemap.view != codemapViewOverview {
		t.Fatalf("expected codemap view %q after repair, got %q", codemapViewOverview, m.codemap.view)
	}
	if m.security.view != securityViewSecrets {
		t.Fatalf("expected security view %q after repair, got %q", securityViewSecrets, m.security.view)
	}
}
