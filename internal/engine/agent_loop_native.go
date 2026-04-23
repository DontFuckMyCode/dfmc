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

// runNativeToolLoopAutonomous wraps runNativeToolLoop with the
// autonomous park→compact→resume cycle. When the loop parks for budget
// reasons and autonomy is enabled (and the cumulative ceiling hasn't
// been hit), we transparently re-enter the loop without returning to
// the caller — the user sees one continuous response instead of having
// to type /continue between every park. Step caps, shutdown parks, and
// errors short-circuit the autonomy and return immediately so the
// user can intervene.
//
// Source label ("ask" / "resume") is recorded on the auto_resume event
// so observability can tell apart the autonomous progression from a
// user-driven /continue chain.
func (e *Engine) runNativeToolLoopAutonomous(ctx context.Context, seed *parkedAgentState, lim agentLimits, source string) (nativeToolCompletion, error) {
	const safetyBound = 64 // belt-and-braces; cumulative ceiling is the real cap
	for attempt := 0; attempt < safetyBound; attempt++ {
		if ctx.Err() != nil {
			return nativeToolCompletion{}, ctx.Err()
		}
		completion, err := e.runNativeToolLoop(ctx, seed, lim)
		if err != nil || !completion.Parked {
			return completion, err
		}
		if completion.ParkedReason != ParkReasonBudgetExhausted {
			// Step cap and shutdown parks deliberately surface to the
			// user — step cap means the model isn't converging, shutdown
			// means the engine is going away. Auto-resume would mask
			// both signals.
			return completion, err
		}
		if !e.autonomousResumeEnabled() {
			return completion, err
		}
		nextSeed, ok := e.attemptAutoResume(source)
		if !ok {
			return completion, err
		}
		seed = nextSeed
	}
	// Hit the safety bound — extremely unlikely given the cumulative
	// ceiling kicks in well before this. Park whatever's current so the
	// user can /continue manually if they want.
	e.publishAgentLoopEvent("agent:loop:safety_bound", map[string]any{
		"safety_bound": safetyBound,
		"source":       source,
		"surface":      "native",
		"provider":     seed.LastProvider,
		"model":        seed.LastModel,
	})
	res, err := e.runNativeToolLoop(ctx, seed, lim)
	return res, err
}

// autonomousResumeEnabled reports whether the engine's config opts in
// to auto-resume on budget park. Default ON; explicit "off"/"false"/"no"/"0"
// disables. The opt-out exists for CI runs that must hard-stop after one
// budget without manual intervention.
func (e *Engine) autonomousResumeEnabled() bool {
	if e == nil || e.Config == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(e.Config.Agent.AutonomousResume)) {
	case "off", "false", "no", "0", "manual":
		return false
	}
	return true
}

// attemptAutoResume runs the same preflight ResumeAgent does — claim
// the parked state, accumulate cumulative counters, refuse on ceiling,
// force-compact history, reset per-attempt counters — and returns the
// fresh seed if and only if another loop attempt is allowed. When it
// returns ok=false the caller must let the park stand: either the
// cumulative ceiling hit (re-parked seed already saved by this fn) or
// there's no parked state (race / cleared).
//
// Emits agent:loop:auto_resume so the TUI can render a one-line "↻
// auto-resuming after compact" indicator instead of the disruptive
// "park / SYS resume / park" trio that breaks the user's reading flow.
func (e *Engine) attemptAutoResume(source string) (*parkedAgentState, bool) {
	seed := e.takeParkedAgent()
	if seed == nil {
		return nil, false
	}
	lim := e.agentLimits()
	seed.CumulativeSteps += seed.Step
	seed.CumulativeTokens += seed.TotalTokens

	mult := e.resumeMaxMultiplier()
	stepCeiling := lim.MaxSteps * mult
	tokenCeiling := lim.MaxTokens * mult
	if lim.MaxSteps > 0 && seed.CumulativeSteps >= stepCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:auto_resume_refused", map[string]any{
			"reason":            "cumulative_steps_ceiling",
			"cumulative_steps":  seed.CumulativeSteps,
			"ceiling":           stepCeiling,
			"max_steps_per_run": lim.MaxSteps,
			"multiplier":        mult,
			"source":            source,
		})
		return nil, false
	}
	if lim.MaxTokens > 0 && seed.CumulativeTokens >= tokenCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:auto_resume_refused", map[string]any{
			"reason":             "cumulative_tokens_ceiling",
			"cumulative_tokens":  seed.CumulativeTokens,
			"ceiling":            tokenCeiling,
			"max_tokens_per_run": lim.MaxTokens,
			"multiplier":         mult,
			"source":             source,
		})
		return nil, false
	}

	priorTokens := seed.TotalTokens
	beforeMsgs := len(seed.Messages)
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
			"phase":            "auto_resume",
		})
	}

	e.publishAgentLoopEvent("agent:loop:auto_resume", map[string]any{
		"resumed_from_step": seed.Step,
		"prior_tokens":      priorTokens,
		"messages_before":   beforeMsgs,
		"messages_after":    len(seed.Messages),
		"cumulative_steps":  seed.CumulativeSteps,
		"cumulative_tokens": seed.CumulativeTokens,
		"step_ceiling":      stepCeiling,
		"token_ceiling":     tokenCeiling,
		"resumes_remaining": stepCeiling - seed.CumulativeSteps,
		"source":            source,
		"surface":           "native",
	})

	seed.Step = 0
	seed.TotalTokens = 0
	return seed, true
}

// defaultResumeMaxMultiplier is the outer ceiling on cumulative agent
// work across every /continue of a single root ask. Each resume gets a
// fresh MaxSteps budget so /continue actually progresses instead of
// instantly re-parking; this multiplier caps the total work one user
// question can spawn. Bumped from 3→10 to support hours-long sustained
// orchestration: with MaxToolSteps=60 default that's 600 cumulative
// steps and ~2.5M cumulative tokens before the engine refuses, which
// fits the "read-edit-verify, repeat for hours" workload without
// letting a runaway model burn unbounded budget. Override per-project
// via cfg.Agent.ResumeMaxMultiplier when even more headroom is needed.
const defaultResumeMaxMultiplier = 10

// resumeMaxMultiplier resolves the active ceiling. Cfg=0 falls back to
// the default; explicit values let high-trust environments lift the cap
// (e.g. an unattended overnight refactor) or tighten it (CI runs that
// must hard-stop after 1 budget).
func (e *Engine) resumeMaxMultiplier() int {
	if e == nil || e.Config == nil || e.Config.Agent.ResumeMaxMultiplier <= 0 {
		return defaultResumeMaxMultiplier
	}
	return e.Config.Agent.ResumeMaxMultiplier
}

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
	mult := e.resumeMaxMultiplier()
	stepCeiling := lim.MaxSteps * mult
	tokenCeiling := lim.MaxTokens * mult
	if lim.MaxSteps > 0 && seed.CumulativeSteps >= stepCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:resume_refused", map[string]any{
			"reason":            "cumulative_steps_ceiling",
			"cumulative_steps":  seed.CumulativeSteps,
			"ceiling":           stepCeiling,
			"max_steps_per_run": lim.MaxSteps,
			"multiplier":        mult,
			"surface":           "native",
		})
		return nativeToolCompletion{}, fmt.Errorf(
			"resume refused: cumulative agent steps %d hit ceiling %d (%d x MaxSteps=%d). The model has already had %d full budgets on this question — start a new ask with refined scope, or raise agent.resume_max_multiplier in config",
			seed.CumulativeSteps, stepCeiling, mult, lim.MaxSteps, mult)
	}
	if lim.MaxTokens > 0 && seed.CumulativeTokens >= tokenCeiling {
		e.saveParkedAgent(seed)
		e.publishAgentLoopEvent("agent:loop:resume_refused", map[string]any{
			"reason":             "cumulative_tokens_ceiling",
			"cumulative_tokens":  seed.CumulativeTokens,
			"ceiling":            tokenCeiling,
			"max_tokens_per_run": lim.MaxTokens,
			"multiplier":         mult,
			"surface":            "native",
		})
		return nativeToolCompletion{}, fmt.Errorf(
			"resume refused: cumulative agent tokens %d hit ceiling %d (%d x MaxTokens=%d). The model has already spent %d full token budgets on this question — start a new ask, or raise agent.resume_max_multiplier in config",
			seed.CumulativeTokens, tokenCeiling, mult, lim.MaxTokens, mult)
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
	// Use the autonomous wrapper so a /continue that itself parks for
	// budget keeps progressing inside the same call instead of forcing
	// the user to type /continue twice. The cumulative ceiling above
	// still caps total work.
	return e.runNativeToolLoopAutonomous(ctx, seed, lim, "resume")
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

