package session

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// depthChain returns the ancestor chain from root to id, checking depth cap.
// It accepts a getAgentFunc so it can be called without holding Session's mutex.
func depthChain(getAgent func(id AgentID) *Agent, id AgentID) ([]AgentID, error) {
	const maxDepth = 5

	var chain []AgentID
	current := id
	for len(chain) < maxDepth {
		if current == 0 {
			break
		}
		agent := getAgent(current)
		if agent == nil {
			return nil, fmt.Errorf("agent %d not found", current)
		}
		chain = append(chain, current)
		if agent.parent == 0 {
			break
		}
		current = agent.parent
	}

	if len(chain) >= maxDepth {
		return nil, fmt.Errorf("delegation depth cap exceeded (cap=%d)", maxDepth)
	}

	// Reverse to root→leaf order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// ValidateDelegation checks parent→child relationship and depth cap.
func ValidateDelegation(sp sessionProvider, from, to AgentID) error {
	if from == 0 {
		return fmt.Errorf("delegation: source agent 0 is invalid")
	}
	if to == 0 {
		return fmt.Errorf("delegation: target agent 0 is invalid")
	}
	if from == to {
		return fmt.Errorf("delegation: cannot delegate to self")
	}

	fromAgent := sp.GetAgent(from)
	toAgent := sp.GetAgent(to)
	if fromAgent == nil {
		return fmt.Errorf("delegation: source agent %d not found", from)
	}
	if toAgent == nil {
		return fmt.Errorf("delegation: target agent %d not found", to)
	}
	if toAgent.parent != from {
		return fmt.Errorf("delegation: agent %d is not a child of agent %d", to, from)
	}

	_, err := depthChain(sp.GetAgent, to)
	return err
}

// setupBlockingDelegation creates the result channel and timeout goroutine for
// a blocking delegation. Called by Session.Delegate.
func setupBlockingDelegation(parent *Agent, taskID uuid.UUID) {
	ch := make(chan DelegationResult, 1)
	parent.pendingMu.Lock()
	parent.pendingResults[taskID] = ch
	parent.pendingMu.Unlock()

	// Timeout: clean up pendingResults after 5 minutes.
	go func() {
		<-time.After(5 * time.Minute)
		parent.pendingMu.Lock()
		delete(parent.pendingResults, taskID)
		parent.pendingMu.Unlock()
	}()
}

func trunc(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
