package engine

// subagent_profiles_clone.go — deep-copy helpers used by runSubagentProfiles
// to seed each retry attempt with an independent parkedAgentState. Sibling to
// subagent_profiles.go which owns the orchestration loop, profile resolution,
// and skill-text aggregation.

import (
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func cloneParkedAgentState(seed *parkedAgentState) *parkedAgentState {
	if seed == nil {
		return nil
	}
	clone := *seed
	clone.Messages = cloneProviderMessages(seed.Messages)
	clone.Traces = cloneNativeToolTraces(seed.Traces)
	clone.Chunks = append([]types.ContextChunk(nil), seed.Chunks...)
	clone.SystemBlocks = append([]provider.SystemBlock(nil), seed.SystemBlocks...)
	clone.Descriptors = cloneToolDescriptors(seed.Descriptors)
	clone.RecentCoachHints = append([]string(nil), seed.RecentCoachHints...)
	if len(seed.LoopFileCache) > 0 {
		clone.LoopFileCache = make(map[string]string, len(seed.LoopFileCache))
		for k, v := range seed.LoopFileCache {
			clone.LoopFileCache[k] = v
		}
	}
	return &clone
}

func cloneProviderMessages(in []provider.Message) []provider.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]provider.Message, len(in))
	for i, msg := range in {
		out[i] = msg
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]provider.ToolCall, len(msg.ToolCalls))
			for j, call := range msg.ToolCalls {
				out[i].ToolCalls[j] = provider.ToolCall{
					ID:    call.ID,
					Name:  call.Name,
					Input: cloneStringAnyMap(call.Input),
				}
			}
		}
	}
	return out
}

func cloneNativeToolTraces(in []nativeToolTrace) []nativeToolTrace {
	if len(in) == 0 {
		return nil
	}
	out := make([]nativeToolTrace, len(in))
	for i, trace := range in {
		out[i] = trace
		out[i].Call = provider.ToolCall{
			ID:    trace.Call.ID,
			Name:  trace.Call.Name,
			Input: cloneStringAnyMap(trace.Call.Input),
		}
		out[i].Result.Data = cloneStringAnyMap(trace.Result.Data)
	}
	return out
}

func cloneToolDescriptors(in []provider.ToolDescriptor) []provider.ToolDescriptor {
	if len(in) == 0 {
		return nil
	}
	out := make([]provider.ToolDescriptor, len(in))
	for i, desc := range in {
		out[i] = desc
		out[i].InputSchema = cloneStringAnyMap(desc.InputSchema)
	}
	return out
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneAny(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
