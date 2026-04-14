package provider

import (
	"context"
	"errors"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

var (
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrProviderNotFound    = errors.New("provider not found")
)

type Message struct {
	Role    types.MessageRole `json:"role"`
	Content string            `json:"content"`
}

type CompletionRequest struct {
	Provider string               `json:"provider,omitempty"`
	Model    string               `json:"model,omitempty"`
	System   string               `json:"system,omitempty"`
	Messages []Message            `json:"messages"`
	Context  []types.ContextChunk `json:"context,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type CompletionResponse struct {
	Text  string `json:"text"`
	Model string `json:"model,omitempty"`
	Usage Usage  `json:"usage,omitempty"`
}

type StreamEventType string

const (
	StreamDelta StreamEventType = "delta"
	StreamDone  StreamEventType = "done"
	StreamError StreamEventType = "error"
)

type StreamEvent struct {
	Type  StreamEventType `json:"type"`
	Delta string          `json:"delta,omitempty"`
	Err   error           `json:"-"`
}

type ProviderHints struct {
	ToolStyle   string   `json:"tool_style"`
	Cache       bool     `json:"cache"`
	LowLatency  bool     `json:"low_latency"`
	BestFor     []string `json:"best_for,omitempty"`
	MaxContext  int      `json:"max_context"`
	DefaultMode string   `json:"default_mode,omitempty"`
}

type Provider interface {
	Name() string
	Model() string
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error)
	CountTokens(text string) int
	MaxContext() int
	Hints() ProviderHints
}
