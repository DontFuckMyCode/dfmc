package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// sessionProvider is the subset of *Session that Agent needs.
// Defined here to avoid circular imports. Full Session type lives in session.go.
type sessionProvider interface {
	GetAgent(id AgentID) *Agent
	Attention() *SharedAttention
	Delegate(from, to AgentID, task string, systemPrompt string, autonomy AutonomyLevel, await bool) error
	writeEvent(event string, fields map[string]any)
}

// Agent is an isolated worker agent within a Session.
// Each agent has its own conversation history, context manager, model config,
// budget, and delegation inbox. Agents run concurrently.
//
// Bridge to existing code (Phase 4):
//   - Tool execution → EngineProvider.ExecuteTool
//   - LLM completion → EngineProvider.Complete
type Agent struct {
	id   AgentID
	name string

	// Tree structure
	parent   AgentID // coordinator (nil=0 for root)
	children []AgentID

	// Per-agent isolated state
	conversation *conversationRef
	context      ContextManagerHandle

	// Model config for this agent
	model    ModelConfig
	autonomy AutonomyLevel

	// Per-agent budget
	maxToolSteps  int
	maxToolTokens int
	usedSteps     int
	usedTokens    int

	// Loop state
	status         AgentStatus
	statusMu       sync.RWMutex
	inbox          chan DelegationTask // incoming tasks from parent
	pendingResults map[uuid.UUID]chan DelegationResult
	pendingMu      sync.Mutex // guards pendingResults

	// Lifecycle
	runCtx    context.Context
	runCancel context.CancelFunc

	// Wired in Phase 4
	engine  EngineProvider  // nil until wired
	session sessionProvider // nil until Session sets it
}

// conversationRef is a lightweight in-memory conversation history.
// Phase 4 will replace this with a wire to internal/conversation/manager.go.
type conversationRef struct {
	mu       sync.RWMutex
	messages []Message
}

func (cr *conversationRef) append(msg Message) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.messages = append(cr.messages, msg)
}

func (cr *conversationRef) messagesCopy() []Message {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	out := make([]Message, len(cr.messages))
	copy(out, cr.messages)
	return out
}

// ModelConfig holds the current model/provider for an agent.
type ModelConfig struct {
	Provider string `json:"provider"` // "anthropic", "openai", etc.
	Model    string `json:"model"`    // "claude-sonnet-4-6", "gpt-4o", etc.
}

// newAgent creates an agent but does not start it. Use Session.SpawnAgent instead.
func newAgent(session sessionProvider, id AgentID, name string, parent AgentID) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
		id:             id,
		name:           name,
		parent:         parent,
		conversation:   &conversationRef{},
		maxToolSteps:   60, // default; overridden per agent
		maxToolTokens:  250000,
		autonomy:       AutonomyFull,
		status:         StatusIdle,
		inbox:          make(chan DelegationTask, 10),
		pendingResults: make(map[uuid.UUID]chan DelegationResult),
		runCtx:         ctx,
		runCancel:      cancel,
		session:        session,
	}
}

// ID returns the agent's unique ID.
func (a *Agent) ID() AgentID { return a.id }

// Name returns the agent's display name.
func (a *Agent) Name() string { return a.name }

// SetName changes the agent's display name.
func (a *Agent) SetName(name string) { a.name = name }

// Status returns the agent's current status.
func (a *Agent) Status() AgentStatus {
	a.statusMu.RLock()
	defer a.statusMu.RUnlock()
	return a.status
}

// StatusEvent is a status transition event emitted on the global channel.
type StatusEvent struct {
	ID  AgentID
	Old AgentStatus
	New AgentStatus
	// Task is set when New == StatusWaitingUserInput — the task description
	// the agent was working on when it hit the wait condition.
	Task string
}

// StatusHookChannel is a global event channel for status transitions.
var StatusHookChannel chan StatusEvent

// PublishStatusEvent sends a status transition to the global channel
// if one is registered. task is set when New==StatusWaitingUserInput.
func PublishStatusEvent(id AgentID, old, new AgentStatus, task string) {
	if StatusHookChannel != nil {
		select {
		case StatusHookChannel <- StatusEvent{id, old, new, task}:
		default:
		}
	}
}

func (a *Agent) setStatus(s AgentStatus) {
	a.statusMu.Lock()
	old := a.status
	a.status = s
	a.statusMu.Unlock()
	if old != s {
		PublishStatusEvent(a.id, old, s, "")
		if a.session != nil {
			a.session.writeEvent("agent:status", map[string]any{
				"agent_id": a.id,
				"from":     old.String(),
				"to":       s.String(),
			})
		}
	}
}

// setStatusAndTask is like setStatus but also carries the current task
// description so the TUI can render it in the waiting-input modal.
func (a *Agent) setStatusAndTask(s AgentStatus, task string) {
	a.statusMu.Lock()
	old := a.status
	a.status = s
	a.statusMu.Unlock()
	if old != s {
		PublishStatusEvent(a.id, old, s, task)
	}
}

// ParentID returns the parent agent's ID (0 for root).
func (a *Agent) ParentID() AgentID { return a.parent }

// SetEngine wires the engine provider. Called by Session after agent construction.
func (a *Agent) SetEngine(eng EngineProvider) { a.engine = eng }

// SetContext sets the per-agent context manager. Called by Session after construction.
func (a *Agent) SetContext(ctx ContextManagerHandle) { a.context = ctx }

// SetMaxSteps sets the tool step budget for this agent.
func (a *Agent) SetMaxSteps(n int) { a.maxToolSteps = n }

// SetMaxTokens sets the token budget for this agent.
func (a *Agent) SetMaxTokens(n int) { a.maxToolTokens = n }

// Run starts the agent's main loop. It blocks until the agent terminates.
// For the root agent, initialTask is the user's first message.
// For spawned agents, the first task comes from the inbox.
func (a *Agent) Run(ctx context.Context, initialTask string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent-%d] panic in Run: %v", a.id, r)
			a.setStatus(StatusFailed)
		}
	}()

	a.setStatus(StatusRunning)

	// If initialTask is non-empty (root task from user), handle it first.
	if initialTask != "" {
		task := DelegationTask{
			ID:        uuid.New(),
			From:      0, // root task
			Task:      initialTask,
			Autonomy:  a.autonomy,
			Status:    TaskStatusRunning,
			CreatedAt: time.Now(),
			StartedAt: time.Now(),
		}
		a.handleTask(task)
		// Task completed (or agent parked). Exit if done.
		if a.Status() == StatusDone || a.Status() == StatusParked {
			return
		}
	}

	// Main event loop.
	for {
		select {
		case <-ctx.Done():
			a.setStatus(StatusDone)
			return

		case task := <-a.inbox:
			a.setStatus(StatusRunning)
			a.handleTask(task)
			// For delegation tasks (From != 0), transition to waiting for next task.
			// For root tasks (From == 0), exit the loop.
			if task.From == 0 {
				// Root task: we're done.
				a.setStatus(StatusDone)
				return
			}
			// Delegation task: stay alive, waiting for next task.
			a.setStatus(StatusWaitingDelegation)

		case <-time.After(200 * time.Millisecond):
			a.tick()
			// If agent reached a terminal state, exit.
			if a.Status() == StatusDone || a.Status() == StatusParked {
				return
			}
			// After tick, immediately re-check inbox without waiting.
			// This prevents the 200ms timer from delaying task receipt.
			select {
			case task := <-a.inbox:
				a.setStatus(StatusRunning)
				a.handleTask(task)
				if task.From == 0 {
					a.setStatus(StatusDone)
					return
				}
				a.setStatus(StatusWaitingDelegation)
			default:
			}
		}
	}
}

// handleTask processes a single delegation task.
func (a *Agent) handleTask(task DelegationTask) {
	if task.SystemPrompt != "" {
		a.conversation.append(Message{Role: "system", Content: task.SystemPrompt})
	}
	a.conversation.append(Message{Role: "user", Content: task.Task})
	a.runToolLoop(task)
}

// runToolLoop is the core think→call tool→check budget cycle.
func (a *Agent) runToolLoop(task DelegationTask) {
	for {
		// Check budgets.
		if a.usedSteps >= a.maxToolSteps || a.usedTokens >= a.maxToolTokens {
			a.onBudgetExhausted(task)
			return
		}

		// Think: get next LLM response.
		resp, err := a.think(task)
		if err != nil {
			a.conversation.append(Message{Role: "assistant", Content: fmt.Sprintf("Error: %v", err)})
			a.finalizeTask(task, fmt.Sprintf("Error: %v", err))
			return
		}

		// No tool calls → final answer.
		if len(resp.ToolCalls) == 0 {
			a.conversation.append(Message{Role: "assistant", Content: resp.Content})
			a.finalizeTask(task, resp.Content)
			return
		}

		// Execute each tool call.
		for _, tc := range resp.ToolCalls {
			a.usedSteps++
			result, err := a.executeTool(tc)
			if err != nil {
				a.conversation.append(Message{
					Role:    "assistant",
					Content: fmt.Sprintf("Tool %s error: %v", tc.Name, err),
				})
				a.finalizeTask(task, fmt.Sprintf("Tool %s error: %v", tc.Name, err))
				return
			}

			// Publish attention event.
			if a.session != nil {
				sa := a.session.Attention()
				if sa != nil {
					PublishToolResult(sa, a.id, tc.Name, result.Output)
				}
			}

			// Rough token estimate: 4 chars ≈ 1 token.
			a.usedTokens += len(result.Output) / 4

			a.conversation.append(Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[%s] %s", tc.Name, result.Output),
			})
		}
	}
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

// LLMResponse is what the LLM returned in a thinking round.
type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// think asks the LLM for the next action.
func (a *Agent) think(task DelegationTask) (*LLMResponse, error) {
	if a.engine == nil {
		// Phase 1 stub: no engine wired yet. Return a done response.
		return &LLMResponse{Content: "[stub] task: " + task.Task + " — engine not wired"}, nil
	}

	msgs := a.conversation.messagesCopy()
	req := CompletionRequest{
		AgentID:   a.id,
		Model:     a.model.Model,
		Provider:  a.model.Provider,
		Messages:  msgs,
		MaxTokens: 4096,
	}

	resp := a.engine.Complete(a.runCtx, req)
	if resp.Error != "" {
		return nil, fmt.Errorf("completion error: %s", resp.Error)
	}

	return a.parseResponse(resp.Content)
}

// parseResponse extracts tool calls or a final answer from LLM output.
func (a *Agent) parseResponse(content string) (*LLMResponse, error) {
	// Try JSON tool call format: {"tool_calls": [{"name":"...", "params":{...}}], "content":"..."}
	var parsed struct {
		ToolCalls []struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		} `json:"tool_calls"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(content), &parsed) == nil && len(parsed.ToolCalls) > 0 {
		resp := &LLMResponse{Content: parsed.Content}
		for _, tc := range parsed.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{Name: tc.Name, Params: tc.Params})
		}
		return resp, nil
	}
	// Plain text → final answer.
	return &LLMResponse{Content: content}, nil
}

// executeTool runs a single tool via the engine bridge.
func (a *Agent) executeTool(tc ToolCall) (ToolResult, error) {
	if a.engine == nil {
		return ToolResult{Success: false, Output: "engine not wired"}, nil
	}
	result, err := a.engine.ExecuteTool(a.runCtx, a.id, tc.Name, tc.Params)
	if err != nil {
		return ToolResult{Success: false, Output: err.Error()}, err
	}
	return ToolResult{Success: result.Success, Output: result.Output}, nil
}

// ToolResult is the output of a tool execution.
type ToolResult struct {
	Success bool
	Output  string
}

// onBudgetExhausted handles step or token cap hit.
func (a *Agent) onBudgetExhausted(task DelegationTask) {
	switch a.autonomy {
	case AutonomyFull:
		a.setStatus(StatusParked)
		a.finalizeTask(task, "Budget exhausted. Parked.")
	case AutonomyLimited:
		a.setStatusAndTask(StatusWaitingUserInput, task.Task)
		if a.session != nil {
			sa := a.session.Attention()
			if sa != nil {
				payload, _ := json.Marshal(map[string]any{
					"agent":       a.id,
					"task":        task.Task,
					"used_steps":  a.usedSteps,
					"max_steps":   a.maxToolSteps,
					"used_tokens": a.usedTokens,
					"max_tokens":  a.maxToolTokens,
				})
				sa.Publish(AttentionEvent{
					From:    a.id,
					Type:    AttentionQuestion,
					Payload: payload,
				})
			}
		}
	case AutonomyBlocked:
		a.finalizeTask(task, "Blocked.")
	}
}

// finalizeTask marks the task done and delivers result to parent if blocking.
func (a *Agent) finalizeTask(task DelegationTask, summary string) {
	result := DelegationResult{
		TaskID:     task.ID,
		Status:     TaskStatusDone,
		Summary:    summary,
		ToolCount:  a.usedSteps,
		TokensUsed: a.usedTokens,
	}

	if task.AwaitResult && a.parent != 0 {
		if parent := a.session.GetAgent(a.parent); parent != nil {
			parent.pendingMu.Lock()
			ch, ok := parent.pendingResults[task.ID]
			delete(parent.pendingResults, task.ID)
			parent.pendingMu.Unlock()
			if ok {
				select {
				case ch <- result:
				default:
				}
			}
		}
	}

	a.setStatus(StatusDone)
}

// tick is called periodically. It checks inbox and monitors children.
func (a *Agent) tick() {
	a.statusMu.RLock()
	status := a.status
	a.statusMu.RUnlock()

	// Coordinator: check children's statuses on every tick.
	if len(a.children) > 0 {
		a.monitorChildren()
	}

	if status == StatusIdle || status == StatusWaitingDelegation {
		select {
		case task := <-a.inbox:
			a.setStatus(StatusRunning)
			a.handleTask(task)
			if a.Status() == StatusDone || a.Status() == StatusParked {
				return
			}
			a.setStatus(StatusWaitingDelegation)
		default:
			if status == StatusIdle {
				a.setStatus(StatusWaitingDelegation)
			}
		}
	}
}
