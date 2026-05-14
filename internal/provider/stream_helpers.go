package provider

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

func emitStreamStartOnce(ctx context.Context, ch chan<- StreamEvent, announced *bool, providerName, model string) {
	if *announced {
		return
	}
	*announced = true
	select {
	case <-ctx.Done():
		return
	case ch <- StreamEvent{Type: StreamStart, Provider: providerName, Model: model}:
	}
}

func streamDoneEvent(providerName, model string, stopReason StopReason, toolCalls []ToolCall, usage Usage, usageSet bool) StreamEvent {
	done := StreamEvent{
		Type:       StreamDone,
		Provider:   providerName,
		Model:      model,
		StopReason: stopReason,
		ToolCalls:  toolCalls,
	}
	if usageSet {
		u := usage
		if u.TotalTokens == 0 {
			u.TotalTokens = u.InputTokens + u.OutputTokens
		}
		done.Usage = &u
	}
	return done
}

func finalizeOpenAIStreamToolCalls(currentCalls map[int]*ToolCall, currentArgs map[int]*strings.Builder) []ToolCall {
	if len(currentCalls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(currentCalls))
	for idx := range currentCalls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	toolCalls := make([]ToolCall, 0, len(indexes))
	for _, idx := range indexes {
		tc := currentCalls[idx]
		if tc == nil {
			continue
		}
		if args, ok := currentArgs[idx]; ok {
			_ = json.Unmarshal([]byte(args.String()), &tc.Input)
		}
		toolCalls = append(toolCalls, *tc)
	}
	return toolCalls
}
