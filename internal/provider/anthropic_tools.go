package provider

// anthropic_tools.go — message + tool serialization for Anthropic's native
// tool_use protocol.
//
// Anthropic represents tool dialogue as content blocks inside the same
// messages array used for ordinary chat:
//
//	assistant: [{type:"text",text:"..."}, {type:"tool_use",id:"toolu_x",name:"x",input:{...}}]
//	user:      [{type:"tool_result",tool_use_id:"toolu_x",content:"...",is_error:false}]
//
// When the model has more to say it pairs text + tool_use blocks; we mirror
// the same structure on the way back. tool_choice is sent as an object:
// {type:"auto"|"any"|"none"}.

import (
	"encoding/json"
	"strings"
)

// anthropicContentBlock is the union type used by Anthropic for response
// content blocks. Only fields relevant to the active type are populated;
// `Input` arrives as raw JSON because Anthropic emits it as an arbitrary
// object whose keys depend on the called tool's schema.
type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// anthropicMessage is what we send to /v1/messages. Content is a heterogeneous
// list of typed blocks (text, tool_use on assistant turns, tool_result on
// user turns).
type anthropicMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

func anthropicToolDescriptors(tools []ToolDescriptor) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{
			"name": t.Name,
		}
		if strings.TrimSpace(t.Description) != "" {
			entry["description"] = t.Description
		}
		if t.InputSchema != nil {
			entry["input_schema"] = t.InputSchema
		} else {
			entry["input_schema"] = map[string]any{"type": "object"}
		}
		out = append(out, entry)
	}
	return out
}

// anthropicSystemPayload returns the value that should be assigned to the
// `system` field of an Anthropic request. When the request carries
// SystemBlocks the payload becomes an array of typed text blocks — any
// block flagged Cacheable is annotated with cache_control so Anthropic's
// prompt caching amortises it across requests. When no blocks are provided
// the flat System string is returned as-is (Anthropic accepts both shapes).
// Returns nil when the system content is empty.
func anthropicSystemPayload(req CompletionRequest) any {
	blocks := make([]SystemBlock, 0, len(req.SystemBlocks))
	for _, b := range req.SystemBlocks {
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		blocks = append(blocks, b)
	}
	if len(blocks) > 0 {
		out := make([]map[string]any, 0, len(blocks))
		for _, b := range blocks {
			entry := map[string]any{
				"type": "text",
				"text": b.Text,
			}
			if b.Cacheable {
				entry["cache_control"] = map[string]any{"type": "ephemeral"}
			}
			out = append(out, entry)
		}
		return out
	}
	if text := strings.TrimSpace(req.System); text != "" {
		return text
	}
	return nil
}

// anthropicToolChoice maps the provider-agnostic tool_choice string into
// Anthropic's tool_choice object. Returns nil when the default ("auto" or
// empty) should be used — Anthropic treats absence as auto.
func anthropicToolChoice(choice string) map[string]any {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "auto":
		return nil
	case "any":
		return map[string]any{"type": "any"}
	case "none":
		return map[string]any{"type": "none"}
	default:
		return nil
	}
}

// buildAnthropicMessages converts the provider-agnostic Message slice into
// Anthropic's typed content-block format. System messages are skipped (they
// belong on the top-level `system` field). Tool round-trip messages
// (ToolCalls on assistant, ToolCallID on user) become tool_use / tool_result
// blocks.
func buildAnthropicMessages(req CompletionRequest) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(req.Messages)+1)
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		if role == "system" {
			continue
		}
		out = append(out, buildAnthropicMessage(role, m))
	}
	if len(req.Context) > 0 {
		out = append(out, anthropicMessage{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": renderContextChunks(req.Context)},
			},
		})
	}
	return out
}

func buildAnthropicMessage(role string, m Message) anthropicMessage {
	blocks := make([]any, 0, 2)

	// Tool result message (user role, ToolCallID set): emit a single
	// tool_result block. Content goes in as text since downstream tools may
	// emit anything.
	if role == "user" && strings.TrimSpace(m.ToolCallID) != "" {
		blocks = append(blocks, map[string]any{
			"type":        "tool_result",
			"tool_use_id": m.ToolCallID,
			"content":     m.Content,
			"is_error":    m.ToolError,
		})
		return anthropicMessage{Role: role, Content: blocks}
	}

	// Assistant message that previously requested tools: text first (if any),
	// then a tool_use block per call so the conversation stays well-formed.
	if role == "assistant" && len(m.ToolCalls) > 0 {
		if strings.TrimSpace(m.Content) != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
		}
		for _, call := range m.ToolCalls {
			input := call.Input
			if input == nil {
				input = map[string]any{}
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": input,
			})
		}
		return anthropicMessage{Role: role, Content: blocks}
	}

	// Plain text message.
	blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
	return anthropicMessage{Role: role, Content: blocks}
}

// splitAnthropicContent walks response content blocks and separates the text
// portion from tool_use calls. Text blocks are concatenated; unknown block
// types are dropped silently (forward-compatible with new Anthropic block
// types).
func splitAnthropicContent(blocks []anthropicContentBlock) (string, []ToolCall) {
	var text strings.Builder
	var calls []ToolCall
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			input := map[string]any{}
			if len(b.Input) > 0 {
				_ = json.Unmarshal(b.Input, &input)
			}
			calls = append(calls, ToolCall{
				ID:    b.ID,
				Name:  b.Name,
				Input: input,
			})
		}
	}
	return text.String(), calls
}

// anthropicStopReason maps Anthropic's stop_reason strings into the
// provider-agnostic StopReason enum.
func anthropicStopReason(raw string) StopReason {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tool_use":
		return StopTool
	case "end_turn", "stop_sequence":
		return StopEnd
	case "max_tokens":
		return StopLength
	default:
		return StopUnknown
	}
}
