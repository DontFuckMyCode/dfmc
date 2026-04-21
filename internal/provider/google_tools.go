package provider

// google_tools.go — message + tool serialization for Gemini's
// generateContent protocol.
//
// Gemini puts the system prompt in a top-level `systemInstruction` and uses
// `contents` for the dialogue. Roles are {"user","model"} (no "assistant"),
// and tool dialogue rides inside the same parts array on each content:
//
//   model:    {role:"model",  parts:[{text:"..."}, {functionCall:{name,args}}]}
//   user:     {role:"user",   parts:[{functionResponse:{name,response}}]}
//
// Tool declarations go under `tools[].functionDeclarations`. Tool choice is
// expressed via `toolConfig.functionCallingConfig.mode` (AUTO|ANY|NONE).

import (
	"encoding/json"
	"fmt"
	"strings"
)

// googlePart is one element of a content's parts array. Exactly one of Text,
// FunctionCall, or FunctionResponse is populated per part.
type googlePart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *googleFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *googleFunctionResponse `json:"functionResponse,omitempty"`
}

type googleFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type googleFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// googleContent is one turn in the dialogue.
type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts"`
}

// buildGoogleContents translates DFMC's Messages into Gemini contents. The
// first user turn may be prefixed with extra code-context chunks so Gemini
// can see them; the flat System string is hoisted out to systemInstruction
// by the caller.
func buildGoogleContents(req CompletionRequest) []googleContent {
	contents := make([]googleContent, 0, len(req.Messages))
	contextInjected := false
	for _, m := range req.Messages {
		role := googleRole(string(m.Role))
		switch {
		case m.ToolCallID != "" && m.ToolName != "":
			// Tool result — Gemini expects this as user-role with a
			// functionResponse part. The response field must be an object;
			// wrap raw text output under {"result":"..."} or {"error":"..."}.
			resp := map[string]any{}
			if m.ToolError {
				resp["error"] = m.Content
			} else {
				resp["result"] = m.Content
			}
			contents = append(contents, googleContent{
				Role:  "user",
				Parts: []googlePart{{FunctionResponse: &googleFunctionResponse{Name: m.ToolName, Response: resp}}},
			})
		case len(m.ToolCalls) > 0:
			// Assistant turn that requested tools.
			parts := make([]googlePart, 0, 1+len(m.ToolCalls))
			if text := strings.TrimSpace(m.Content); text != "" {
				parts = append(parts, googlePart{Text: text})
			}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Input)
				parts = append(parts, googlePart{FunctionCall: &googleFunctionCall{Name: tc.Name, Args: args}})
			}
			contents = append(contents, googleContent{Role: "model", Parts: parts})
		default:
			text := m.Content
			if role == "user" && !contextInjected && len(req.Context) > 0 {
				text = renderContextChunks(req.Context) + "\n" + text
				contextInjected = true
			}
			contents = append(contents, googleContent{Role: role, Parts: []googlePart{{Text: text}}})
		}
	}
	return contents
}

// googleRole maps DFMC message roles to Gemini's {"user","model"} vocabulary.
// "system" never shows up here — systemInstruction is a separate field.
func googleRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "user"
	case "assistant", "model":
		return "model"
	default:
		return "user"
	}
}

// googleSystemInstruction returns the value assigned to systemInstruction.
// When SystemBlocks are set we concatenate them with blank lines between
// so the model sees the same segmented ordering cache-aware backends get.
// Returns nil when the system content is empty.
func googleSystemInstruction(req CompletionRequest) *googleContent {
	var b strings.Builder
	for _, sb := range req.SystemBlocks {
		t := strings.TrimSpace(sb.Text)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t)
	}
	if b.Len() == 0 {
		sys := strings.TrimSpace(req.System)
		if sys == "" {
			return nil
		}
		b.WriteString(sys)
	}
	return &googleContent{Parts: []googlePart{{Text: b.String()}}}
}

// googleToolDeclarations wraps our ToolDescriptors into Gemini's
// tools[].functionDeclarations[] shape.
func googleToolDeclarations(tools []ToolDescriptor) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{"name": t.Name}
		if strings.TrimSpace(t.Description) != "" {
			entry["description"] = t.Description
		}
		if t.InputSchema != nil {
			entry["parameters"] = t.InputSchema
		} else {
			entry["parameters"] = map[string]any{"type": "object"}
		}
		decls = append(decls, entry)
	}
	return []map[string]any{{"functionDeclarations": decls}}
}

// googleToolChoice converts DFMC's tool_choice string into Gemini's
// toolConfig object. Returns nil when the caller didn't set a choice.
func googleToolChoice(choice string) map[string]any {
	c := strings.ToLower(strings.TrimSpace(choice))
	if c == "" {
		return nil
	}
	var mode string
	switch c {
	case "any", "required":
		mode = "ANY"
	case "none":
		mode = "NONE"
	case "auto":
		mode = "AUTO"
	default:
		return nil
	}
	return map[string]any{
		"functionCallingConfig": map[string]any{"mode": mode},
	}
}

// parseGoogleCandidate extracts the text + tool calls + stop reason from one
// Gemini candidate entry. Tool calls get synthetic IDs of the form
// "call_<name>_<index>" — Gemini doesn't assign its own IDs and the agent
// loop needs something to echo back.
func parseGoogleCandidate(cand googleCandidate) (text string, calls []ToolCall, stop StopReason) {
	var textBuf strings.Builder
	idx := 0
	for _, part := range cand.Content.Parts {
		if strings.TrimSpace(part.Text) != "" {
			textBuf.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			input := map[string]any{}
			if len(part.FunctionCall.Args) > 0 {
				_ = json.Unmarshal(part.FunctionCall.Args, &input)
			}
			calls = append(calls, ToolCall{
				ID:    fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, idx),
				Name:  part.FunctionCall.Name,
				Input: input,
			})
			idx++
		}
	}
	return textBuf.String(), calls, googleStopReason(cand.FinishReason, len(calls) > 0)
}

// googleStopReason maps Gemini finishReason strings onto DFMC's StopReason
// enum. Gemini conflates tool-use with ordinary STOP when functionCall parts
// are present; the caller passes `hasTools` so we don't have to re-inspect
// the content to detect the tool-use case.
func googleStopReason(raw string, hasTools bool) StopReason {
	if hasTools {
		return StopTool
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "STOP":
		return StopEnd
	case "MAX_TOKENS":
		return StopLength
	case "":
		return StopUnknown
	default:
		return StopEnd
	}
}

// googleCandidate / googleUsageMetadata mirror the fields we care about in
// generateContent responses. Everything else (safetyRatings, citation data)
// is discarded.
type googleCandidate struct {
	Content      googleContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

type googleUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}
