package engine

// SetSupervisor registers the active supervisor for budget accounting.
// Sub-agent budget halving uses the supervisor pool when non-nil.
// Called by the supervisor start path; cleared when the supervisor finishes.
func (e *Engine) SetSupervisor(supervisor interface {
	AllocTokens(int) int
	RestoreTokens(int)
}) {
	e.activeSupervisor = supervisor
}

// ClearSupervisor removes the active supervisor reference after a run ends.
func (e *Engine) ClearSupervisor() {
	e.activeSupervisor = nil
}

// ActiveSupervisorBudget returns the remaining token budget if a supervisor
// is active and has a token cap, or -1 for unlimited.
func (e *Engine) ActiveSupervisorBudget() int {
	type budgeter interface {
		Remaining() int
	}
	if e.activeSupervisor == nil {
		return -1
	}
	if b, ok := e.activeSupervisor.(budgeter); ok {
		return b.Remaining()
	}
	return -1
}

// IsSupervising returns true when a supervisor run is currently active.
func (e *Engine) IsSupervising() bool {
	return e.activeSupervisor != nil
}
