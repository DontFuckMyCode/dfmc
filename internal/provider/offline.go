package provider

import (
	"context"
	"strings"
)

type OfflineProvider struct{}

func NewOfflineProvider() *OfflineProvider {
	return &OfflineProvider{}
}

func (p *OfflineProvider) Name() string {
	return "offline"
}

func (p *OfflineProvider) Model() string   { return "offline-analyzer-v1" }
func (p *OfflineProvider) Models() []string { return []string{"offline-analyzer-v1"} }

func (p *OfflineProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	question := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			question = strings.TrimSpace(req.Messages[i].Content)
			break
		}
	}

	// Pick the task out of the merged system prompt so the heuristic
	// pipeline matches what the engine routed us for.
	task := detectOfflineTask(req.System, question)

	var out string
	if len(req.Context) == 0 && question == "" {
		out = "DFMC offline mode is active but no question or context was provided. " +
			"Type a question or open files so the offline analyzer has something to look at. " +
			"Configure an API key (`dfmc providers setup`) to enable online LLM generation."
	} else {
		out = analyzeOffline(task, question, req.Context)
		out += "\n\n---\n_DFMC offline — configure a provider API key (`dfmc providers setup`) for LLM generation._"
	}

	usage := Usage{
		InputTokens:  p.CountTokens(question),
		OutputTokens: p.CountTokens(out),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens

	return &CompletionResponse{
		Text:  out,
		Model: p.Model(),
		Usage: usage,
	}, nil
}

func (p *OfflineProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		resp, err := p.Complete(ctx, req)
		if err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		ch <- StreamEvent{Type: StreamStart, Provider: p.Name(), Model: resp.Model}
		lines := strings.Split(resp.Text, "\n")
		for _, line := range lines {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: StreamError, Err: ctx.Err()}
				return
			case ch <- StreamEvent{Type: StreamDelta, Delta: line + "\n"}:
			}
		}
		usage := resp.Usage
		ch <- StreamEvent{
			Type:       StreamDone,
			Provider:   p.Name(),
			Model:      resp.Model,
			Usage:      &usage,
			StopReason: StopEnd,
		}
	}()
	return ch, nil
}

func (p *OfflineProvider) CountTokens(text string) int {
	return len(strings.Fields(text))
}

func (p *OfflineProvider) MaxContext() int {
	return 12000
}

func (p *OfflineProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:   "none",
		Cache:       false,
		LowLatency:  true,
		BestFor:     []string{"offline-analysis", "fallback"},
		MaxContext:  p.MaxContext(),
		DefaultMode: "standard",
	}
}
