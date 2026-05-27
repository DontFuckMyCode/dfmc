package memory

import "context"

// LLMUpdater abstracts the LLM call needed by RequestLLMUpdate so the
// memory package stays engine/provider-independent.
type LLMUpdater interface {
	// Call takes a provider+model hint and a prompt, returns the raw text
	// response from the LLM (max LLMUpdateMaxTokens tokens). Empty string
	// means the call failed and should be treated as a no-op.
	Call(ctx context.Context, providerHint, modelHint, prompt string) (string, error)
}
