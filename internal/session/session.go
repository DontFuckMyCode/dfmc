package session

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Session manages a multi-agent session. It owns all agents, handles spawning,
// delegation, and the shared attention bus. A session is created by the TUI or
// CLI entry point and lives for the duration of that interactive session.
//
// Lifecycle:
//   - New() → configure → Start() → agents run → user interacts via TUI/CLI → Close()
//
// The Session does NOT persist across process restarts (Phase 1).
//
// Bridge to existing code (Phase 4):
//   - Session holds *Engine → each agent gets EngineProvider wrapper
//   - Session.Attention() → wired to engine.EventBus for cross-cutting events
//   - Agent.conversation → wired to internal/conversation/manager.go
type Session struct {
	mu sync.RWMutex // guards agents map

	// The shared engine. Set via AttachEngine in Phase 4.
	engine EngineProvider

	agents map[AgentID]*Agent

	// Tree roots. Agent 1 is always the root (user-facing).
	// Additional roots can be added for multi-root sessions (future).
	root AgentID

	// Shared attention bus for short-lived awareness events.
	attention *SharedAttention

	// Currently active (visible) agent in the TUI.
	activeAgent AgentID

	// Next available agent ID counter.
	nextID AgentID

	// Configuration
	depthCap int // max delegation depth, default 5
}

// New creates a new multi-agent session. It starts with a single root agent (Agent 1).
func New() *Session {
	s := &Session{
		agents:   make(map[AgentID]*Agent),
		attention: NewSharedAttention(),
		nextID:   1,
		depthCap: 5,
	}

	// Create the root agent (Agent 1) — user-facing.
	root := newAgent(s, RootAgentID, "agent-1", 0)
	root.model = ModelConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"}
	root.autonomy = AutonomyFull
	s.agents[RootAgentID] = root
	s.root = RootAgentID
	s.activeAgent = RootAgentID
	s.nextID = 2 // next agent will be Agent 2

	// Set up per-session status hook so the TUI can be notified of agent
	// status changes (e.g. waiting_user_input). The TUI calls SetStatusHook
	// before creating any session. The package-level statusHook is shared
	// across all agents, so no per-agent wiring is needed here.

	return s
}

// AttachEngine wires the shared Engine to this session. Called during Phase 4 setup.
// Until then, agents run in stub mode (no real tool execution or LLM calls).
func (s *Session) AttachEngine(eng EngineProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine = eng

	// Wire all existing and future agents to the engine.
	for _, a := range s.agents {
		a.SetEngine(eng)
	}
}

// Start launches all agents. For now, only the root agent is started.
// Child agents are started when they receive their first delegation task.
func (s *Session) Start() error {
	s.mu.Lock()
	root := s.agents[s.root]
	s.mu.Unlock()

	if root == nil {
		return fmt.Errorf("session: no root agent")
	}

	// Root agent runs in a goroutine; it will be the one processing user input.
	go root.Run(root.runCtx, "")

	return nil
}

// Close terminates all agents and cleans up the session.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range s.agents {
		a.runCancel()
	}

	s.attention.Sweep()
	return nil
}

// GetAgent returns an agent by ID, or nil if not found.
func (s *Session) GetAgent(id AgentID) *Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[id]
}

// GetAgentByParent returns the first agent whose parent is the given ID.
// Used by the delegation system.
func (s *Session) GetAgentByParent(parentID AgentID) *Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.agents {
		if a.parent == parentID {
			return a
		}
	}
	return nil
}

// ActiveAgent returns the currently active (visible) agent.
func (s *Session) ActiveAgent() *Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[s.activeAgent]
}

// SetActiveAgent switches the visible TUI to the given agent.
func (s *Session) SetActiveAgent(id AgentID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[id]; !ok {
		return fmt.Errorf("session: agent %d not found", id)
	}
	s.activeAgent = id
	return nil
}

// Attention returns the session's shared attention bus.
func (s *Session) Attention() *SharedAttention { return s.attention }

// writeEvent is part of sessionProvider interface (agent.go).
// Session always writes to stdout for now; real file logging follows in a later change.
func (s *Session) writeEvent(event string, fields map[string]any) {
	// Stub — satisfies sessionProvider without changing existing behavior.
	_ = event
	_ = fields
}

// SpawnAgent creates a new agent as a child of the given parent.
// The new agent does not start running until it receives a task in its inbox.
// Returns the new agent's ID.
func (s *Session) SpawnAgent(parentID AgentID, task string, autonomy AutonomyLevel) (*Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parent, ok := s.agents[parentID]
	if !ok {
		return nil, fmt.Errorf("session: parent agent %d not found", parentID)
	}

	// Check depth cap. We hold the write lock, so directly access s.agents.
	chain, err := depthChain(func(id AgentID) *Agent { return s.agents[id] }, parentID)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	if len(chain) >= s.depthCap {
		return nil, fmt.Errorf("spawn: depth cap exceeded (cap=%d)", s.depthCap)
	}

	id := s.nextID
	s.nextID++

	name := fmt.Sprintf("agent-%d", id)
	agent := newAgent(s, id, name, parentID)
	agent.autonomy = autonomy

	// Clone parent's model config.
	agent.model = parent.model

	// Wire engine if already attached.
	if s.engine != nil {
		agent.SetEngine(s.engine)
	}

	// If task is non-empty, this spawn also includes an initial task.
	// We put it in the inbox before the goroutine starts.
	if task != "" {
		dt := DelegationTask{
			ID:        uuid.New(),
			From:      parentID,
			Task:      task,
			Autonomy:  autonomy,
			Status:    TaskStatusPending,
			CreatedAt: time.Now(),
		}
		select {
		case agent.inbox <- dt:
		default:
			return nil, fmt.Errorf("spawn: new agent inbox full")
		}
	}

	s.agents[id] = agent
	parent.children = append(parent.children, id)

	// Subscribe the parent to this child's attention events.
	if s.attention != nil {
		s.attention.Subscribe(parentID, id)
	}

	// Start the agent goroutine.
	go agent.Run(agent.runCtx, "")

	return agent, nil
}

// AgentTree returns a tree view of all agents for UI display.
func (s *Session) AgentTree() []AgentTreeNode {
	s.mu.Lock()
	defer s.mu.Unlock()

	var nodes []AgentTreeNode
	var build func(agentID AgentID, depth int)
	build = func(agentID AgentID, depth int) {
		agent, ok := s.agents[agentID]
		if !ok {
			return
		}
		nodes = append(nodes, AgentTreeNode{
			ID:       agent.id,
			Name:     agent.name,
			Status:   agent.status,
			Parent:   agent.parent,
			Children: append([]AgentID{}, agent.children...),
			Depth:    depth,
		})
		for _, childID := range agent.children {
			build(childID, depth+1)
		}
	}

	// Start from roots (agents with no parent, i.e., parent=0).
	// In Phase 1 there's only one root.
	build(s.root, 0)
	return nodes
}

// AgentCount returns the total number of agents.
func (s *Session) AgentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.agents)
}

// Delegate queues a task from a parent agent to one of its children.
// Validates parent→child relationship and respects depth cap.
//
// If await=true, the calling agent blocks on the result (5 min timeout).
// If await=false, fire-and-forget.
//
// systemPrompt is optional and gets injected into the child's conversation
// as a system message before the task. Use it to give the child a task-specific
// identity or context.
//
// This method is safe to call concurrently.
func (s *Session) Delegate(from, to AgentID, task string, systemPrompt string, autonomy AutonomyLevel, await bool) error {
	if err := ValidateDelegation(s, from, to); err != nil {
		return err
	}

	s.mu.Lock()
	toAgent := s.agents[to]
	fromAgent := s.agents[from]
	s.mu.Unlock()

	if toAgent == nil {
		return fmt.Errorf("delegate: target agent %d not found", to)
	}

	delegTask := DelegationTask{
		ID:           uuid.New(),
		From:         from,
		Task:         task,
		SystemPrompt: systemPrompt,
		Autonomy:     autonomy,
		AwaitResult:  await,
		Status:       TaskStatusPending,
		CreatedAt:    time.Now(),
	}

	// For blocking delegation, set up result channel + timeout.
	if await {
		setupBlockingDelegation(fromAgent, delegTask.ID)
	}

	// Queue to child's inbox (non-blocking).
	select {
	case toAgent.inbox <- delegTask:
		// Queued successfully.
	default:
		// Rollback the pending result if inbox is full.
		if await {
			fromAgent.pendingMu.Lock()
			delete(fromAgent.pendingResults, delegTask.ID)
			fromAgent.pendingMu.Unlock()
		}
		return fmt.Errorf("delegate: agent %d inbox full", to)
	}

	// Publish attention event.
	if s.attention != nil {
		payload, _ := json.Marshal(map[string]any{
			"from":         from,
			"to":           to,
			"task_id":      delegTask.ID,
			"await":        await,
			"task_snippet": trunc(task, 100),
		})
		s.attention.Publish(AttentionEvent{
			From:    from,
			Type:    AttentionDelegationSent,
			Payload: payload,
		})
	}

	return nil
}

// KillAgent terminates an agent and its entire subtree.
// The root agent cannot be killed.
func (s *Session) KillAgent(id AgentID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == 0 || id == s.root {
		return fmt.Errorf("session: cannot kill root agent")
	}

	agent, ok := s.agents[id]
	if !ok {
		return fmt.Errorf("session: agent %d not found", id)
	}

	// Recursively kill children first.
	for _, childID := range agent.children {
		s.killLocked(childID)
	}

	// Cancel the agent's goroutine.
	agent.runCancel()

	// Remove from parent's children list.
	if agent.parent != 0 {
		if parent := s.agents[agent.parent]; parent != nil {
			for i, c := range parent.children {
				if c == id {
					parent.children = append(parent.children[:i], parent.children[i+1:]...)
					break
				}
			}
		}
	}

	delete(s.agents, id)
	return nil
}

func (s *Session) killLocked(id AgentID) {
	agent, ok := s.agents[id]
	if !ok {
		return
	}
	for _, childID := range agent.children {
		s.killLocked(childID)
	}
	agent.runCancel()
	delete(s.agents, id)
}

// SendToAgent injects a user message directly into an agent's conversation.
// Used by the TUI when the user types on an agent's screen.
func (s *Session) SendToAgent(id AgentID, message string) error {
	s.mu.Lock()
	agent, ok := s.agents[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session: agent %d not found", id)
	}

	// If the agent is waiting for user input, inject the message and
	// wake it up with a synthetic resume task. The agent's outer loop
	// will pick up the resume task, call handleTask, and the conversation
	// already has the injected user message.
	if agent.Status() == StatusWaitingUserInput {
		agent.conversation.append(Message{Role: "user", Content: message})
		// Inject a resume task to wake the agent's outer loop.
		// From=id (same agent) makes it a non-root task so the agent
		// stays alive after handling it (doesn't exit the loop).
		resumeTask := DelegationTask{
			ID:        uuid.New(),
			From:      id, // non-zero → delegation task, stays in loop
			Task:      "continue",
			Autonomy:  agent.autonomy,
			Status:    TaskStatusRunning,
			CreatedAt: time.Now(),
		}
		select {
		case agent.inbox <- resumeTask:
			agent.setStatus(StatusRunning)
		default:
			return fmt.Errorf("session: agent %d inbox full during resume", id)
		}
		return nil
	}

	return fmt.Errorf("session: agent %d is not waiting for input (status=%s)", id, agent.Status())
}
