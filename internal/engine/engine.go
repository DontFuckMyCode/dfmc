// engine.go is the skeleton of the Engine. It owns construction,
// shared state, and the small runtime helpers (state accessors,
// background-task wiring) only. Domain methods live in siblings:
//
//   - engine_lifecycle.go   StartServing, Shutdown stage-drain,
//                           publishShutdownError event+stderr fan-out.
//   - engine_tools.go       ListTools, CallTool, tool exec lifecycle
//                           (approval + pre/post hooks + panic guard).
//   - engine_context.go     Context budgeting / recommendations /
//                           tuning, buildContextChunks, reserve
//                           breakdown, compression & task profile.
//   - engine_prompt.go      Prompt recommendations, PromptRuntime
//                           resolver, buildSystemPrompt, system
//                           blocks assembly.
//   - engine_ask.go         Ask / AskRaced / AskWithMetadata /
//                           StreamAsk, history budgeting & summary,
//                           indexCodebase, recordInteraction.
//   - engine_passthrough.go Status, Memory*, Conversation*, provider
//                           status, SetProviderModel / SetVerbose /
//                           ReloadConfig.
//   - engine_analyze.go     Analyze pipeline: dead-code detection,
//                           complexity scoring, language-aware text
//                           strippers.
//
// Status / report / option types live in status_types.go; the native
// agent loop lives in agent_loop_native*.go.

package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/applog"
	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/bot"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/intent"
	"github.com/dontfuckmycode/dfmc/internal/langintel"
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/providerlog"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/internal/toolhistory"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type EngineState int

const (
	StateCreated EngineState = iota
	StateInitializing
	StateReady
	StateServing
	StateShuttingDown
	StateStopped
)

type Engine struct {
	Config      *config.Config
	Storage     *storage.Store
	EventBus    *EventBus
	ProjectRoot string
	Version     string
	AST         *ast.Engine
	CodeMap     *codemap.Engine
	Context     *ctxmgr.Manager
	// lastContextSnapshot holds the retrieval outcome from the most recent
	// buildContextChunks call. Attached to supervisor.Task after todo
	// execution so resume/replay reuse the same chunks.
	lastContextSnapshot *ctxmgr.ContextSnapshot
	Providers           *provider.Router
	Tools               *tools.Engine
	Memory              *memory.Store
	Conversation        *conversation.Manager
	Security            *security.Scanner
	// LangIntel is the per-language knowledge base used to surface tips,
	// patterns, bug patterns, security rules, and idioms during analysis.
	// Nil is safe — callers check before using.
	LangIntel *langintel.Registry

	// Hooks dispatches user-configured shell commands on lifecycle events
	// (user_prompt_submit, pre_tool, post_tool, session_start/end). A nil
	// value is safe — Fire is a no-op on nil.
	Hooks *hooks.Dispatcher

	// LearnedPatterns persists successful tool interaction patterns.
	// Initialized in engine_init.go. nil-safe.
	LearnedPatterns *toolhistory.LearnedPatternStore

	// TelegramBot is the optional Telegram bot. nil means Telegram is
	// not enabled for this instance. When set, messages from Telegram
	// users are routed to the agent loop and replies are sent back
	// via the same bot instance.
	TelegramBot *bot.TelegramBot
	// TelegramSessionName is the display name shown in Telegram messages
	// (e.g. "work", "home"). Distinguishes multiple DFMC instances.
	TelegramSessionName string
	// TelegramAllowedUsers restricts Telegram access to specific user IDs.
	// nil means allow all (subject to config AllowedUsers list).
	TelegramAllowedUsers []int64

	// ProviderLog persists every provider:complete event to a daily
	// JSONL file under <data-dir>/provider_calls/. Survives session
	// crashes and conversation compaction so the user can audit
	// "which model got which prompt and how many tokens it cost"
	// after the fact. nil-safe: Tail/Dir return zero values, Close
	// is a no-op.
	ProviderLog *providerlog.Logger

	// AppLog is the structured application logger. Every error,
	// warning, and operational event across the engine lands here as
	// JSONL under <data-dir>/app/{YYYY-MM-DD}.jsonl. nil-safe.
	AppLog *applog.Logger
	// activeSkills holds the skill names resolved from the current
	// prompt build (BuildSystemPromptBundle). executeToolWithLifecycle
	// reads it to honour skill-scoped Preferred/Allowed tool lists.
	// Set by the prompt-building path before the agent loop runs; cleared
	// when the loop parks or completes.
	activeSkills []string

	// Intent is the state-aware request normalizer that runs before each
	// Ask. Built in Init from Config.Intent + Providers; nil-safe in
	// every consumer (a nil router falls back to the raw input). See
	// internal/intent for the routing semantics.
	Intent *intent.Router

	providerOverride string
	modelOverride    string
	verbose          bool

	// Lock ordering (MUST be held in this order to avoid deadlocks):
	//   1. agentMu   — agent lifecycle + parked state
	//   2. mu        — general state (state, lastContextIn, background)
	// Never acquire agentMu while holding mu. Shutdown only touches mu;
	// the agent loop only touches agentMu and reads state via State()
	// which takes mu independently.
	mu    sync.RWMutex
	state EngineState

	modifiedFiles map[string]time.Time // path -> timestamp, cleared after staleWindow
	seenFiles     map[string]struct{}  // absolute paths already read via read_file in this session

	// toolEventSeq is the monotonically-incrementing source of
	// per-tool-call sequence numbers. Allocated once at the start of
	// each tool execution (callToolFromSource for user-initiated paths,
	// per-call in executeToolCallsParallel for agent-loop paths) and
	// stamped on every Event the lifecycle emits for that execution so
	// subscribers can dedupe (tool:error + tool:timeout + tool:result
	// from one timeout-failure) on (Type, Seq) tuples instead of a
	// time-window heuristic. Atomic so allocation is lock-free.
	toolEventSeq atomic.Uint64

	lastContextIn ContextInStatus
	// lastContextDebug holds the exact content chunks from the most recent
	// LLM context build. Status only exposes metadata; this powers the TUI
	// debug/full context view without recomputing a different retrieval.
	lastContextDebug ContextDebugStatus

	latestUpdate UpdateInfo

	// memoryDegraded is set when Memory.Load() failed during Init. The
	// engine keeps running with an empty in-memory store so the user
	// isn't hard-blocked, but Status surfaces the flag so the TUI can
	// show a "memory disabled" notice and downstream code can decide
	// not to rely on historical recall.
	memoryDegraded bool
	memoryLoadErr  string

	// Background lifecycle. indexCancel cancels the initial codebase
	// indexer; indexWG waits for it (and any other engine-owned
	// goroutines) to finish before Shutdown tears down AST / CodeMap /
	// Storage. Without these, Shutdown could race with a still-running
	// indexer touching the CodeMap after it's been closed.
	backgroundCtx    context.Context
	backgroundCancel context.CancelFunc
	indexCancel      context.CancelFunc
	indexWG          sync.WaitGroup

	// Approval gate state. Previously held in package-level maps keyed
	// by *Engine; moved into the struct so per-engine state goes away
	// naturally with the *Engine instead of needing a Shutdown-time
	// cleanup hook to keep GC happy. approvalMu is independent of mu/
	// agentMu — neither lock should be held while taking it.
	approvalMu         sync.RWMutex
	registeredApprover Approver
	approverToken      any // ownership token for ReleaseApproverWithToken
	approverLeases     map[any]*approverLease
	recentDenials      []RecentDenial

	agentMu         sync.Mutex
	agentParked     *parkedAgentState
	agentNotesQueue []string
	// Project config snapshot used for pre-ask auto-reload. When the user
	// edits .dfmc/config.yaml while the TUI is already running, the next ask
	// should not keep using stale provider/tool state until they remember to
	// type /reload manually.
	configProjectPath    string
	configProjectModTime time.Time
	// subagentInFlight counts active RunSubagent calls. The first subagent
	// to start stashes the parent's parked state under subagentStashed;
	// nested/concurrent subagents bump the counter without touching it.
	// The last subagent to finish restores it. Guarded by agentMu.
	subagentInFlight int
	subagentStashed  *parkedAgentState
}

// Status / report / option types live in status_types.go — keeping
// engine.go for the runtime (Engine struct, lifecycle methods, hot
// loop) instead of mixing JSON-shape data carriers in alongside.

func New(cfg *config.Config) (*Engine, error) {
	return NewWithVersion(cfg, "dev")
}

func NewWithVersion(cfg *config.Config, version string) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return &Engine{
		Config:   cfg,
		Version:  version,
		EventBus: NewEventBus(),
		state:    StateCreated,
	}, nil
}

// SetTelegramBot wires a Telegram bot into the engine. Must be called
// after New and before Init. The bot's message handler forwards Telegram
// messages to the agent loop; responses are sent back via the same bot.
func (e *Engine) SetTelegramBot(tgBot *bot.TelegramBot, sessionName string, allowedUsers []int64) {
	e.TelegramBot = tgBot
	e.TelegramSessionName = sessionName
	e.TelegramAllowedUsers = allowedUsers
}

// AttachSession wires a multi-agent session into the engine. The session
// holds agents and calls back into the engine via an EngineProvider interface.
// The provider is expected to be a *session.Session cast as any to avoid
// importing session in engine.go. Call this after engine.Init completes.
func (e *Engine) AttachSession(provider any) {
	if e == nil || provider == nil {
		return
	}
	if attachSessionProvider != nil {
		attachSessionProvider(e, provider)
	}
}

// attachSessionProvider is set by the session package via SetAttachProvider.
var attachSessionProvider func(interface{}, interface{})

// SetAttachProvider registers the session attachment function.
// Called by the session package to wire the bridge from its init().
func SetAttachProvider(fn func(interface{}, interface{})) {
	attachSessionProvider = fn
}

// sessionBridgeInit is called by the session package in session's init().
// The session package calls this to register its bridge function with the
// engine before AttachSession is ever called. Declared here to avoid
// importing session in engine.go.
var sessionBridgeInit func(fn func(interface{}, interface{}))

// Init lives in engine_init.go — it wires storage / AST / codemap /
// context / tools / memory / conversation / providers / intent / hooks
// and kicks off the initial codebase indexer.
//
// ListTools / CallTool and the shared tool exec lifecycle live in
// engine_tools.go.

// StartServing + Shutdown + publishShutdownError live in
// engine_lifecycle.go.

func (e *Engine) State() EngineState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// Status + cloneContextInStatus live in engine_passthrough.go.
// ContextBudgetPreview / Recommendations / TuningSuggestions live in
// engine_context.go. PromptRecommendation lives in engine_prompt.go.
// SetProviderModel / SetVerbose / ReloadConfig / provider / model and
// provider-status helpers live in engine_passthrough.go.
// buildContextChunks / contextBuildOptions* / setLastContextInStatus /
// buildContextInStatus / normalizeContextPathForStatus /
// explainContextFileReason / contextPathMatchesMention /
// detectContextTask / contextTaskProfile + helpers /
// providerMaxContext* / normalizeContextCompression and compression
// rank helpers / contextReserveBreakdown* live in engine_context.go.
// promptRuntime / PromptRuntime / promptRuntimeWithOverrides /
// promptRuntimeForProvider live in engine_prompt.go.
// buildRequestMessages / conversation history budgeting / trim helpers
// / buildHistorySummary / latestOmittedByRole / recentUserQuestions /
// topTermsFromMessages / topFileMentions live in engine_ask.go.

func (e *Engine) setState(state EngineState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
}

func (e *Engine) requireReady(op string) error {
	if e == nil {
		return ErrEngineNil
	}
	state := e.State()
	switch state {
	case StateReady, StateServing, StateShuttingDown:
		return nil
	}
	if strings.TrimSpace(op) == "" {
		op = "operation"
	}
	return fmt.Errorf("%w for %s (state=%v)", ErrEngineNotInitialized, op, state)
}

// BackgroundContext returns the engine-owned lifecycle context for
// long-lived work started from UI/API surfaces. It is cancelled during
// Shutdown so callers can stop before Storage and other subsystems are
// torn down.
func (e *Engine) BackgroundContext() context.Context {
	e.mu.RLock()
	ctx := e.backgroundCtx
	e.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// StartBackgroundTask runs fn under the engine's lifecycle context and
// joins it during Shutdown via indexWG. The function should return
// promptly when ctx is cancelled.
func (e *Engine) StartBackgroundTask(name string, fn func(context.Context)) {
	if fn == nil {
		return
	}
	ctx := e.BackgroundContext()
	e.indexWG.Add(1)
	types.SafeGo(name, func() {
		defer e.indexWG.Done()
		fn(ctx)
	})
}

// indexCodebase / Ask / AskRaced / AskWithMetadata / StreamAsk /
// recordInteraction live in engine_ask.go.
// buildSystemPrompt / memoryDegradedSystemNotice / bundleToSystemBlocks
// live in engine_prompt.go.
// ensureIndexed / Analyze / AnalyzeWithOptions / collectSourceFiles /
// detectDeadCode / computeComplexity and the text-stripper /
// complexity helpers / minInt / maxInt / estimateTokens live in
// engine_analyze.go.

// exportLearnedPatterns serializes the learned pattern store into a
// context-injection string. Returns "" when there are no patterns.
func (e *Engine) exportLearnedPatterns() string {
	if e.LearnedPatterns == nil {
		return ""
	}
	return e.LearnedPatterns.ExportForContext()
}
