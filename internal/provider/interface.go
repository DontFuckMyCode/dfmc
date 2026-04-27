package provider

import (
	"context"
	"errors"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

var (
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrProviderNotFound    = errors.New("provider not found")
	// ErrNoCapableProvider is returned by Complete/Stream when the fallback
	// cascade resolves to an empty order because every registered provider
	// was filtered out for lacking a capability the caller needs (today:
	// SupportsTools). Distinguishing this from ErrProviderNotFound matters
	// for operator messaging - the providers exist, they just cannot service
	// this specific request, and surfacing (nil, "", nil) the way the
	// zero-iteration fallthrough used to would NPE the agent loop three
	// frames up instead of telling the operator what to fix.
	ErrNoCapableProvider   = errors.New("no capable provider available")
	// ErrContextOverflow is a hint the router uses to decide whether to
	// compact the conversation and retry the same provider before falling
	// over to the next one. Providers that can detect the condition should
	// wrap their error with it (via fmt.Errorf("%w: ...", ErrContextOverflow));
	// the router also falls back to string-pattern detection for providers
	// that don't.
	ErrContextOverflow = errors.New("context length exceeded")
	// ErrProviderThrottled is the signal for "transient rate-limit or
	// overload" — 429 or 503 upstream. The router waits (respecting any
	// Retry-After hint the provider surfaced) and retries the SAME
	// provider a bounded number of times before moving to the fallback.
	// Without this sentinel every 429 immediately cascaded to offline,
	// which is rarely what the user wants.
	ErrProviderThrottled = errors.New("provider throttled")
)

// ThrottledError carries the Retry-After hint when a provider surfaces
// one. Wrap any throttled response in this type so the router's backoff
// logic can honour the upstream's requested wait. Callers read it via
// errors.As; the sentinel ErrProviderThrottled stays the primary signal
// so existing error handling keeps working.
type ThrottledError struct {
	Provider   string
	StatusCode int
	// RetryAfter is the provider's suggested wait — zero means "no
	// hint, use default backoff". Never negative.
	RetryAfter time.Duration
	Detail     string
}

func (e *ThrottledError) Error() string {
	if e == nil {
		return ErrProviderThrottled.Error()
	}
	return e.Detail
}

// Unwrap routes errors.Is(err, ErrProviderThrottled) through the
// sentinel so existing branching keeps working without an errors.As.
func (e *ThrottledError) Unwrap() error { return ErrProviderThrottled }

// ToolDescriptor is the provider-agnostic description of one callable tool.
// Anthropic and OpenAI providers serialize this into their native tool/
// function-calling shapes. Built from tools.ToolSpec by the agent loop.
type ToolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"` // JSON-Schema object
}

// ToolCall represents one tool invocation requested by the model. ID is the
// provider's correlation token (Anthropic tool_use.id, OpenAI tool_call.id);
// the same ID must come back on the result message so the model can pair
// request/response.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input,omitempty"`
}

type Message struct {
	Role    types.MessageRole `json:"role"`
	Content string            `json:"content"`

	// Provider-native tool round-trip support. These fields are zero-valued
	// for ordinary chat turns; populated only when the message is part of a
	// tool dialogue.

	// ToolCalls: assistant turns that requested tool invocations.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID: result turns echo the originating call's ID.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolName: result turns echo the originating tool name (OpenAI requires it).
	ToolName string `json:"tool_name,omitempty"`
	// ToolError: result turns surface execution failure to the model.
	ToolError bool `json:"tool_error,omitempty"`
}

// SystemBlock is a labelled fragment of the system prompt that carries a
// cacheability hint. Providers that support prompt caching (Anthropic) emit
// a cache-control annotation on blocks where Cacheable is true; providers
// that do not simply concatenate the text.
type SystemBlock struct {
	Label     string `json:"label,omitempty"`
	Text      string `json:"text"`
	Cacheable bool   `json:"cacheable,omitempty"`
}

type CompletionRequest struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	System   string `json:"system,omitempty"`
	// SystemBlocks optionally mirrors System as an ordered list of labelled
	// fragments. When set, providers that understand prompt caching prefer
	// these over the flat System string. When empty, the flat string is
	// authoritative. Both may be set simultaneously; SystemBlocks wins for
	// cache-aware providers.
	SystemBlocks []SystemBlock        `json:"system_blocks,omitempty"`
	Messages     []Message            `json:"messages"`
	Context      []types.ContextChunk `json:"context,omitempty"`

	// Tools advertises which tools the model may call. Empty disables tool
	// calling for the request even if the provider supports it.
	Tools []ToolDescriptor `json:"tools,omitempty"`
	// ToolChoice: "auto" (default), "any" (force one), or "none".
	ToolChoice string `json:"tool_choice,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// StopReason carries why generation stopped. Provider-native loops dispatch
// on StopTool to decide whether to execute tool calls and continue.
type StopReason string

const (
	StopEnd     StopReason = "end_turn"
	StopTool    StopReason = "tool_use"
	StopLength  StopReason = "max_tokens"
	StopUnknown StopReason = ""
)

type CompletionResponse struct {
	Text       string     `json:"text"`
	Model      string     `json:"model,omitempty"`
	Usage      Usage      `json:"usage,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StopReason StopReason `json:"stop_reason,omitempty"`
}

type StreamEventType string

const (
	// StreamStart is emitted once at the top of a stream to announce the
	// resolved provider/model pair. Consumers that need to display which
	// upstream handled a streaming request pick this up instead of waiting
	// for StreamDone. Providers are not required to emit it; consumers must
	// tolerate streams that open with a StreamDelta.
	StreamStart StreamEventType = "start"
	// StreamDelta carries a text increment. Concatenating all Delta strings
	// from a single stream yields the full assistant message.
	StreamDelta StreamEventType = "delta"
	// StreamDone is the terminal success event. Providers SHOULD populate
	// Model/Usage/StopReason when the upstream API surfaces those values.
	StreamDone StreamEventType = "done"
	// StreamError is the terminal failure event. Err carries the underlying
	// error; consumers must check it rather than Delta.
	StreamError StreamEventType = "error"
)

// StreamEvent is the provider-agnostic streaming event. The shape is
// backward-compatible: consumers that only switch on Type and read Delta/Err
// keep working, while newer consumers can pick up Model/Usage/StopReason from
// the terminal StreamDone event without a separate Complete call.
type StreamEvent struct {
	Type  StreamEventType `json:"type"`
	Delta string          `json:"delta,omitempty"`
	Err   error           `json:"-"`

	// Model identifies the upstream model that served the request. Set on
	// StreamStart (when known) and repeated on StreamDone. Zero-valued on
	// intermediate StreamDelta events to keep payloads small.
	Model string `json:"model,omitempty"`
	// Provider echoes the logical provider name (e.g. "anthropic",
	// "deepseek"). Same lifecycle as Model.
	Provider string `json:"provider,omitempty"`
	// Usage carries token accounting from the upstream response when the
	// provider reports it. Nil when absent (most SSE backends only report
	// usage on the terminal event, and some not at all).
	Usage *Usage `json:"usage,omitempty"`
	// StopReason explains why generation ended. Populated on StreamDone.
	StopReason StopReason `json:"stop_reason,omitempty"`
}

type ProviderHints struct {
	ToolStyle   string   `json:"tool_style"`
	Cache       bool     `json:"cache"`
	LowLatency  bool     `json:"low_latency"`
	BestFor     []string `json:"best_for,omitempty"`
	MaxContext  int      `json:"max_context"`
	DefaultMode string   `json:"default_mode,omitempty"`
	// SupportsTools: true when the provider can negotiate provider-native
	// tool calling (Anthropic tool_use, OpenAI tool_calls). False for offline
	// and placeholder stand-ins; the agent loop falls back to text-only mode
	// instead of asking these providers for tool dialogue.
	SupportsTools bool `json:"supports_tools,omitempty"`
}

type Provider interface {
	Name() string
	Model() string
	Models() []string // ordered list of available models; first is preferred
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error)
	CountTokens(text string) int
	MaxContext() int
	Hints() ProviderHints
}
