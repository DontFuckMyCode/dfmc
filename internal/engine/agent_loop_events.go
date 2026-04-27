// agent_loop_events.go — persistence and event-bus publishers for the
// native agent loop:
//
//   - recordNativeAgentInteraction: writes the completed turn into the
//     conversation log and memory store, preserving per-step tool_call /
//     tool_result records so a restart can replay the full trajectory.
//   - publishNativeToolCall / publishNativeToolResultWithPayload: fire the
//     "tool:call" and "tool:result" events the TUI chip strip and web
//     activity feed both subscribe to. The result event carries RTK
//     compression stats (raw vs. post-trim payload) so the UI can surface
//     the savings.
//
// Extracted from agent_loop_native.go to keep the main loop file focused
// on control flow.

package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) recordNativeAgentInteraction(question string, completion nativeToolCompletion) {
	now := time.Now()
	assistantMsg := types.Message{
		Role:      types.RoleAssistant,
		Content:   completion.Answer,
		Timestamp: now,
		TokenCnt:  completion.TokenCount,
		Metadata: map[string]string{
			"provider":    completion.Provider,
			"model":       completion.Model,
			"tool_rounds": fmt.Sprintf("%d", len(completion.ToolTraces)),
			"surface":     "native",
		},
	}
	for _, trace := range completion.ToolTraces {
		callMetadata := map[string]string{
			"provider":     trace.Provider,
			"model":        trace.Model,
			"step":         fmt.Sprintf("%d", trace.Step),
			"tool_call_id": trace.Call.ID,
		}
		resultMetadata := map[string]string{
			"provider":     trace.Provider,
			"model":        trace.Model,
			"step":         fmt.Sprintf("%d", trace.Step),
			"tool_call_id": trace.Call.ID,
		}
		if trace.Err != "" {
			resultMetadata["error"] = trace.Err
		}
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, types.ToolCallRecord{
			Name:      trace.Call.Name,
			Params:    trace.Call.Input,
			Timestamp: trace.OccurredAt,
			Metadata:  callMetadata,
		})
		assistantMsg.Results = append(assistantMsg.Results, types.ToolResultRecord{
			Name:      trace.Call.Name,
			Output:    strings.TrimSpace(trace.Result.Output),
			Success:   trace.Err == "",
			Timestamp: trace.OccurredAt,
			Metadata:  resultMetadata,
		})
	}

	if e.Conversation != nil {
		e.Conversation.AddMessage(completion.Provider, completion.Model, types.Message{
			Role:      types.RoleUser,
			Content:   question,
			Timestamp: now,
		})
		e.Conversation.AddMessage(completion.Provider, completion.Model, assistantMsg)
		// Persist after every completed turn — without this the
		// JSONL is only flushed at engine.Shutdown(), so a panic,
		// SIGKILL, OOM, or power loss between turns silently drops
		// the entire in-memory conversation. The save uses an atomic
		// temp + rename (storage.SaveConversationLog), so the write
		// cost is one disk transaction per turn and any reader either
		// sees the previous full log or the new full log — never a
		// torn intermediate.
		e.saveActiveConversationWithWarning("turn_complete", map[string]any{
			"question": truncateRunesWithMarker(question, 120, "..."),
		})
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, completion.Answer)
		for _, ch := range completion.Context {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, completion.Answer, 0.75)
	}
}

func (e *Engine) publishNativeToolCall(trace nativeToolTrace) {
	if e.EventBus == nil {
		return
	}
	e.EventBus.Publish(Event{
		Type:   "tool:call",
		Source: "engine",
		Payload: map[string]any{
			"tool":           trace.Call.Name,
			"params":         trace.Call.Input,
			"params_preview": formatToolParamsPreview(trace.Call.Input, 180),
			"step":           trace.Step,
			"provider":       trace.Provider,
			"model":          trace.Model,
			"tool_call_id":   trace.Call.ID,
			"surface":        "native",
		},
	})
}

// publishNativeToolResultWithPayload emits a tool:result event enriched
// with RTK compression stats — the exact bytes (and token estimate) that
// go back to the model after the noise filter + char-cap trim. When
// modelPayload is empty (e.g. coming from the legacy publish path), the
// payload-size fields are omitted. The diff between raw output and payload
// is the RTK savings the TUI stats panel can surface.
func (e *Engine) publishNativeToolResultWithPayload(trace nativeToolTrace, modelPayload string) {
	if e.EventBus == nil {
		return
	}
	outputText := trace.Result.Output
	payload := map[string]any{
		"tool":           trace.Call.Name,
		"success":        trace.Err == "",
		"durationMs":     trace.Result.DurationMs,
		"step":           trace.Step,
		"provider":       trace.Provider,
		"model":          trace.Model,
		"truncated":      trace.Result.Truncated,
		"output_preview": compactToolPayload(outputText, 180),
		"output_chars":   len(outputText),
		"output_tokens":  tokens.Estimate(outputText),
		"tool_call_id":   trace.Call.ID,
		"surface":        "native",
	}
	if modelPayload != "" {
		payload["payload_chars"] = len(modelPayload)
		payload["payload_tokens"] = tokens.Estimate(modelPayload)
		if raw := len(outputText); raw > 0 {
			saved := max(raw-len(modelPayload), 0)
			payload["compression_saved_chars"] = saved
			// Ratio kept as float so the UI can render "83%".
			payload["compression_ratio"] = float64(len(modelPayload)) / float64(raw)
		}
	}
	if trace.Err != "" {
		payload["error"] = trace.Err
	}
	if summary := batchFanoutSummary(trace.Call.Name, trace.Result.Data); summary != nil {
		for k, v := range summary {
			payload[k] = v
		}
	}
	e.EventBus.Publish(Event{
		Type:    "tool:result",
		Source:  "engine",
		Payload: payload,
	})
}
