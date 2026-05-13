package session

import (
	"encoding/json"
	"log"
)

// Coordinator is the role played by a parent agent when monitoring its children.
// The parent agent's tick() calls monitorChildren() on every cycle.
//
// When a child transitions to waiting_user_input or failed, the coordinator
// (parent) decides:
//   - Re-delegate to another sibling child (if available)
//   - Surface to the user (via Session's user input modal system)
//   - For Phase 2: log and mark the child as needing coordinator attention
//
// Phase 3 adds: actual re-delegation to siblings, user input modal injection.

// monitorChildren checks all children for status changes that need coordinator action.
// Called by the parent agent on each tick().
func (a *Agent) monitorChildren() {
	for _, childID := range a.children {
		child := a.session.GetAgent(childID)
		if child == nil {
			continue
		}

		status := child.Status()
		switch status {
		case StatusWaitingUserInput:
			a.handleWaitingChild(child)

		case StatusFailed:
			a.handleFailedChild(child)

		case StatusDone:
			// Result delivery is already handled via pendingResults channel.
			// Log for visibility.
			log.Printf("[coordinator agent-%d] child agent-%d done", a.id, childID)

		default:
			// Running, Parked, Idle, WaitingDelegation — no action needed.
		}
	}
}

// handleWaitingChild is called when a child is waiting for user input.
// The coordinator (parent) must decide: re-delegate or surface to user.
func (a *Agent) handleWaitingChild(child *Agent) {
	decision := a.EvaluateChildStatus(child)

	switch decision {
	case CoordRedelegate:
		// Try to find another sibling to re-delegate the task.
		// For Phase 2: just log the intent.
		log.Printf("[coordinator agent-%d] child agent-%d waiting; decision=redelegate (siblings=%d)",
			a.id, child.id, len(a.children))

	case CoordSurface:
		// Surface to user via attention event.
		// The TUI will pick this up in Phase 3.
		log.Printf("[coordinator agent-%d] child agent-%d waiting; decision=surface_to_user",
			a.id, child.id)

		// Publish a special event that the TUI can listen to.
		if a.session != nil {
			sa := a.session.Attention()
			if sa != nil {
				payload, _ := json.Marshal(map[string]any{
					"child_id":   child.id,
					"child_name": child.name,
					"message":    "child agent needs your input",
				})
				sa.Publish(AttentionEvent{
					From:    child.id,
					Type:    AttentionQuestion,
					Payload: payload,
				})
			}
		}

	case CoordAnswer:
		// Coordinator answers on behalf of the child.
		log.Printf("[coordinator agent-%d] child agent-%d waiting; decision=answer_self",
			a.id, child.id)

	case CoordIgnore:
		// No action needed.
	}
}

// handleFailedChild logs the child's failure and potentially re-delegates.
func (a *Agent) handleFailedChild(child *Agent) {
	log.Printf("[coordinator agent-%d] child agent-%d failed", a.id, child.id)

	// Phase 3: try to re-delegate to another sibling.
	// For Phase 2: just log.
}

// CanDelegateTo checks whether this agent can delegate to a child.
func (a *Agent) CanDelegateTo(childID AgentID) bool {
	if a.session == nil {
		return false
	}
	child := a.session.GetAgent(childID)
	if child == nil {
		return false
	}
	return child.parent == a.id
}

// DelegateTo is a convenience method for the coordinator to delegate to a child.
func (a *Agent) DelegateTo(childID AgentID, task string, autonomy AutonomyLevel, await bool) error {
	if !a.CanDelegateTo(childID) {
		return &InvalidDelegationError{From: a.id, To: childID, Reason: "not a direct child"}
	}
	return a.session.Delegate(a.id, childID, task, "", autonomy, await)
}

// DelegateToWithPrompt is like DelegateTo but allows a task-specific system prompt
// to be injected into the child's conversation before the task message.
func (a *Agent) DelegateToWithPrompt(childID AgentID, task string, systemPrompt string, autonomy AutonomyLevel, await bool) error {
	if !a.CanDelegateTo(childID) {
		return &InvalidDelegationError{From: a.id, To: childID, Reason: "not a direct child"}
	}
	return a.session.Delegate(a.id, childID, task, systemPrompt, autonomy, await)
}

// InvalidDelegationError represents a delegation validation failure.
type InvalidDelegationError struct {
	From, To AgentID
	Reason   string
}

func (e *InvalidDelegationError) Error() string {
	return "invalid delegation from " + itoa(int(e.From)) + " to " + itoa(int(e.To)) + ": " + e.Reason
}

// itoa converts int to string without importing strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

// CoordinatorDecision is the result of a coordinator evaluating a child's status.
type CoordinatorDecision int

const (
	CoordRedelegate CoordinatorDecision = iota // re-delegate to another child
	CoordAnswer                                // coordinator answers itself
	CoordSurface                               // surface to user (waiting_user_input)
	CoordIgnore                                // no action needed
)

// EvaluateChildStatus decides what a coordinator should do when a child is waiting.
func (a *Agent) EvaluateChildStatus(child *Agent) CoordinatorDecision {
	status := child.Status()
	switch status {
	case StatusWaitingUserInput:
		// If the child has its own children, try to re-delegate first.
		if len(child.children) > 0 {
			return CoordRedelegate
		}
		// No grandchildren to delegate to. Surface to user.
		return CoordSurface
	case StatusFailed:
		return CoordRedelegate
	default:
		return CoordIgnore
	}
}

// ParseChildQuestion decodes an AttentionQuestion payload from a child agent.
func ParseChildQuestion(payload json.RawMessage) (childID AgentID, task string, usedSteps, maxSteps int, err error) {
	var p struct {
		Agent      AgentID `json:"agent"`
		Task       string  `json:"task"`
		UsedSteps  int     `json:"used_steps"`
		MaxSteps   int     `json:"max_steps"`
		UsedTokens int     `json:"used_tokens"`
		MaxTokens  int     `json:"max_tokens"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return 0, "", 0, 0, err
	}
	return p.Agent, p.Task, p.UsedSteps, p.MaxSteps, nil
}
