package engine

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) parkNativeLoopForShutdown(s *loopRunState, step int, state EngineState) nativeToolCompletion {
	headline := fmt.Sprintf(
		"Parked at step %d — engine is shutting down (%d tool rounds, ~%d tokens).",
		step, len(s.traces), s.totalTokens,
	)
	notice := composeParkedNotice(headline, s.traces,
		`Restart dfmc and resume — your work is saved.`)
	e.publishAgentLoopEvent("agent:loop:shutdown_parked", map[string]any{
		"step":    step,
		"state":   int(state),
		"surface": "native",
	})
	return s.park(e, notice, ParkReasonShuttingDown)
}

func (e *Engine) injectQueuedAgentNotes(s *loopRunState, step int) {
	for _, note := range e.drainAgentNotes() {
		s.msgs = append(s.msgs, provider.Message{
			Role:    types.RoleUser,
			Content: "[user btw] " + note,
		})
		e.publishAgentLoopEvent("agent:note:injected", map[string]any{
			"step": step,
			"note": note,
		})
	}
}

func (e *Engine) maybeInjectSynthesisHint(s *loopRunState, step int, alreadyInjected bool) bool {
	if alreadyInjected || len(s.traces) < s.lim.RoundSoftCap {
		return alreadyInjected
	}
	s.msgs = append(s.msgs, provider.Message{
		Role: types.RoleUser,
		Content: fmt.Sprintf(
			"[system] Checkpoint: %d tool rounds in. If the original task is genuinely complete, "+
				"share the result now. Otherwise keep working — read, edit, run, verify — until "+
				"you've reached a real stopping point. The goal is sustained progress, not a "+
				"premature wrap-up. When you do stop, end with a 2-3 sentence summary covering "+
				"what you accomplished, what's still open, and the natural next step.",
			len(s.traces),
		),
	})
	e.publishAgentLoopEvent("agent:loop:synthesize_hint", map[string]any{
		"step":        step,
		"tool_rounds": len(s.traces),
		"surface":     "native",
	})
	return true
}

func (e *Engine) parkNativeLoopForInterruptedContext(s *loopRunState, step int, ctxErr error) nativeToolCompletion {
	headline := fmt.Sprintf(
		"Parked at step %d — interrupted (%d tool rounds, ~%d tokens).",
		step, len(s.traces), s.totalTokens,
	)
	notice := composeParkedNotice(headline, s.traces,
		`Type /continue (or just "continue") to resume — your work is saved.`)
	e.publishAgentLoopEvent("agent:loop:interrupted", map[string]any{
		"step":        step,
		"tool_rounds": len(s.traces),
		"error":       ctxErr.Error(),
		"surface":     "native",
	})
	return s.park(e, notice, ParkReasonInterrupted)
}

func (e *Engine) completeNativeLoop(s *loopRunState, step int, resp *provider.CompletionResponse) nativeToolCompletion {
	completion := nativeToolCompletion{
		Answer:       resp.Text,
		Provider:     s.lastProvider,
		Model:        s.lastModel,
		TokenCount:   s.totalTokens,
		Context:      s.chunks,
		ToolTraces:   s.traces,
		SystemPrompt: s.systemPrompt,
	}
	e.recordNativeAgentInteraction(s.question, completion)
	e.publishAgentLoopEvent("agent:loop:final", map[string]any{
		"step":           step,
		"max_tool_steps": s.lim.MaxSteps,
		"tool_rounds":    len(s.traces),
		"tokens_used":    s.totalTokens,
		"provider":       s.lastProvider,
		"model":          s.lastModel,
		"surface":        "native",
	})
	e.publishProviderCompleteWithSource(s.lastProvider, s.lastModel, s.totalTokens, "agent_loop", s.question, completion.Answer, resp.Usage)
	e.emitCoachNotes(s.question, completion)
	return completion
}

func (e *Engine) publishNativeNarration(step int, resp *provider.CompletionResponse) {
	if text := strings.TrimSpace(resp.Text); text != "" {
		e.publishAgentLoopEvent("agent:loop:narration", map[string]any{
			"step":  step,
			"text":  text,
			"tools": len(resp.ToolCalls),
		})
	}
}

func (s *loopRunState) parkAtStepCap(e *Engine, step int) nativeToolCompletion {
	headline := fmt.Sprintf(
		"Parked at step %d — hit the configured ceiling (%d tool rounds, ~%d tokens).",
		step, len(s.traces), s.totalTokens,
	)
	notice := composeParkedNotice(headline, s.traces,
		`Type /continue to resume — add a note to redirect (e.g. "/continue focus on the test file").`)
	return s.park(e, notice, ParkReasonStepCap)
}
