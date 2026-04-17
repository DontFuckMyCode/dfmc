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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// parkPhase distinguishes the two contexts in which the loop reaches
// the budget-exhausted park notice. "before" runs in the preflight
// gate — we know we WOULD overflow if we ran the next round, so the
// notice mentions the headroom we needed but didn't have. "after"
// runs once we've already executed and seen the totalTokens go past
// MaxTokens; headroom is moot at that point.
type parkPhase int

const (
	parkPhaseBefore parkPhase = iota
	parkPhaseAfter
)

// formatBudgetExhaustedNotice renders the "Agent loop parked … tool
// budget exhausted" message that the engine emits when the native
// loop can't run another tool round without busting MaxTokens. The
// same string template appeared in 4 places (preflight headroom-fail
// happy path, preflight headroom-fail catch-all, post-step compact-
// failed path, post-step no-compact path) — fixing the wording in
// one without the others was a regression magnet flagged in the
// REPORT.md walk. headroom is ignored when phase == parkPhaseAfter.
func formatBudgetExhaustedNotice(phase parkPhase, step, tokens, maxTokens, headroom, rounds int) string {
	suffix := "Type /continue to resume with fresh headroom, or add a note to narrow focus — e.g. /continue just finish the test file."
	switch phase {
	case parkPhaseBefore:
		return fmt.Sprintf(
			"Agent loop parked before step %d — tool budget exhausted (~%d/%d tokens, need ~%d headroom, %d rounds). %s",
			step, tokens, maxTokens, headroom, rounds, suffix,
		)
	default:
		return fmt.Sprintf(
			"Agent loop parked after step %d — tool budget exhausted (~%d/%d tokens, %d rounds). %s",
			step, tokens, maxTokens, rounds, suffix,
		)
	}
}

// Defaults used when agent config is unset *and* the provider exposes no
// context window. They act as a safety floor — the real runtime budget is
// elastic and scales with `provider.MaxContext()` so a 1M-token window gets
// a commensurately bigger tool budget instead of being throttled to 120k.
const (
	defaultMaxNativeToolSteps       = 25
	defaultMaxNativeToolTokens      = 120000
	defaultMaxNativeToolResultChars = 3200
	defaultMaxNativeToolDataChars   = 1200

	// elasticToolTokensRatio gives the tool loop 60% of the provider's
	// context window. The other 40% covers base prompt, context chunks,
	// response reserve, and scrollback headroom.
	elasticToolTokensRatio = 0.60
	// elasticToolResultCharsRatio caps an *individual* tool payload at
	// ~2.5% of the window. A single read_file can't swamp the round.
	elasticToolResultCharsRatio = 1.0 / 40.0
	// elasticToolDataCharsRatio caps the JSON sidecar tighter — data
	// payloads are usually duplicative of the text output.
	elasticToolDataCharsRatio = 1.0 / 100.0

	// toolRoundSoftCap is the round count at which the loop injects a
	// single synthesis nudge: "you have enough context, answer now."
	// Tuned below the default step cap so a model that's stuck in a
	// read-read-read loop gets one firm redirect before the hard cap
	// takes away tool_use entirely. Real-world answers rarely need
	// more than 5 rounds of gathering.
	toolRoundSoftCap = 5
	// toolRoundHardCap flips ToolChoice to "none" for every subsequent
	// call, so the provider MUST emit natural-language text. This is
	// what saves us from the observed 7-round pathology where the
	// model keeps grepping past the budget. Spaced two rounds above
	// the soft cap so the nudge has room to land.
	toolRoundHardCap = 7
	// budgetHeadroomDivisor reserves ~14% of MaxTokens as a safety
	// margin before each round starts. Without it, the post-round
	// gate can only detect exhaustion AFTER the round has consumed
	// its tokens — a 40k round on top of 95k lands at 135k/120k and
	// the cost is already burned. 1/7 is cheap, empirical, and
	// prevents the overshoot without starving small budgets.
	budgetHeadroomDivisor = 7
	// toolResultDedupStubBytes is the threshold below which a prior
	// tool_result message is considered already-trimmed and we don't
	// bother replacing it with the dedup stub. Anything above this
	// (a real, full payload) gets replaced with a one-liner.
	toolResultDedupStubBytes = 160
)

// agentLimits is the resolved runtime budget for a single agent loop.
type agentLimits struct {
	MaxSteps       int
	MaxTokens      int
	MaxResultChars int
	MaxDataChars   int
}

// agentLimits resolves the runtime budget. Rule: cfg.Agent values are a
// *floor*, not a cap. When the active provider exposes a context window we
// scale each limit up proportionally — so capable models aren't strangled
// by defaults meant for 128k windows. Cfg=0 means "fully elastic".
func (e *Engine) agentLimits() agentLimits {
	lim := agentLimits{
		MaxSteps:       defaultMaxNativeToolSteps,
		MaxTokens:      defaultMaxNativeToolTokens,
		MaxResultChars: defaultMaxNativeToolResultChars,
		MaxDataChars:   defaultMaxNativeToolDataChars,
	}
	if e == nil || e.Config == nil {
		return lim
	}
	cfg := e.Config.Agent
	if cfg.MaxToolSteps > 0 {
		lim.MaxSteps = cfg.MaxToolSteps
	}
	if cfg.MaxToolTokens > 0 {
		lim.MaxTokens = cfg.MaxToolTokens
	}
	if cfg.MaxToolResultChars > 0 {
		lim.MaxResultChars = cfg.MaxToolResultChars
	}
	if cfg.MaxToolResultDataChars > 0 {
		lim.MaxDataChars = cfg.MaxToolResultDataChars
	}

	window := e.providerMaxContext()
	if window <= 0 {
		return lim
	}

	if scaled := int(float64(window) * elasticToolTokensRatio); scaled > lim.MaxTokens {
		lim.MaxTokens = scaled
	}
	if scaled := int(float64(window) * elasticToolResultCharsRatio); scaled > lim.MaxResultChars {
		lim.MaxResultChars = scaled
	}
	if scaled := int(float64(window) * elasticToolDataCharsRatio); scaled > lim.MaxDataChars {
		lim.MaxDataChars = scaled
	}
	return lim
}

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
	// (coach, TUI) can tailor their copy. Values: "step_cap" or
	// "budget_exhausted". Empty when Parked is false.
	ParkedReason string
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

	chunks := e.buildContextChunks(question)
	systemPrompt, systemBlocks := e.buildNativeToolSystemPromptBundle(question, chunks)
	descriptors := metaSpecsToDescriptors(e.Tools.MetaSpecs())

	contextTokens := 0
	for _, c := range chunks {
		contextTokens += c.TokenCount
	}

	seed := &parkedAgentState{
		Question:      question,
		Messages:      e.buildToolLoopRequestMessages(question, chunks, systemPrompt, nil),
		Traces:        nil,
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
	lim := e.agentLimits()
	e.publishAgentLoopEvent("agent:loop:start", map[string]any{
		"provider":        seed.LastProvider,
		"model":           seed.LastModel,
		"max_tool_steps":  lim.MaxSteps,
		"max_tool_tokens": lim.MaxTokens,
		"surface":         "native",
		"context_files":   len(chunks),
		"context_tokens":  contextTokens,
		"meta_tools":      metaToolNames(descriptors),
	})
	return e.runNativeToolLoop(ctx, seed, lim)
}

// resumeMaxMultiplier is the outer ceiling on cumulative agent work
// across every /continue of a single root ask. Each resume normally
// gets a fresh MaxSteps budget (so /continue actually progresses
// instead of instantly re-parking), but unbounded resumes let a
// model that keeps parking burn tokens forever. A multiplier of 3
// lets the user /continue twice past the initial run for a total of
// 3 x MaxSteps before the engine refuses further resumes. Tune via
// this constant, not per-call — the semantic is "how many agent
// budgets is one user question worth, at most."
const resumeMaxMultiplier = 3

// ResumeAgent re-enters the native tool loop from a previously parked state.
// An optional note is appended as a user message before the next round-trip,
// e.g. /continue focus on the auth tests. Returns an error when there is no
// parked loop, or when the cumulative work ceiling has been hit.
func (e *Engine) ResumeAgent(ctx context.Context, note string) (nativeToolCompletion, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	seed := e.takeParkedAgent()
	if seed == nil {
		return nativeToolCompletion{}, fmt.Errorf("no parked agent loop to resume")
	}
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		seed.Messages = append(seed.Messages, provider.Message{
			Role:    types.RoleUser,
			Content: trimmed,
		})
	}
	lim := e.agentLimits()

	// Accumulate cumulative counters from the just-parked run BEFORE we
	// zero out the per-attempt values below. Without this the outer
	// ceiling never engages: every resume starts fresh and the model
	// can park indefinitely.
	seed.CumulativeSteps += seed.Step
	seed.CumulativeTokens += seed.TotalTokens

	// Hard ceiling — refuse further resumes when the model has already
	// burned resumeMaxMultiplier full budgets. We re-park so the user
	// can still inspect the state, but we won't run another round.
	stepCeiling := lim.MaxSteps * resumeMaxMultiplier
	tokenCeiling := lim.MaxTokens * resumeMaxMultiplier
	if lim.MaxSteps > 0 && seed.CumulativeSteps >= stepCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:resume_refused", map[string]any{
			"reason":            "cumulative_steps_ceiling",
			"cumulative_steps":  seed.CumulativeSteps,
			"ceiling":           stepCeiling,
			"max_steps_per_run": lim.MaxSteps,
			"surface":           "native",
		})
		return nativeToolCompletion{}, fmt.Errorf(
			"resume refused: cumulative agent steps %d hit ceiling %d (%d x MaxSteps=%d). The model has already had %d full budgets on this question — start a new ask with refined scope instead of continuing",
			seed.CumulativeSteps, stepCeiling, resumeMaxMultiplier, lim.MaxSteps, resumeMaxMultiplier)
	}
	if lim.MaxTokens > 0 && seed.CumulativeTokens >= tokenCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:resume_refused", map[string]any{
			"reason":             "cumulative_tokens_ceiling",
			"cumulative_tokens":  seed.CumulativeTokens,
			"ceiling":            tokenCeiling,
			"max_tokens_per_run": lim.MaxTokens,
			"surface":            "native",
		})
		return nativeToolCompletion{}, fmt.Errorf(
			"resume refused: cumulative agent tokens %d hit ceiling %d (%d x MaxTokens=%d). The model has already spent %d full token budgets on this question — start a new ask",
			seed.CumulativeTokens, tokenCeiling, resumeMaxMultiplier, lim.MaxTokens, resumeMaxMultiplier)
	}

	// Force-compact the parked history before the next provider round-trip.
	// Without this, resume ships the full fat tool_result transcript back to
	// the provider, which balloons Usage.TotalTokens past the budget on step
	// 1 and re-parks us in a cycle.
	priorTokens := seed.TotalTokens
	if compacted, report := e.forceCompactNativeLoopHistory(seed.Messages, seed.SystemPrompt, seed.Chunks); report != nil {
		seed.Messages = compacted
		e.publishAgentLoopEvent("context:lifecycle:compacted", map[string]any{
			"step":             0,
			"before_tokens":    report.BeforeTokens,
			"after_tokens":     report.AfterTokens,
			"rounds_collapsed": report.RoundsCollapsed,
			"messages_removed": report.MessagesRemoved,
			"threshold_ratio":  report.ThresholdRatio,
			"keep_recent":      report.KeepRecentRounds,
			"surface":          "native",
			"phase":            "resume",
		})
	}

	e.publishAgentLoopEvent("agent:loop:resume", map[string]any{
		"resumed_from_step": seed.Step,
		"max_tool_steps":    lim.MaxSteps,
		"tool_rounds":       len(seed.Traces),
		"tokens_used":       priorTokens,
		"cumulative_steps":  seed.CumulativeSteps,
		"cumulative_tokens": seed.CumulativeTokens,
		"step_ceiling":      stepCeiling,
		"token_ceiling":     tokenCeiling,
		"surface":           "native",
	})
	// Give the resumed loop a fresh cap of MaxSteps *additional* iterations
	// on top of whatever it already consumed. That's what the user typed
	// /continue for — they want more work, not another instant park. Reset
	// TotalTokens too: the budget meters per resume attempt, not cumulative
	// — otherwise every /continue trips MaxTokens on step 1. The outer
	// ceiling above still bounds the total.
	seed.Step = 0
	seed.TotalTokens = 0
	return e.runNativeToolLoop(ctx, seed, lim)
}

// maxBudgetAutoRecoveries caps how many times a single agent-loop invocation
// will auto-compact + reset tokens on budget_exhausted before giving up and
// parking. One is usually enough: Fix A's force-compact on resume already
// handles the bulk of the bloat; this is the safety net for runs that keep
// growing mid-loop. Higher values risk infinite compact→fill→compact cycles
// when the model's asks inherently generate more data than fits.
const maxBudgetAutoRecoveries = 1

func (e *Engine) runNativeToolLoop(ctx context.Context, seed *parkedAgentState, lim agentLimits) (nativeToolCompletion, error) {
	msgs := seed.Messages
	traces := seed.Traces
	if traces == nil {
		traces = make([]nativeToolTrace, 0, lim.MaxSteps)
	}
	totalTokens := seed.TotalTokens
	chunks := seed.Chunks
	systemPrompt := seed.SystemPrompt
	systemBlocks := seed.SystemBlocks
	descriptors := seed.Descriptors
	lastProvider := seed.LastProvider
	lastModel := seed.LastModel
	question := seed.Question
	autoRecoveries := 0
	// One-shot flags for recovery paths below. These are per-invocation
	// state: synthesizeHintInjected gates the "stop tool-calling" nudge
	// so it doesn't spam; emptyRecoveryTried lets us reprompt the model
	// once when it returns zero tool_calls AND zero text (observed when
	// the model gets confused by a compacted history or a tool failure).
	synthesizeHintInjected := len(traces) >= toolRoundSoftCap
	emptyRecoveryTried := false

	for step := 1; step <= lim.MaxSteps; step++ {
		if notes := e.drainAgentNotes(); len(notes) > 0 {
			for _, note := range notes {
				msgs = append(msgs, provider.Message{
					Role:    types.RoleUser,
					Content: "[user btw] " + note,
				})
				e.publishAgentLoopEvent("agent:note:injected", map[string]any{
					"step": step,
					"note": note,
				})
			}
		}

		if compacted, report := e.maybeCompactNativeLoopHistoryForBudget(msgs, systemPrompt, chunks, lim.MaxTokens); report != nil {
			msgs = compacted
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

		// Pre-flight budget gate. The existing post-round gate at the
		// bottom catches consumption after the fact — if a round takes
		// 40k on top of 95k we only notice at 135k/120k, and the cost
		// is already burned. Reserve ~14% of MaxTokens as headroom for
		// the round we're about to start; when we can't fit, auto-
		// compact once and retry, else park cleanly.
		if lim.MaxTokens > 0 {
			headroom := lim.MaxTokens / budgetHeadroomDivisor
			if totalTokens+headroom >= lim.MaxTokens {
				if autoRecoveries < maxBudgetAutoRecoveries {
					if compacted, report := e.forceCompactNativeLoopHistory(msgs, systemPrompt, chunks); report != nil && report.MessagesRemoved > 0 {
						msgs = compacted
						before := totalTokens
						totalTokens = 0
						autoRecoveries++
						e.publishAgentLoopEvent("agent:loop:auto_recover", map[string]any{
							"step":             step,
							"attempt":          autoRecoveries,
							"max_attempts":     maxBudgetAutoRecoveries,
							"tokens_before":    before,
							"rounds_collapsed": report.RoundsCollapsed,
							"messages_removed": report.MessagesRemoved,
							"reason":           "budget_headroom_preflight",
							"surface":          "native",
						})
						// Fall through — the next iteration re-checks the gate
						// with the zeroed budget and runs the round normally.
					} else {
						notice := formatBudgetExhaustedNotice(parkPhaseBefore, step, totalTokens, lim.MaxTokens, headroom, len(traces))
						return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, "budget_exhausted"), nil
					}
				} else {
					notice := formatBudgetExhaustedNotice(parkPhaseBefore, step, totalTokens, lim.MaxTokens, headroom, len(traces))
					return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, "budget_exhausted"), nil
				}
			}
		}

		// Synthesis nudge. After N rounds of tool calls the model often
		// has enough context to answer but keeps reading. One explicit
		// "stop gathering, answer now" message has been observed to
		// break that pattern without the harder intervention below.
		if !synthesizeHintInjected && len(traces) >= toolRoundSoftCap {
			synthesizeHintInjected = true
			msgs = append(msgs, provider.Message{
				Role: types.RoleUser,
				Content: fmt.Sprintf(
					"[system] You have completed %d rounds of tool calls and gathered substantial context. "+
						"Please synthesize a natural-language answer now based on what you've learned. "+
						"Only make further tool calls if strictly necessary — otherwise respond with your findings.",
					len(traces),
				),
			})
			e.publishAgentLoopEvent("agent:loop:synthesize_hint", map[string]any{
				"step":        step,
				"tool_rounds": len(traces),
				"surface":     "native",
			})
		}

		// Hard cap: after N rounds the model doesn't get to ask for
		// tools anymore. `ToolChoice: "none"` forces the next call to
		// emit plain text. This is the final guardrail before the
		// step cap trips.
		toolChoice := "auto"
		if len(traces) >= toolRoundHardCap {
			toolChoice = "none"
			e.publishAgentLoopEvent("agent:loop:tools_force_stop", map[string]any{
				"step":        step,
				"tool_rounds": len(traces),
				"hard_cap":    toolRoundHardCap,
				"surface":     "native",
			})
		}

		e.publishAgentLoopEvent("agent:loop:thinking", map[string]any{
			"step":           step,
			"max_tool_steps": lim.MaxSteps,
			"tool_rounds":    len(traces),
			"tokens_used":    totalTokens,
			"tool_choice":    toolChoice,
			"surface":        "native",
		})

		req := provider.CompletionRequest{
			Provider:     e.provider(),
			Model:        e.model(),
			System:       systemPrompt,
			SystemBlocks: systemBlocks,
			Context:      chunks,
			Messages:     msgs,
			Tools:        descriptors,
			ToolChoice:   toolChoice,
		}

		resp, usedProvider, err := e.Providers.Complete(ctx, req)
		if err != nil {
			e.publishAgentLoopEvent("agent:loop:error", map[string]any{
				"step":  step,
				"error": err.Error(),
			})
			return nativeToolCompletion{}, err
		}
		totalTokens += resp.Usage.TotalTokens
		if strings.TrimSpace(usedProvider) != "" {
			lastProvider = usedProvider
		}
		if strings.TrimSpace(resp.Model) != "" {
			lastModel = resp.Model
		}

		// Empty turn: zero tool_calls AND zero visible text. Usually a
		// symptom of model confusion after a tool failure or an
		// over-aggressive compact. The old behaviour was to treat this
		// as a final answer (Answer="") and return silently — the user
		// saw an empty assistant bubble and no explanation. Retry once
		// with an explicit synthesis nudge; if it happens a second time
		// we surface an honest failure message instead of a ghost.
		if len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Text) == "" {
			if !emptyRecoveryTried {
				emptyRecoveryTried = true
				msgs = append(msgs, provider.Message{
					Role:      types.RoleAssistant,
					Content:   resp.Text,
					ToolCalls: resp.ToolCalls,
				})
				msgs = append(msgs, provider.Message{
					Role: types.RoleUser,
					Content: "[system] Your previous response was empty. Please provide a natural-language answer to the original question based on the context you've gathered. " +
						"If you genuinely cannot answer, say so explicitly — do not return an empty response.",
				})
				e.publishAgentLoopEvent("agent:loop:empty_recovery", map[string]any{
					"step":        step,
					"tool_rounds": len(traces),
					"tokens_used": totalTokens,
					"surface":     "native",
				})
				continue
			}
			// Second empty turn — give up with a visible notice instead
			// of returning a blank bubble.
			completion := nativeToolCompletion{
				Answer: "The model returned an empty response twice in a row even after an explicit synthesis nudge. " +
					"Try rephrasing the question or `/continue` with a narrower scope.",
				Provider:     lastProvider,
				Model:        lastModel,
				TokenCount:   totalTokens,
				Context:      chunks,
				ToolTraces:   traces,
				SystemPrompt: systemPrompt,
			}
			e.recordNativeAgentInteraction(question, completion)
			e.publishAgentLoopEvent("agent:loop:empty_final", map[string]any{
				"step":        step,
				"tool_rounds": len(traces),
				"tokens_used": totalTokens,
				"surface":     "native",
			})
			return completion, nil
		}

		// No tool calls → final answer.
		if len(resp.ToolCalls) == 0 {
			completion := nativeToolCompletion{
				Answer:       resp.Text,
				Provider:     lastProvider,
				Model:        lastModel,
				TokenCount:   totalTokens,
				Context:      chunks,
				ToolTraces:   traces,
				SystemPrompt: systemPrompt,
			}
			e.recordNativeAgentInteraction(question, completion)
			e.publishAgentLoopEvent("agent:loop:final", map[string]any{
				"step":           step,
				"max_tool_steps": lim.MaxSteps,
				"tool_rounds":    len(traces),
				"tokens_used":    totalTokens,
				"provider":       lastProvider,
				"model":          lastModel,
				"surface":        "native",
			})
			e.publishProviderComplete(lastProvider, lastModel, totalTokens)
			e.emitCoachNotes(question, completion)
			return completion, nil
		}

		// Append the assistant turn (text + tool_calls) so the model sees its
		// own previous step on the next round.
		msgs = append(msgs, provider.Message{
			Role:      types.RoleAssistant,
			Content:   resp.Text,
			ToolCalls: resp.ToolCalls,
		})

		freshStart := len(traces)

		// Publish call events up-front in issue order so the TUI / activity
		// log reflects what the model asked for, even if we're about to fan
		// the calls out across goroutines.
		stepTraces := make([]nativeToolTrace, len(resp.ToolCalls))
		for i, call := range resp.ToolCalls {
			stepTraces[i] = nativeToolTrace{
				Call:       call,
				Provider:   lastProvider,
				Model:      lastModel,
				Step:       step,
				OccurredAt: time.Now(),
			}
			e.publishNativeToolCall(stepTraces[i])
		}

		// Parallelize when every call in the batch targets a read-only
		// tool. Mixed batches (reads + writes / reads + run_command) stay
		// sequential so ordering guarantees hold — run_command's
		// workspace-changed snapshot alone makes interleaved execution
		// unsafe.
		batchSize := 1
		if allParallelSafe(resp.ToolCalls) {
			batchSize = e.parallelBatchSize()
		}
		results := e.executeToolCallsParallel(ctx, resp.ToolCalls, batchSize)

		// When we're already deep in the budget, halve the per-tool
		// char caps so new round results don't accelerate the bloat.
		// This kicks in once we've consumed 50% of MaxTokens — the
		// point where previous rounds are already carrying substantial
		// weight into every subsequent request.
		effectiveMaxResult := lim.MaxResultChars
		effectiveMaxData := lim.MaxDataChars
		if lim.MaxTokens > 0 && totalTokens*2 >= lim.MaxTokens {
			if effectiveMaxResult > 0 {
				effectiveMaxResult /= 2
			}
			if effectiveMaxData > 0 {
				effectiveMaxData /= 2
			}
		}

		// Rejoin results in original call order for message append /
		// trace accumulation. Doing this in a second pass (after
		// executeToolCallsParallel returns in-order) keeps the message
		// sequence the provider sees identical to the sequential path.
		for i, call := range resp.ToolCalls {
			r := results[i]
			trace := stepTraces[i]
			if r.Err != nil {
				trace.Err = r.Err.Error()
			} else {
				trace.Result = r.Result
			}

			content, isErr := formatNativeToolResultPayloadWithLimits(r.Result, r.Err, effectiveMaxResult, effectiveMaxData)
			// Publish after formatting so we can attach RTK compression
			// stats (raw→payload bytes) to the event for the TUI stats
			// panel. Publication order matches issue order.
			e.publishNativeToolResultWithPayload(trace, content)
			traces = append(traces, trace)

			// Cross-round dedup: if the model already invoked this exact
			// (name, input) combination and we kept the full payload in
			// history, replace the older result with a one-line stub.
			// The model always has the most-recent read in the live
			// round, so the older one just bloats context. We never
			// remove the message — ToolCallID chains must stay intact
			// for provider APIs — only shrink its Content.
			if prev := findPriorIdenticalToolResult(msgs, call, call.ID); prev >= 0 {
				if len(msgs[prev].Content) > toolResultDedupStubBytes {
					msgs[prev].Content = fmt.Sprintf(
						"[deduped — identical %s call appears again in a later round; see that result for the current payload]",
						call.Name,
					)
				}
			}

			msgs = append(msgs, provider.Message{
				Role:       types.RoleUser,
				Content:    content,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				ToolError:  isErr,
			})
		}

		// Dynamic prompt composition: derive trajectory-aware coach hints
		// from the round we just finished and inject them as a system-tagged
		// user note before the next provider round-trip. Rules live in
		// internal/context/trajectory.go; de-dup is tracked on the parked
		// state so the same hint doesn't fire twice in one run.
		if hints := buildTrajectoryHints(traces[freshStart:], traces, seed.RecentCoachHints); len(hints) > 0 {
			if block := ctxmgr.FormatTrajectoryHints(hints); block != "" {
				msgs = append(msgs, provider.Message{
					Role:    types.RoleUser,
					Content: block,
				})
				seed.RecentCoachHints = appendRecentHints(seed.RecentCoachHints, hints)
				e.publishAgentLoopEvent("agent:coach:hint", map[string]any{
					"step":  step,
					"hints": hints,
				})
			}
		}

		if lim.MaxTokens > 0 && totalTokens >= lim.MaxTokens {
			// Before parking, try auto-recovery: force-compact the running
			// history and reset the per-run token counter so the next
			// iteration gets fresh headroom. Only attempt this a fixed
			// number of times to avoid infinite compact→fill cycles when
			// the work inherently exceeds one budget.
			if autoRecoveries < maxBudgetAutoRecoveries {
				if compacted, report := e.forceCompactNativeLoopHistory(msgs, systemPrompt, chunks); report != nil && report.MessagesRemoved > 0 {
					msgs = compacted
					before := totalTokens
					totalTokens = 0
					autoRecoveries++
					e.publishAgentLoopEvent("agent:loop:auto_recover", map[string]any{
						"step":             step,
						"attempt":          autoRecoveries,
						"max_attempts":     maxBudgetAutoRecoveries,
						"tokens_before":    before,
						"rounds_collapsed": report.RoundsCollapsed,
						"messages_removed": report.MessagesRemoved,
						"reason":           "budget_exhausted",
						"surface":          "native",
					})
					// Fall through to the step-cap check. If we're not at
					// MaxSteps yet, the loop iterates with the slimmed
					// history and a zeroed budget.
				} else {
					// Nothing to compact — park. Same behaviour as before.
					notice := formatBudgetExhaustedNotice(parkPhaseAfter, step, totalTokens, lim.MaxTokens, 0, len(traces))
					return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, "budget_exhausted"), nil
				}
			} else {
				notice := formatBudgetExhaustedNotice(parkPhaseAfter, step, totalTokens, lim.MaxTokens, 0, len(traces))
				return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, "budget_exhausted"), nil
			}
		}

		if step == lim.MaxSteps {
			notice := fmt.Sprintf(
				"Agent loop parked at step %d (%d tool rounds, ~%d tokens). "+
					"Type /continue to resume, optionally with a note — e.g. /continue focus on the test file.",
				step, len(traces), totalTokens,
			)
			return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, "step_cap"), nil
		}
	}

	return nativeToolCompletion{}, fmt.Errorf("agent tool loop ended unexpectedly")
}

// parkNativeToolLoop freezes the running loop state under a `parkedAgentState`,
// publishes the `agent:loop:parked` event (plus `agent:loop:budget_exhausted`
// when the token budget tripped), and returns a `nativeToolCompletion` whose
// Answer is a friendly notice telling the user to /continue. The helper is
// shared by the two exit paths that used to inline this logic — MaxSteps cap
// and MaxTokens budget — so they behave identically from the UI's point of
// view. The only difference is `reason`, which lands in both events so the
// TUI can distinguish "worked until step cap" from "ran out of headroom".
func (e *Engine) parkNativeToolLoop(
	question string,
	seed *parkedAgentState,
	msgs []provider.Message,
	traces []nativeToolTrace,
	chunks []types.ContextChunk,
	systemPrompt string,
	systemBlocks []provider.SystemBlock,
	descriptors []provider.ToolDescriptor,
	lastProvider, lastModel string,
	totalTokens, step int,
	notice, reason string,
) nativeToolCompletion {
	lim := e.agentLimits()
	parked := &parkedAgentState{
		Question:         question,
		Messages:         msgs,
		Traces:           traces,
		Chunks:           chunks,
		SystemPrompt:     systemPrompt,
		SystemBlocks:     systemBlocks,
		Descriptors:      descriptors,
		ContextTokens:    seed.ContextTokens,
		TotalTokens:      totalTokens,
		Step:             step,
		LastProvider:     lastProvider,
		LastModel:        lastModel,
		RecentCoachHints: seed.RecentCoachHints,
		// Carry cumulative counters forward across park→resume→park
		// cycles. Without this the ceiling in ResumeAgent never trips:
		// each park rebuilds a fresh parkedAgentState and wipes the
		// cumulative totals that track total work done on this ask.
		CumulativeSteps:  seed.CumulativeSteps,
		CumulativeTokens: seed.CumulativeTokens,
	}
	e.saveParkedAgent(parked)

	if reason == "budget_exhausted" {
		// Keep the dedicated telemetry event firing so operators grepping
		// for budget trips still see them. The old behavior returned an
		// error here; parking is strictly more graceful but shouldn't cost
		// us the observability.
		e.publishAgentLoopEvent("agent:loop:budget_exhausted", map[string]any{
			"step":            step,
			"max_tool_steps":  lim.MaxSteps,
			"max_tool_tokens": lim.MaxTokens,
			"tokens_used":     totalTokens,
			"tool_rounds":     len(traces),
			"surface":         "native",
		})
	}
	e.publishAgentLoopEvent("agent:loop:parked", map[string]any{
		"step":            step,
		"max_tool_steps":  lim.MaxSteps,
		"max_tool_tokens": lim.MaxTokens,
		"tool_rounds":     len(traces),
		"tokens_used":     totalTokens,
		"reason":          reason,
		"surface":         "native",
	})
	return nativeToolCompletion{
		Answer:       notice,
		Provider:     lastProvider,
		Model:        lastModel,
		TokenCount:   totalTokens,
		Context:      chunks,
		ToolTraces:   traces,
		SystemPrompt: systemPrompt,
		Parked:       true,
		ParkedAtStep: step,
		ParkedReason: reason,
	}
}

// buildNativeToolSystemPromptBundle composes the standard system prompt
// (project brief, task style, tool-call policy) and folds the short
// native-mode tool surface block into the *stable* prefix so the whole
// instruction stack (≈40 extra tokens) is eligible for Anthropic prompt
// caching alongside the rest of the base template. Returns both the flat
// text (for providers that ignore caching) and the structured SystemBlocks.
func (e *Engine) buildNativeToolSystemPromptBundle(question string, chunks []types.ContextChunk) (string, []provider.SystemBlock) {
	var bundle *promptlib.PromptBundle
	if e.Context != nil {
		bundle = e.Context.BuildSystemPromptBundle(
			e.ProjectRoot,
			question,
			chunks,
			e.ListTools(),
			e.promptRuntime(),
		)
	}
	bridge := strings.TrimSpace(buildNativeMetaToolInstructions(e.Tools.BackendSpecs()))
	if bridge == "" {
		return bundleToSystemBlocks(bundle)
	}
	bridgeText := "[DFMC native tool surface]\n" + bridge

	if bundle == nil || len(bundle.Sections) == 0 {
		composed := &promptlib.PromptBundle{Sections: []promptlib.PromptSection{
			{Label: "stable", Text: bridgeText, Cacheable: true},
		}}
		return bundleToSystemBlocks(composed)
	}

	sections := make([]promptlib.PromptSection, 0, len(bundle.Sections)+1)
	injected := false
	for _, s := range bundle.Sections {
		if !injected && s.Cacheable {
			s.Text = strings.TrimSpace(s.Text) + "\n\n" + bridgeText
			injected = true
		}
		sections = append(sections, s)
	}
	if !injected {
		// No cacheable section exists yet (template lacks the cache-break
		// marker). Prepend a stable section so the tool surface stays
		// cacheable on its own and the base prompt remains dynamic until a
		// template author adds a boundary.
		sections = append([]promptlib.PromptSection{
			{Label: "stable", Text: bridgeText, Cacheable: true},
		}, sections...)
	}
	return bundleToSystemBlocks(&promptlib.PromptBundle{Sections: sections})
}

func buildNativeMetaToolInstructions(backend []tools.ToolSpec) string {
	lines := []string{
		"You have 4 meta tools that proxy to a richer backend registry:",
		"  - tool_search(query, limit?) — discover backend tools by topic",
		"  - tool_help(name)            — fetch full schema/usage for one tool",
		"  - tool_call(name, args)      — execute a single backend tool",
		"  - tool_batch_call(calls[])   — execute several backend tools in one round-trip",
		"Discover before invoking. Cite evidence by file/line. Never dump raw tool output to the user.",
	}
	if len(backend) > 0 {
		preview := backend
		if len(preview) > 6 {
			preview = preview[:6]
		}
		names := make([]string, 0, len(preview))
		for _, s := range preview {
			names = append(names, s.Name)
		}
		hint := "Backend registry includes: " + strings.Join(names, ", ")
		if len(backend) > len(preview) {
			hint += fmt.Sprintf(" (+%d more — use tool_search to discover)", len(backend)-len(preview))
		}
		lines = append(lines, hint)
	}
	return strings.Join(lines, "\n")
}

// metaSpecsToDescriptors converts ToolSpecs into the provider-agnostic
// ToolDescriptor shape that providers serialize into Anthropic's tools[] or
// OpenAI's tools[].function entries.
func metaSpecsToDescriptors(specs []tools.ToolSpec) []provider.ToolDescriptor {
	out := make([]provider.ToolDescriptor, 0, len(specs))
	for _, s := range specs {
		out = append(out, provider.ToolDescriptor{
			Name:        s.Name,
			Description: s.Summary,
			InputSchema: s.JSONSchema(),
		})
	}
	return out
}

func metaToolNames(descs []provider.ToolDescriptor) []string {
	names := make([]string, 0, len(descs))
	for _, d := range descs {
		names = append(names, d.Name)
	}
	return names
}

// findPriorIdenticalToolResult walks msgs backward looking for a
// user-role tool_result whose originating assistant call had the
// same tool name AND the same input payload as `current`. Returns
// the index in msgs (or -1 when no duplicate exists). Identity is
// decided by the tool name plus a canonical JSON encoding of the
// input — models sometimes reorder map keys, so string-compare on
// the raw input is unreliable. `skipID` lets the caller exclude
// the current call (its own ID is usually not yet in msgs, but the
// arg exists defensively).
func findPriorIdenticalToolResult(msgs []provider.Message, current provider.ToolCall, skipID string) int {
	currentKey, ok := canonicalToolCallKey(current)
	if !ok {
		return -1
	}
	// Map ToolCallID → index of its tool_result in msgs so we don't
	// re-scan for each candidate.
	resultIdx := make(map[string]int, len(msgs))
	for i, m := range msgs {
		if m.Role == types.RoleUser && strings.TrimSpace(m.ToolCallID) != "" {
			resultIdx[m.ToolCallID] = i
		}
	}
	// Walk assistant turns (most recent first) so when multiple
	// duplicates exist we stub the NEWEST of the priors — the older
	// ones are usually already stubbed from an earlier pass.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != types.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == skipID {
				continue
			}
			key, ok := canonicalToolCallKey(tc)
			if !ok || key != currentKey {
				continue
			}
			idx, found := resultIdx[tc.ID]
			if !found {
				continue
			}
			return idx
		}
	}
	return -1
}

// canonicalToolCallKey builds a stable hash key from a ToolCall's
// name + input. Returns ("", false) for empty calls so the caller
// can skip them.
func canonicalToolCallKey(tc provider.ToolCall) (string, bool) {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		return "", false
	}
	if tc.Input == nil {
		return name + "|", true
	}
	// json.Marshal on a map uses lexicographic key order, which is
	// exactly what we need for canonicalisation. Errors are ignored
	// — a map with a non-serialisable value just produces "null"
	// and we fall back to name-only comparison.
	raw, err := json.Marshal(tc.Input)
	if err != nil {
		return name + "|", true
	}
	return name + "|" + string(raw), true
}

// formatNativeToolResultPayloadWithLimits turns a tools.Result + error into
// the string payload sent back to the model as tool_result content. Failures
// are signalled with isError=true so the model can pivot rather than retry
// the same call. maxOutput/maxData = 0 falls back to unbounded trim.
func formatNativeToolResultPayloadWithLimits(res tools.Result, toolErr error, maxOutput, maxData int) (string, bool) {
	if toolErr != nil {
		return "ERROR: " + toolErr.Error(), true
	}
	// RTK-style pass: strip ANSI, drop progress/spinner noise, collapse
	// repeated lines. Runs before char-budget trimming so we don't waste
	// budget on decorative bytes the model doesn't need.
	output := compressToolResult(strings.TrimSpace(res.Output))
	data := res.Data
	// For tool_batch_call fan-outs, cap each inner call's output/data
	// proportionally so a 10-file read doesn't eat 10x the budget of a
	// single read. Total model payload stays bounded by maxOutput/maxData
	// regardless of batch size.
	if data != nil {
		if trimmed, didTrim := slimBatchInnerResults(data, maxOutput, maxData); didTrim {
			data = trimmed
		}
	}
	hasData := len(data) > 0
	if output == "" && !hasData {
		return "(no output)", false
	}
	out := trimToolPayload(output, maxOutput)
	if hasData {
		if raw, err := json.MarshalIndent(data, "", "  "); err == nil {
			dataStr := trimToolPayload(compressToolResult(string(raw)), maxData)
			if out == "" {
				out = dataStr
			} else {
				out = out + "\n\nDATA:\n" + dataStr
			}
		}
	}
	if res.Truncated {
		out += "\n\n(output truncated by sandbox)"
	}
	return out, false
}

// slimBatchInnerResults detects a tool_batch_call-shaped Data map and caps
// each inner call's `output` and `data` to a proportional slice of the outer
// budget. Returns a shallow-cloned map so we don't mutate the live
// tools.Result held by the trace (the TUI still sees the full payload via
// the tool:result event, which uses the original Data). Second return is
// true when slimming actually happened.
func slimBatchInnerResults(data map[string]any, maxOutput, maxData int) (map[string]any, bool) {
	rawResults, ok := data["results"]
	if !ok {
		return data, false
	}
	var results []map[string]any
	switch v := rawResults.(type) {
	case []map[string]any:
		results = v
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				results = append(results, m)
			}
		}
	}
	if len(results) == 0 {
		return data, false
	}

	// Proportional budget per inner call with a sane floor (we want the
	// model to get *something* useful from each call even if there are
	// many). 400 chars ≈ 100 tokens — enough for a one-paragraph snippet
	// or a few lines of shell output.
	perOut := maxOutput / len(results)
	if perOut < 400 {
		perOut = 400
	}
	perData := maxData / len(results)
	if perData < 200 {
		perData = 200
	}

	clonedResults := make([]map[string]any, len(results))
	changed := false
	for i, r := range results {
		slot := make(map[string]any, len(r))
		for k, v := range r {
			slot[k] = v
		}
		if s, ok := slot["output"].(string); ok {
			compressed := compressToolResult(s)
			if trimmed := trimToolPayload(compressed, perOut); trimmed != s {
				slot["output"] = trimmed
				changed = true
			}
		}
		if inner, ok := slot["data"].(map[string]any); ok && len(inner) > 0 {
			if raw, err := json.Marshal(inner); err == nil && len(raw) > perData {
				slot["data"] = trimToolPayload(compressToolResult(string(raw)), perData)
				changed = true
			}
		}
		clonedResults[i] = slot
	}
	if !changed {
		return data, false
	}

	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	out["results"] = clonedResults
	return out, true
}

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
		_ = e.Conversation.SaveActive()
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
		"output_tokens":  estimateTokens(outputText),
		"tool_call_id":   trace.Call.ID,
		"surface":        "native",
	}
	if modelPayload != "" {
		payload["payload_chars"] = len(modelPayload)
		payload["payload_tokens"] = estimateTokens(modelPayload)
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

// batchFanoutSummary extracts a compact {count, parallel, ok, fail} view
// from the Result.Data of a tool_batch_call so the TUI can show
// "4 parallel · 3 ok · 1 fail" in the chip preview. Returns nil for
// non-batch tools or malformed data so the caller can skip cheaply.
func batchFanoutSummary(toolName string, data map[string]any) map[string]any {
	if toolName != "tool_batch_call" || data == nil {
		return nil
	}
	results, _ := data["results"].([]map[string]any)
	if results == nil {
		// fallback: some call paths stringify into []any
		if arr, ok := data["results"].([]any); ok {
			for _, v := range arr {
				if m, ok := v.(map[string]any); ok {
					results = append(results, m)
				}
			}
		}
	}
	if len(results) == 0 {
		return nil
	}
	ok, fail := 0, 0
	for _, r := range results {
		if succ, _ := r["success"].(bool); succ {
			ok++
		} else {
			fail++
		}
	}
	out := map[string]any{
		"batch_count": len(results),
		"batch_ok":    ok,
		"batch_fail":  fail,
	}
	if p, ok := data["parallel"].(int); ok && p > 0 {
		out["batch_parallel"] = p
	}
	return out
}
