// Package supervisor provides the task-supervision coordinator for
// parallel sub-agent execution with budget halving and dependency resolution.
package supervisor

// TokenAllocator is the budget-pool capability the engine needs from an
// active supervisor: alloc/restore tokens for sub-agent budget halving.
// Optional capabilities (Status, Remaining) are sniffed via type
// assertion at the call site so a supervisor that doesn't expose them
// still satisfies the required core.
type TokenAllocator interface {
	AllocTokens(int) int
	RestoreTokens(int)
}

// Statuser exposes the current supervisor status for observability.
type Statuser interface {
	Status() SupervisorStatus
}

// Budgeter reports remaining token budget for a worker.
type Budgeter interface {
	Remaining() int
}

