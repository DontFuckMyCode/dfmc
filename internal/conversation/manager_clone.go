package conversation

// manager_clone.go — defensive-copy helpers for the conversation
// manager. Every public Active() / Load*() return value goes through
// cloneConversation so callers can never mutate the manager's
// in-memory state through a shared slice or map. Tool-call params and
// result metadata get their own deep-copy paths because they may
// contain provider-supplied maps the LLM round-trip later reuses.

import (
	"maps"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func cloneConversation(in *Conversation) *Conversation {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMap(in.Metadata)
	out.Branches = map[string][]types.Message{}
	for k, v := range in.Branches {
		out.Branches[k] = cloneMessages(v)
	}
	return &out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func cloneMessages(in []types.Message) []types.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Message, len(in))
	for i, msg := range in {
		out[i] = cloneMessage(msg)
	}
	return out
}

func cloneMessage(in types.Message) types.Message {
	out := in
	out.Metadata = cloneMap(in.Metadata)
	if len(in.ToolCalls) > 0 {
		out.ToolCalls = make([]types.ToolCallRecord, len(in.ToolCalls))
		for i, call := range in.ToolCalls {
			out.ToolCalls[i] = call
			out.ToolCalls[i].Params = cloneAnyMap(call.Params)
			out.ToolCalls[i].Metadata = cloneMap(call.Metadata)
		}
	}
	if len(in.Results) > 0 {
		out.Results = make([]types.ToolResultRecord, len(in.Results))
		for i, result := range in.Results {
			out.Results[i] = result
			out.Results[i].Metadata = cloneMap(result.Metadata)
		}
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}
