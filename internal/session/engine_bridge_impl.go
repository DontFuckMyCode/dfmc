package session

import (
	"context"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// engineBridge is a minimal EngineProvider that wraps an opaque engine reference.
// Stored as interface{} in session to avoid importing engine from session:
//   engine → session (for *session.Session in AttachSession)
//   session → engine (via interface{} cast at call site, no import needed)
//
// The real engine is passed as an opaque interface{} and cast at execution time.
type engineBridge struct {
	engine any // stored as any to avoid importing engine in session
}

// newEngineBridgeForSession creates an EngineProvider wrapping the given engine.
// The engine parameter is typed as interface{} to avoid a compile-time import
// of engine in session (which would create engine→session→engine cycle).
func newEngineBridgeForSession(engine any) EngineProvider {
	return &engineBridge{engine: engine}
}

// AttachSessionToEngine wires a session to a real engine.
// Exported so engine can call it during AttachSession.
func AttachSessionToEngine(sess *Session, engine any) {
	if sess != nil && engine != nil {
		sess.AttachEngine(newEngineBridgeForSession(engine))
	}
}

// providerCaller is the interface that *engine.Engine.Providers exposes.
// We use this to call Providers.Complete without importing engine.
type providerCaller interface {
	Complete(ctx context.Context, req providerCompletionRequest) (providerCompletionResponse, string, error)
}

// eventPublisher interface is satisfied by *engine.Engine.EventBus.
// We use this to publish attention events to the engine EventBus without
// importing engine.
type eventPublisher interface {
	Publish(event Event)
}

// Event mirrors engine.Event so we can construct one without importing engine.
type Event struct {
	Type    string
	Source  string
	Payload any
}

// providerCompletionRequest mirrors provider.CompletionRequest field layout.
// Uses plain fields (no json tags) since this is passed via interface{}.
// Tags would make this incompatible with the plain-field slice we build locally.
type providerCompletionRequest struct {
	Provider  string
	Model     string
	System    string
	Messages  []struct{ Role, Content string }
	MaxTokens int
}

// providerCompletionResponse mirrors provider.CompletionResponse field layout.
type providerCompletionResponse struct {
	Text       string
	Model      string
	Usage      providerUsageResponse
	ToolCalls  []toolCallEntry
	StopReason string
}

type providerUsageResponse struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type toolCallEntry struct {
	ID    string
	Name  string
	Input string
}

func (b *engineBridge) ExecuteTool(ctx context.Context, agentID AgentID, name string, params map[string]any) (tools.Result, error) {
	if b.engine == nil {
		return tools.Result{}, ErrEngineNotInitialized
	}
	type toolExecutor interface {
		Execute(ctx context.Context, name string, req struct {
			ProjectRoot string
			Params      map[string]any
		}) (tools.Result, error)
	}
	exec, ok := b.engine.(toolExecutor)
	if !ok {
		return tools.Result{}, ErrEngineNotInitialized
	}
	return exec.Execute(ctx, name, struct {
		ProjectRoot string
		Params      map[string]any
	}{"", params})
}

func (b *engineBridge) Complete(ctx context.Context, req CompletionRequest) CompletionResponse {
	if b.engine == nil {
		return CompletionResponse{Error: "engine not initialized"}
	}
	caller, ok := b.engine.(providerCaller)
	if !ok {
		return CompletionResponse{Error: "engine bridge complete not wired"}
	}
	msgs := make([]struct{ Role, Content string }, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = struct{ Role, Content string }{Role: m.Role, Content: m.Content}
	}
	// Convert session request to provider request format.
	providerReq := providerCompletionRequest{
		Provider:  req.Provider,
		Model:     req.Model,
		System:    req.SystemPrompt,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
	}
	resp, _, err := caller.Complete(ctx, providerReq)
	if err != nil {
		return CompletionResponse{Error: err.Error()}
	}
	return CompletionResponse{
		Content:    resp.Text,
		StopReason: resp.StopReason,
		Usage: TokenUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}
}



func (b *engineBridge) PublishAttention(event AttentionEvent) {
	if b.engine == nil {
		return
	}
	pub, ok := b.engine.(eventPublisher)
	if !ok {
		return
	}
	payload := map[string]any{
		"agent_id":  event.From,
		"type":      event.Type.String(),
		"payload":   string(event.Payload),
		"id":        event.ID.String(),
		"timestamp": event.Timestamp.Format(time.RFC3339),
	}
	pub.Publish(Event{
		Type:    "session:attention",
		Source:  "session",
		Payload: payload,
	})
}

// ErrEngineNotInitialized is returned when the engine bridge has no engine set.
var ErrEngineNotInitialized = &engineError{msg: "engine not initialized"}

type engineError struct{ msg string }

func (e *engineError) Error() string { return e.msg }