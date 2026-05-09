package engine

// agent_loop_native_entry.go — provider-native agent loop entry point
// + small surface helpers. The orchestration loop itself
// (runNativeToolLoop) and per-step phase helpers
// (computeNativeToolChoice, applyNativeBudgetCompaction,
// updateNativeTokenFootprint, buildNativeLoopRequest) live in
// agent_loop_native.go. Per-step phases (preflightBudget,
// postStepBudget, executeAndAppendToolBatch, handleEmptyTurn) live in
// agent_loop_phases.go.

import (
	"context"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type nativeToolTrace struct {
	Call       provider.ToolCall
	Result     tools.Result
	Err        string
	Provider   string
	Model      string
	Step       int
	OccurredAt time.Time
}

type nativeToolCompletion struct {
	Answer       string
	Provider     string
	Model        string
	TokenCount   int
	Context      []types.ContextChunk
	ToolTraces   []nativeToolTrace
	SystemPrompt string
	// Parked is true when the loop hit MaxSteps and saved its state for /continue
	// to pick up. Answer is a friendly "parked at step N" notice in that case.
	Parked       bool
	ParkedAtStep int
	// ParkedReason discriminates why the loop parked so downstream surfaces
	// (coach, TUI) can tailor their copy. Values: ParkReasonStepCap or
	// ParkReasonBudgetExhausted. Empty when Parked is false.
	ParkedReason ParkReason
}

// shouldUseNativeToolLoop reports whether the active provider negotiates
// provider-native tool calling (Anthropic, OpenAI) AND has at least one
// backend tool to expose. Falls back to false for offline/placeholder.
func (e *Engine) shouldUseNativeToolLoop() bool {
	if e == nil || e.Tools == nil || e.Providers == nil {
		return false
	}
	if len(e.Tools.BackendSpecs()) == 0 {
		return false
	}
	p, ok := e.Providers.Get(e.provider())
	if !ok || p == nil {
		return false
	}
	return p.Hints().SupportsTools
}

func (e *Engine) askWithNativeTools(ctx context.Context, question string, onDelta ...func(string)) (nativeToolCompletion, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	e.ensureIndexed(ctx)

	// Fresh question → abandon any stale parked loop.
	e.ClearParkedAgent()

	preflight := e.prepareAutonomyPreflight(ctx, question, "top_level", true)
	chunks := e.buildContextChunks(question)
	systemPrompt, systemBlocks := e.buildNativeToolSystemPromptBundle(question, chunks, preflight)
	descriptors := metaSpecsToDescriptors(e.Tools.MetaSpecs())
	lim := e.agentLimits()
	kickoffTail, kickoffTraces := e.maybeAutoKickoffAutonomy(ctx, question, preflight, lim)

	contextTokens := 0
	for _, c := range chunks {
		contextTokens += c.TokenCount
	}
	protocol := ""
	baseURL := ""
	if e.Config != nil {
		if profile, ok := e.Config.Providers.Profiles[e.provider()]; ok {
			protocol = strings.TrimSpace(profile.Protocol)
			baseURL = strings.TrimSpace(profile.BaseURL)
		}
	}

	var deltaFn func(string)
	if len(onDelta) > 0 {
		deltaFn = onDelta[0]
	}

	seed := &parkedAgentState{
		Question:      question,
		Messages:      e.buildToolLoopRequestMessages(question, chunks, systemPrompt, kickoffTail),
		Traces:        kickoffTraces,
		Chunks:        chunks,
		SystemPrompt:  systemPrompt,
		SystemBlocks:  systemBlocks,
		Descriptors:   descriptors,
		ContextTokens: contextTokens,
		TotalTokens:   0,
		Step:          0,
		LastProvider:  e.provider(),
		LastModel:     e.model(),
	}
	e.publishAgentLoopEvent("agent:loop:start", map[string]any{
		"provider":        seed.LastProvider,
		"model":           seed.LastModel,
		"protocol":        protocol,
		"base_url":        baseURL,
		"max_tool_steps":  lim.MaxSteps,
		"max_tool_tokens": lim.MaxTokens,
		"surface":         "native",
		"context_files":   len(chunks),
		"context_tokens":  contextTokens,
		"meta_tools":      metaToolNames(descriptors),
	})
	return e.runNativeToolLoopAutonomous(ctx, seed, lim, "ask", deltaFn)
}

// initialSynthesisFlag computes the starting value of the
// synthesizeHintInjected gate for a new loop iteration. For a fresh
// run the gate matches the original "did we already cross the soft
// cap?" condition. For an auto-resumed run (CumulativeSteps>0) the
// gate is forced false so the nudge can fire again — the prior one
// was compacted away with the rest of the transcript and the model
// needs re-anchoring, not silence. Extracted to a helper so the
// re-arm condition is unit-testable without standing up a full
// scripted-provider end-to-end fixture.
func initialSynthesisFlag(s *loopRunState, lim agentLimits) bool {
	if s == nil {
		return false
	}
	if s.seed != nil && s.seed.CumulativeSteps > 0 {
		return false
	}
	return len(s.traces) >= lim.RoundSoftCap
}
