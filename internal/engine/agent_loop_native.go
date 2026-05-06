package engine

// agent_loop_native.go — provider-native agent loop.
//
// The model only sees the 4 meta tools (tool_search, tool_help, tool_call,
// tool_batch_call). It discovers backend tools through tool_search/tool_help
// and invokes them through tool_call / tool_batch_call. Tool dialogue rides
// on Anthropic's tool_use blocks or OpenAI's tool_calls — the text-bridge
// fenced JSON format is gone.
//
// The loop is bounded by maxNativeToolSteps (config-overridable in S4).
// Per-call failures don't abort the loop; the model gets a tool_result with
// is_error=true and decides how to recover.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// parkPhase, ParkReason, formatBudgetExhaustedNotice, and parkNativeToolLoop
// live in agent_parking.go.

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

func (e *Engine) askWithNativeTools(ctx context.Context, question string) (nativeToolCompletion, error) {
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
	return e.runNativeToolLoopAutonomous(ctx, seed, lim, "ask")
}

// maxBudgetAutoRecoveries caps how many times a single agent-loop invocation
// will auto-compact + reset tokens on budget_exhausted before giving up and
// parking. One is usually enough: Fix A's force-compact on resume already
// handles the bulk of the bloat; this is the safety net for runs that keep
// growing mid-loop. Higher values risk infinite compact→fill→compact cycles
// when the model's asks inherently generate more data than fits.
const maxBudgetAutoRecoveries = 1

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

// stuckStreakHardstopThreshold is the number of consecutive rounds the
// trajectory layer must flag the repeated-failure pattern before the
// loop forces tool_choice="none" on the next provider call. Three is
// the smallest value that reliably distinguishes "the model is iterating
// on a hard problem" (one stuck round, sometimes two) from "the model
// is ignoring the switch-tactic hint and grinding on the same broken
// approach" (three+). Bumping this would let unattended runs waste
// more steps; lowering it would interrupt productive multi-attempt
// recoveries. Not config-tunable yet — bake in the floor first, expose
// when we have evidence the default is wrong for some workload.
const stuckStreakHardstopThreshold = 3

func (e *Engine) runNativeToolLoop(ctx context.Context, seed *parkedAgentState, lim agentLimits) (nativeToolCompletion, error) {
	callBudget, depthLimit := 0, 0
	if e.Config != nil {
		callBudget = e.Config.Agent.MetaCallBudget
		depthLimit = e.Config.Agent.MetaDepthLimit
	}
	ctx = tools.SeedMetaToolBudgetWithLimits(ctx, callBudget, depthLimit)

	// Per-loop tool result cache. Lives on seed so it persists across
	// park/resume; lazy-init here on first run. The mutex guards
	// concurrent access from the parallel batch dispatcher AND the
	// parallel read-range index — both live behind the same lock so a
	// single Lock/Unlock per dispatch covers exact-key + range merge.
	if seed.LoopFileCache == nil {
		seed.LoopFileCache = make(map[string]string)
	}
	if seed.LoopReadRangeIndex == nil {
		seed.LoopReadRangeIndex = make(map[string][]readRangeEntry)
	}

	traces := seed.Traces
	if traces == nil {
		traces = make([]nativeToolTrace, 0, lim.MaxSteps)
	}

	s := &loopRunState{
		seed:         seed,
		msgs:         seed.Messages,
		traces:       traces,
		totalTokens:  seed.TotalTokens,
		lastProvider: seed.LastProvider,
		lastModel:    seed.LastModel,
		question:     seed.Question,
		chunks:       seed.Chunks,
		systemPrompt: seed.SystemPrompt,
		systemBlocks: seed.SystemBlocks,
		descriptors:  seed.Descriptors,
		lim:          lim,
		cacheMu:      &sync.Mutex{},
	}

	// One-shot flags for recovery paths below. These are per-invocation
	// state: synthesizeHintInjected gates the "stop tool-calling" nudge
	// so it doesn't spam; emptyRecoveryTried lets us reprompt the model
	// once when it returns zero tool_calls AND zero text (observed when
	// the model gets confused by a compacted history or a tool failure).
	//
	// Auto-resume re-arm: when this loop instance comes from
	// attemptAutoResume (CumulativeSteps>0), the prior nudge — if any —
	// was compacted away with the rest of the transcript. Setting the
	// flag to false re-arms the nudge so a model that just had its
	// context wiped gets re-anchored ("you've been at this a while,
	// either share results or keep going with intent") instead of
	// drifting on whatever fragments survived the compact.
	synthesizeHintInjected := initialSynthesisFlag(s, lim)
	emptyRecoveryTried := false

	for step := 1; step <= lim.MaxSteps; step++ {
		s.step = step
		// Engine.Shutdown() transitions through StateShuttingDown before
		// closing storage / conversation. Without this guard an in-flight
		// loop can start a new tool round AFTER shutdown begins, racing
		// with bbolt close (panic) and leaving the parked-state save with
		// nowhere to write. Park here so the user can /continue once a
		// fresh engine boots, instead of erroring out mid-round.
		// REPORT.md #9.
		if state := e.State(); state >= StateShuttingDown {
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
			return s.park(e, notice, ParkReasonShuttingDown), nil
		}
		if notes := e.drainAgentNotes(); len(notes) > 0 {
			for _, note := range notes {
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

		if compacted, report := e.maybeCompactNativeLoopHistoryForBudget(s.msgs, s.systemPrompt, s.chunks, lim.MaxTokens); report != nil {
			s.msgs = compacted
			e.publishAgentLoopEvent("context:lifecycle:compacted", map[string]any{
				"step":             step,
				"before_tokens":    report.BeforeTokens,
				"after_tokens":     report.AfterTokens,
				"rounds_collapsed": report.RoundsCollapsed,
				"messages_removed": report.MessagesRemoved,
				"threshold_ratio":  report.ThresholdRatio,
				"keep_recent":      report.KeepRecentRounds,
				"surface":          "native",
			})
		}

		// Proactive step-boundary compaction. Once we're past the soft
		// round cap (15 by default), drop the threshold so old rounds get
		// collapsed before headroom crashes. The reactive compactor above
		// uses 0.7; this one uses 0.5 — fires earlier so a long sustained
		// loop never has to pay an emergency park. No-op when the loop is
		// short or the lifecycle is disabled.
		if step > lim.RoundSoftCap {
			if compacted, report := e.proactiveCompactNativeLoopHistory(s.msgs, s.systemPrompt, s.chunks, lim.MaxTokens); report != nil {
				s.msgs = compacted
				e.publishAgentLoopEvent("context:lifecycle:proactive_compacted", map[string]any{
					"step":             step,
					"before_tokens":    report.BeforeTokens,
					"after_tokens":     report.AfterTokens,
					"rounds_collapsed": report.RoundsCollapsed,
					"messages_removed": report.MessagesRemoved,
					"threshold_ratio":  proactiveCompactRatio,
					"surface":          "native",
				})
			}
		}

		// Pre-flight budget gate (preflightBudget in agent_loop_phases.go).
		// Park-or-recover before we burn another round's tokens.
		if parked := e.preflightBudget(s); parked != nil {
			return *parked, nil
		}

		// Synthesis nudge. After N rounds of tool calls the model often
		// has enough context to answer but keeps reading. One explicit
		// "stop gathering, answer now" message has been observed to
		// break that pattern without the harder intervention below.
		if !synthesizeHintInjected && len(s.traces) >= lim.RoundSoftCap {
			synthesizeHintInjected = true
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
		}

		// Hard cap: after N rounds the model doesn't get to ask for
		// tools anymore. `ToolChoice: "none"` forces the next call to
		// emit plain text. This is the final guardrail before the
		// step cap trips.
		toolChoice := "auto"
		if len(s.traces) >= lim.RoundHardCap {
			toolChoice = "none"
			e.publishAgentLoopEvent("agent:loop:tools_force_stop", map[string]any{
				"step":        step,
				"tool_rounds": len(s.traces),
				"hard_cap":    lim.RoundHardCap,
				"surface":     "native",
			})
		}
		// Stuck-streak force-stop: if Rule 0 has flagged the same
		// failure pattern for stuckStreakHardstopThreshold consecutive
		// rounds AND the model still hasn't switched tactic, the
		// "switch tactic" hint is being ignored. Force tool_choice=
		// "none" so the next response MUST be text — the model is
		// compelled to either explain the blocker or hand back to the
		// user. Without this, an unattended run can burn 30+ rounds
		// retrying read_file with different guesses while the same
		// hint scrolls past every round. Independent of RoundHardCap
		// because it can fire much earlier (3 consecutive stuck rounds
		// vs. 30 total tool rounds).
		if toolChoice == "auto" && s.stuckStreak >= stuckStreakHardstopThreshold {
			toolChoice = "none"
			e.publishAgentLoopEvent("agent:loop:stuck_force_stop", map[string]any{
				"step":         step,
				"stuck_streak": s.stuckStreak,
				"threshold":    stuckStreakHardstopThreshold,
				"surface":      "native",
			})
		}

		e.publishAgentLoopEvent("agent:loop:thinking", map[string]any{
			"step":           step,
			"max_tool_steps": lim.MaxSteps,
			"tool_rounds":    len(s.traces),
			"tokens_used":    s.totalTokens,
			"tool_choice":    toolChoice,
			"surface":        "native",
		})

		reqProvider := strings.TrimSpace(s.lastProvider)
		if reqProvider == "" {
			reqProvider = e.provider()
		}
		reqModel := strings.TrimSpace(s.lastModel)
		if reqModel == "" {
			if selected, ok := e.Providers.Get(reqProvider); ok && selected != nil {
				reqModel = strings.TrimSpace(selected.Model())
			}
		}
		req := provider.CompletionRequest{
			Provider:     reqProvider,
			Model:        reqModel,
			System:       s.systemPrompt,
			SystemBlocks: s.systemBlocks,
			Context:      s.chunks,
			Messages:     s.msgs,
			Tools:        s.descriptors,
			ToolChoice:   toolChoice,
		}

		resp, usedProvider, err := e.Providers.Complete(ctx, req)
		if err != nil {
			// Ctx cancellation mid-round (user interrupt, parent timeout)
			// would otherwise discard every trace + msg the loop has built.
			// Park instead so /continue can pick up where we left off.
			if ctxErr := ctx.Err(); ctxErr != nil && len(s.traces) > 0 {
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
				return s.park(e, notice, ParkReasonInterrupted), nil
			}
			e.publishAgentLoopEvent("agent:loop:error", map[string]any{
				"step":  step,
				"error": err.Error(),
			})
			return nativeToolCompletion{}, err
		}
		// totalTokens is the rolling conversation footprint as seen by
		// the provider, NOT a cumulative sum across rounds. Summing
		// per-round Usage.TotalTokens double-counted the growing history
		// every iteration — a modest 20-round loop that never left a 25k
		// working set would trip a 250k "budget" purely from re-counting
		// the same prompt tokens. Replace with the latest
		// InputTokens+OutputTokens so the metric tracks real footprint
		// and correctly shrinks after auto-compact trims the history.
		if footprint := resp.Usage.InputTokens + resp.Usage.OutputTokens; footprint > 0 {
			s.totalTokens = footprint
		} else if resp.Usage.TotalTokens > 0 {
			// Provider only reported the aggregate (some OpenAI-compatible
			// endpoints drop the per-direction split). Best-effort
			// footprint: treat TotalTokens as the request size.
			s.totalTokens = resp.Usage.TotalTokens
		}
		if strings.TrimSpace(usedProvider) != "" {
			s.lastProvider = usedProvider
		}
		if strings.TrimSpace(resp.Model) != "" {
			s.lastModel = resp.Model
		}

		// Empty turn: zero tool_calls AND zero visible text. Delegate to
		// handleEmptyTurn which handles the first-time nudge vs the
		// second-time visible-failure case (in agent_loop_phases.go).
		if len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Text) == "" {
			var emptyOut *nativeToolCompletion
			emptyRecoveryTried, emptyOut = e.handleEmptyTurn(s, resp, emptyRecoveryTried)
			if emptyOut != nil {
				return *emptyOut, nil
			}
			continue
		}

		// No tool calls → final answer.
		if len(resp.ToolCalls) == 0 {
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
				"max_tool_steps": lim.MaxSteps,
				"tool_rounds":    len(s.traces),
				"tokens_used":    s.totalTokens,
				"provider":       s.lastProvider,
				"model":          s.lastModel,
				"surface":        "native",
			})
			e.publishProviderComplete(s.lastProvider, s.lastModel, s.totalTokens, resp.Usage)
			e.emitCoachNotes(s.question, completion)
			return completion, nil
		}

		// Run the round's tool calls (executeAndAppendToolBatch in
		// agent_loop_phases.go), then layer trajectory-aware coach
		// hints over the result before the next provider round.
		freshStart := e.executeAndAppendToolBatch(ctx, s, resp)
		e.injectTrajectoryHints(s, freshStart)

		// Post-step budget gate (postStepBudget in agent_loop_phases.go).
		if parked := e.postStepBudget(s); parked != nil {
			return *parked, nil
		}

		if step == lim.MaxSteps {
			headline := fmt.Sprintf(
				"Parked at step %d — hit the configured ceiling (%d tool rounds, ~%d tokens).",
				step, len(s.traces), s.totalTokens,
			)
			notice := composeParkedNotice(headline, s.traces,
				`Type /continue to resume — add a note to redirect (e.g. "/continue focus on the test file").`)
			return s.park(e, notice, ParkReasonStepCap), nil
		}
	}

	return nativeToolCompletion{}, fmt.Errorf("agent tool loop ended unexpectedly")
}
