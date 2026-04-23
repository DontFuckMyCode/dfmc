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

// Defaults used when agent config is unset *and* the provider exposes no
// context window. They act as a safety floor — the real runtime budget is
// elastic and scales with `provider.MaxContext()` so a 1M-token window gets
// a commensurately bigger tool budget instead of being throttled to 120k.
const (
	// Sustained-loop defaults — these are the safety floor used when both
	// cfg.Agent.* AND the elastic provider-window scaling produce zero.
	// They must agree with config.DefaultConfig().Agent.* (see
	// internal/config/defaults.go); drifting these two sources apart
	// silently halves the budget for engines built without a full
	// DefaultConfig (rare in production, common in unit tests). The
	// numbers were tuned for real refactor work — small enough that a
	// runaway model can't burn through tokens unbounded, large enough
	// to not interrupt a 30-step "read N files, edit M, verify, repeat".
	defaultMaxNativeToolSteps       = 60
	defaultMaxNativeToolTokens      = 250000
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
	// single permission-to-continue checkpoint nudge. Tuned high enough
	// for sustained orchestration (multi-file refactor, read-edit-verify
	// chains) without the model getting prematurely told to stop.
	// Smaller models may benefit from a lower soft cap via config; the
	// default is generous on purpose so big-context models can do real
	// work without the engine fighting them.
	toolRoundSoftCap = 15
	// toolRoundHardCap flips ToolChoice to "none" for every subsequent
	// call, so the provider MUST emit natural-language text. The hard
	// cap is the last guardrail before the step cap trips; raised in
	// lockstep with the soft cap to leave the same ~2x ratio.
	toolRoundHardCap = 30
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

	// Round-cap and headroom knobs were hard-coded constants until the
	// Config promotion landed. They sit in agentLimits so a single resolve
	// step at loop start carries every budget dial — the loop body never
	// re-reads cfg mid-iteration and tests can stub the whole struct.
	RoundSoftCap            int
	RoundHardCap            int
	BudgetHeadroomDivisor   int
	ElasticTokensRatio      float64
	ElasticResultCharsRatio float64
	ElasticDataCharsRatio   float64
}

// agentLimits resolves the runtime budget. Rule: cfg.Agent values are a
// *floor*, not a cap. When the active provider exposes a context window we
// scale each limit up proportionally — so capable models aren't strangled
// by defaults meant for 128k windows. Cfg=0 means "fully elastic".
func (e *Engine) agentLimits() agentLimits {
	lim := agentLimits{
		MaxSteps:                defaultMaxNativeToolSteps,
		MaxTokens:               defaultMaxNativeToolTokens,
		MaxResultChars:          defaultMaxNativeToolResultChars,
		MaxDataChars:            defaultMaxNativeToolDataChars,
		RoundSoftCap:            toolRoundSoftCap,
		RoundHardCap:            toolRoundHardCap,
		BudgetHeadroomDivisor:   budgetHeadroomDivisor,
		ElasticTokensRatio:      elasticToolTokensRatio,
		ElasticResultCharsRatio: elasticToolResultCharsRatio,
		ElasticDataCharsRatio:   elasticToolDataCharsRatio,
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
	if cfg.ToolRoundSoftCap > 0 {
		lim.RoundSoftCap = cfg.ToolRoundSoftCap
	}
	if cfg.ToolRoundHardCap > 0 {
		lim.RoundHardCap = cfg.ToolRoundHardCap
	}
	if cfg.BudgetHeadroomDivisor > 0 {
		lim.BudgetHeadroomDivisor = cfg.BudgetHeadroomDivisor
	}
	if cfg.ElasticToolTokensRatio > 0 {
		lim.ElasticTokensRatio = cfg.ElasticToolTokensRatio
	}
	if cfg.ElasticToolResultCharsRatio > 0 {
		lim.ElasticResultCharsRatio = cfg.ElasticToolResultCharsRatio
	}
	if cfg.ElasticToolDataCharsRatio > 0 {
		lim.ElasticDataCharsRatio = cfg.ElasticToolDataCharsRatio
	}

	window := e.providerMaxContext()
	if window <= 0 {
		return lim
	}

	if scaled := int(float64(window) * lim.ElasticTokensRatio); scaled > lim.MaxTokens {
		lim.MaxTokens = scaled
	}
	if scaled := int(float64(window) * lim.ElasticResultCharsRatio); scaled > lim.MaxResultChars {
		lim.MaxResultChars = scaled
	}
	if scaled := int(float64(window) * lim.ElasticDataCharsRatio); scaled > lim.MaxDataChars {
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

func (e *Engine) runNativeToolLoop(ctx context.Context, seed *parkedAgentState, lim agentLimits) (nativeToolCompletion, error) {
	ctx = tools.SeedMetaToolBudget(ctx)
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
	synthesizeHintInjected := len(traces) >= lim.RoundSoftCap
	emptyRecoveryTried := false

	// Per-loop tool result cache. Lives on seed so it persists across
	// park/resume; lazy-init here on first run. The mutex guards
	// concurrent access from the parallel batch dispatcher.
	if seed.LoopFileCache == nil {
		seed.LoopFileCache = make(map[string]string)
	}
	cacheMu := &sync.Mutex{}

	for step := 1; step <= lim.MaxSteps; step++ {
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
				step, len(traces), totalTokens,
			)
			notice := composeParkedNotice(headline, traces,
				`Restart dfmc and resume — your work is saved.`)
			e.publishAgentLoopEvent("agent:loop:shutdown_parked", map[string]any{
				"step":    step,
				"state":   int(state),
				"surface": "native",
			})
			return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, ParkReasonShuttingDown), nil
		}
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

		// Proactive step-boundary compaction. Once we're past the soft
		// round cap (15 by default), drop the threshold so old rounds get
		// collapsed before headroom crashes. The reactive compactor above
		// uses 0.7; this one uses 0.5 — fires earlier so a long sustained
		// loop never has to pay an emergency park. No-op when the loop is
		// short or the lifecycle is disabled.
		if step > lim.RoundSoftCap {
			if compacted, report := e.proactiveCompactNativeLoopHistory(msgs, systemPrompt, chunks, lim.MaxTokens); report != nil {
				msgs = compacted
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
		var parked *nativeToolCompletion
		msgs, totalTokens, autoRecoveries, parked = e.preflightBudget(seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, question, lastProvider, lastModel, totalTokens, step, autoRecoveries, lim)
		if parked != nil {
			return *parked, nil
		}

		// Synthesis nudge. After N rounds of tool calls the model often
		// has enough context to answer but keeps reading. One explicit
		// "stop gathering, answer now" message has been observed to
		// break that pattern without the harder intervention below.
		if !synthesizeHintInjected && len(traces) >= lim.RoundSoftCap {
			synthesizeHintInjected = true
			msgs = append(msgs, provider.Message{
				Role: types.RoleUser,
				Content: fmt.Sprintf(
					"[system] Checkpoint: %d tool rounds in. If the original task is genuinely complete, "+
						"share the result now. Otherwise keep working — read, edit, run, verify — until "+
						"you've reached a real stopping point. The goal is sustained progress, not a "+
						"premature wrap-up. When you do stop, end with a 2-3 sentence summary covering "+
						"what you accomplished, what's still open, and the natural next step.",
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
		if len(traces) >= lim.RoundHardCap {
			toolChoice = "none"
			e.publishAgentLoopEvent("agent:loop:tools_force_stop", map[string]any{
				"step":        step,
				"tool_rounds": len(traces),
				"hard_cap":    lim.RoundHardCap,
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

		reqProvider := strings.TrimSpace(lastProvider)
		if reqProvider == "" {
			reqProvider = e.provider()
		}
		reqModel := strings.TrimSpace(lastModel)
		if reqModel == "" {
			if selected, ok := e.Providers.Get(reqProvider); ok && selected != nil {
				reqModel = strings.TrimSpace(selected.Model())
			}
		}
		req := provider.CompletionRequest{
			Provider:     reqProvider,
			Model:        reqModel,
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

		// Empty turn: zero tool_calls AND zero visible text. Delegate to
		// handleEmptyTurn which handles the first-time nudge vs the
		// second-time visible-failure case (in agent_loop_phases.go).
		if len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Text) == "" {
			var emptyOut *nativeToolCompletion
			msgs, emptyRecoveryTried, emptyOut = e.handleEmptyTurn(question, msgs, traces, resp, chunks, systemPrompt, lastProvider, lastModel, step, totalTokens, emptyRecoveryTried)
			if emptyOut != nil {
				return *emptyOut, nil
			}
			continue
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

		// Run the round's tool calls (executeAndAppendToolBatch in
		// agent_loop_phases.go), then layer trajectory-aware coach
		// hints over the result before the next provider round.
		var freshStart int
		msgs, traces, freshStart = e.executeAndAppendToolBatch(ctx, resp, msgs, traces, seed.ToolSource, lastProvider, lastModel, step, totalTokens, lim, seed.LoopFileCache, cacheMu)
		msgs = e.injectTrajectoryHints(seed, msgs, traces, freshStart, step)

		// Post-step budget gate (postStepBudget in agent_loop_phases.go).
		msgs, totalTokens, autoRecoveries, parked = e.postStepBudget(seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, question, lastProvider, lastModel, totalTokens, step, autoRecoveries, lim)
		if parked != nil {
			return *parked, nil
		}

		if step == lim.MaxSteps {
			headline := fmt.Sprintf(
				"Parked at step %d — hit the configured ceiling (%d tool rounds, ~%d tokens).",
				step, len(traces), totalTokens,
			)
			notice := composeParkedNotice(headline, traces,
				`Type "devam" / "continue" or /continue to resume — add a note to redirect (e.g. "devam, focus on the test file").`)
			return e.parkNativeToolLoop(question, seed, msgs, traces, chunks, systemPrompt, systemBlocks, descriptors, lastProvider, lastModel, totalTokens, step, notice, ParkReasonStepCap), nil
		}
	}

	return nativeToolCompletion{}, fmt.Errorf("agent tool loop ended unexpectedly")
}

