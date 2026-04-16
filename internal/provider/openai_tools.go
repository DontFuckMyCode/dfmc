package provider

// openai_tools.go — message + tool serialization for OpenAI's
// function-calling / tool_calls protocol.
//
// OpenAI expresses tool calls as fields on chat messages:
//
//	assistant: {role:"assistant", content:null, tool_calls:[{id,type:"function",function:{name,arguments(JSON string)}}]}
//	tool:      {role:"tool", tool_call_id:"call_x", name:"x", content:"..."}
//
// Tool definitions are sent as {"tools":[{type:"function",function:{name,description,parameters}}]}.
// `parameters` is a JSON-Schema object — the same one tools.ToolSpec.JSONSchema()
// produces.

import (
	"encoding/json"
	"strings"
)

// openaiChatMessage models the response message shape we care about. Content
// can be null when the model emits only tool_calls; we leave it as a Go
// string (empty when null was sent).
type openaiChatMessage struct {
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	ToolCalls []openaiToolCall   `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string per OpenAI spec
}

func openaiToolDescriptors(tools []ToolDescriptor) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{
			"name": t.Name,
		}
		if strings.TrimSpace(t.Description) != "" {
			fn["description"] = t.Description
		}
		if t.InputSchema != nil {
			fn["parameters"] = t.InputSchema
		} else {
			fn["parameters"] = map[string]any{"type": "object"}
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": fn,
		})
	}
	return out
}

// openaiToolChoice maps the provider-agnostic choice into OpenAI's shape.
// OpenAI accepts the strings "auto", "required", "none" or an object
// {type:"function",function:{name:"x"}} to force a specific tool. We only
// surface the high-level modes; targeted forcing is left for future use.
func openaiToolChoice(choice string) any {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "auto":
		return nil
	case "any":
		return "required" // OpenAI's name for "any"
	case "none":
		return "none"
	default:
		return nil
	}
}

// buildOpenAIMessages converts provider-agnostic Messages into OpenAI's
// message array. Tool round-trip messages get role=tool with tool_call_id;
// assistant tool-call requests get content="" plus the tool_calls array.
func buildOpenAIMessages(req CompletionRequest) []map[string]any {
	out := make([]map[string]any, 0, len(req.Messages)+2)
	if strings.TrimSpace(req.System) != "" {
		out = append(out, map[string]any{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		out = append(out, buildOpenAIMessage(role, m))
	}
	if len(req.Context) > 0 {
		out = append(out, map[string]any{
			"role":    "system",
			"content": renderContextChunks(req.Context),
		})
	}
	return out
}

func buildOpenAIMessage(role string, m Message) map[string]any {
	// Tool result message: surface as role=tool with the originating call id.
	if strings.TrimSpace(m.ToolCallID) != "" {
		entry := map[string]any{
			"role":         "tool",
			"tool_call_id": m.ToolCallID,
			"content":      m.Content,
		}
		if strings.TrimSpace(m.ToolName) != "" {
			entry["name"] = m.ToolName
		}
		return entry
	}
	// Assistant message that requested tools.
	if role == "assistant" && len(m.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			argBytes, _ := json.Marshal(c.Input)
			if argBytes == nil {
				argBytes = []byte("{}")
			}
			calls = append(calls, map[string]any{
				"id":   c.ID,
				"type": "function",
				"function": map[string]any{
					"name":      c.Name,
					"arguments": string(argBytes),
				},
			})
		}
		entry := map[string]any{
			"role":       "assistant",
			"tool_calls": calls,
		}
		// OpenAI accepts content alongside tool_calls; include if present.
		if strings.TrimSpace(m.Content) != "" {
			entry["content"] = m.Content
		} else {
			entry["content"] = ""
		}
		return entry
	}
	return map[string]any{
		"role":    role,
		"content": m.Content,
	}
}

func parseOpenAIToolCalls(raw []openaiToolCall) []ToolCall {
	if len(raw) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(raw))
	for _, c := range raw {
		input := map[string]any{}
		if strings.TrimSpace(c.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(c.Function.Arguments), &input)
		}
		out = append(out, ToolCall{
			ID:    c.ID,
			Name:  c.Function.Name,
			Input: input,
		})
	}
	return out
}

// openaiStopReason maps OpenAI's finish_reason into the provider-agnostic
// StopReason. "tool_calls" is the trigger for the agent loop to dispatch
// and continue.
func openaiStopReason(raw string) StopReason {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tool_calls", "function_call":
		return StopTool
	case "stop":
		return StopEnd
	case "length":
		return StopLength
	default:
		return StopUnknown
	}
}
