// agent_loop_limits.go — runtime budget resolution for the native agent
// loop. Owns the safety-floor defaults, the elastic scaling ratios, and
// the `agentLimits` struct that the loop consults once at entry and
// never re-reads mid-iteration.
//
// The rule (documented on Engine.agentLimits): cfg.Agent.* values act as
// a *floor*, not a ceiling. When the active provider exposes a context
// window we scale each limit up proportionally so a 1M-token window
// doesn't get throttled to the 128k-era defaults. Cfg=0 means "fully
// elastic", so zero-config engines get the scaled budget automatically.
//
// Extracted from agent_loop_native.go to keep the main loop file focused
// on control flow and make the single resolve point easy to find.

package engine

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
