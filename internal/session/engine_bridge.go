package session

import (
	"context"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// EngineProvider is the interface through which Session controls the shared Engine.
//
// This abstracts the DFMC Engine so Session does not depend on the concrete
// engine type directly. The real bridge (see engine_bridge_impl.go) holds a
// *Engine and delegates to its methods.
//
// Bridge points to existing DFMC code:
//   - ExecuteTool → internal/tools/engine.go:Engine.Execute (tool name + params)
//   - Complete    → internal/provider/router.go:Router.Complete
//
// TODO(phase4): Bridge to internal/engine/engine.go
type EngineProvider interface {
	// ExecuteTool runs a tool call on behalf of an agent. The agent tracks its
	// own used_steps/used_tokens based on the returned Result.
	//
	// Bridge target: internal/tools/engine.go:Engine.Execute
	ExecuteTool(ctx context.Context, agentID AgentID, name string, params map[string]any) (tools.Result, error)

	// Complete runs a synchronous LLM completion for the agent.
	//
	// Bridge target: internal/provider/router.go:Complete
	Complete(ctx context.Context, req CompletionRequest) CompletionResponse

	// PublishAttention publishes an attention event to the session's SharedAttention.
	PublishAttention(event AttentionEvent)
}

// CompletionRequest is a non-streaming LLM completion request.
type CompletionRequest struct {
	AgentID      AgentID
	Model        string // model name, e.g. "claude-sonnet-4-6"
	Provider     string // provider profile name, e.g. "anthropic"
	SystemPrompt string
	Messages     []Message
	MaxTokens    int
}

// CompletionResponse is the result of an LLM completion.
type CompletionResponse struct {
	Content    string
	StopReason string
	Usage      TokenUsage
	Error      string
}

// Message is a single conversation message.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant" | "system"
	Content string `json:"content"`
}

// TokenUsage tracks approximate token consumption.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ContextManagerHandle abstracts the per-agent context manager.
// It is responsible for ranking and compressing file snippets under a token budget.
// Bridge target: internal/context/manager.go
type ContextManagerHandle interface {
	// Build returns the current context prompt (system + user messages, compressed).
	Build(context.Context, []Message) (string, TokenUsage, error)
	// BudgetRemaining returns how many tokens are left.
	BudgetRemaining() int
	// SetBudget sets the max token budget for this context.
	SetBudget(int)
	// RecordUsage deducts from the budget after a completion or tool call.
	RecordUsage(TokenUsage)
	// Reset clears the context.
	Reset()
}
