// Prompt-building and prompt-runtime methods for the Engine.
// Extracted from engine.go. Groups prompt recommendation and cache
// sizing with the PromptRuntime resolver (provider/model/tool-style/
// default-mode) and the system-prompt assembler that feeds both the
// non-native and native tool loops.

package engine

import (
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) PromptRecommendation(question string) PromptRecommendationInfo {
	return e.PromptRecommendationWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) PromptRecommendationWithRuntime(question string, overrides ctxmgr.PromptRuntime) PromptRecommendationInfo {
	query := strings.TrimSpace(question)
	runtime := e.promptRuntimeWithOverrides(overrides)
	task := detectContextTask(query)
	language := promptlib.InferLanguage(query, nil)
	role := ctxmgr.ResolvePromptRole(query, task)
	profile := ctxmgr.ResolvePromptProfile(query, task, runtime)
	renderBudget := ctxmgr.ResolvePromptRenderBudget(task, profile, runtime)
	promptBudget := ctxmgr.PromptTokenBudget(task, profile, runtime)

	hints := make([]ContextRecommendation, 0, 4)
	add := func(severity, code, message string) {
		hints = append(hints, ContextRecommendation{
			Severity: strings.TrimSpace(strings.ToLower(severity)),
			Code:     strings.TrimSpace(strings.ToLower(code)),
			Message:  strings.TrimSpace(message),
		})
	}

	if runtime.MaxContext > 0 && promptBudget > runtime.MaxContext/4 {
		add("warn", "prompt_budget_high", "Prompt budget is high relative to runtime max_context. Use compact profile or narrower injected context.")
	}
	if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 && profile == "deep" {
		add("warn", "runtime_context_tight", "Runtime context is tight for deep profile. Compact profile may reduce truncation risk.")
	}
	if countExplicitFileMentions(query) == 0 && !strings.Contains(query, "```") {
		add("info", "add_explicit_context", "No explicit file marker or fenced code detected. Add [[file:...]] or inline code blocks for higher precision.")
	}
	if runtime.ToolStyle == "" {
		add("info", "tool_style_unknown", "Provider tool style is unknown. Consider explicit runtime tool-style override when rendering prompts.")
	}
	if len(hints) == 0 {
		add("info", "prompt_budget_balanced", "Prompt profile and budget look balanced for this query.")
	}

	cacheable, dynamic := e.promptCacheTokens(query, overrides)
	percent := 0
	if total := cacheable + dynamic; total > 0 {
		percent = (cacheable * 100) / total
	}

	return PromptRecommendationInfo{
		Provider: runtime.Provider,
		Model:    runtime.Model,

		Task:     task,
		Language: language,
		Profile:  profile,
		Role:     role,

		ToolStyle:  runtime.ToolStyle,
		MaxContext: runtime.MaxContext,
		LowLatency: runtime.LowLatency,

		PromptBudgetTokens: promptBudget,

		ContextFiles:       renderBudget.ContextFiles,
		ToolList:           renderBudget.ToolList,
		InjectedBlocks:     renderBudget.InjectedBlocks,
		InjectedLines:      renderBudget.InjectedLines,
		InjectedTokens:     renderBudget.InjectedTokens,
		ProjectBriefTokens: renderBudget.ProjectBriefTokens,

		CacheableTokens:  cacheable,
		DynamicTokens:    dynamic,
		CacheablePercent: percent,

		Hints: hints,
	}
}

// promptCacheTokens renders the system prompt bundle for the given query
// and returns (cacheable_tokens, dynamic_tokens). No injected context is
// built beyond what BuildSystemPromptBundle already assembles, so the
// call is diagnostic-safe — callable from status endpoints without side
// effects. Returns zeros when the context manager isn't wired up.
func (e *Engine) promptCacheTokens(query string, overrides ctxmgr.PromptRuntime) (int, int) {
	if e == nil || e.Context == nil {
		return 0, 0
	}
	runtime := e.promptRuntimeWithOverrides(overrides)
	bundle := e.Context.BuildSystemPromptBundle(e.ProjectRoot, query, nil, e.ListTools(), runtime)
	if bundle == nil {
		return 0, 0
	}
	return tokens.Estimate(bundle.CacheableText()), tokens.Estimate(bundle.DynamicText())
}
func (e *Engine) promptRuntime() ctxmgr.PromptRuntime {
	rt := ctxmgr.PromptRuntime{
		Provider: strings.TrimSpace(e.provider()),
		Model:    strings.TrimSpace(e.model()),
	}
	if e.Providers == nil {
		return rt
	}
	p, ok := e.Providers.Get(rt.Provider)
	if !ok || p == nil {
		return rt
	}
	hints := p.Hints()
	if rt.Model == "" {
		rt.Model = strings.TrimSpace(p.Model())
	}
	rt.ToolStyle = strings.TrimSpace(hints.ToolStyle)
	rt.DefaultMode = strings.TrimSpace(hints.DefaultMode)
	rt.Cache = hints.Cache
	rt.LowLatency = hints.LowLatency
	rt.MaxContext = hints.MaxContext
	if rt.MaxContext <= 0 {
		rt.MaxContext = p.MaxContext()
	}
	if len(hints.BestFor) > 0 {
		rt.BestFor = append([]string(nil), hints.BestFor...)
	}
	return rt
}

func (e *Engine) PromptRuntime() ctxmgr.PromptRuntime {
	return e.promptRuntime()
}

func (e *Engine) promptRuntimeWithOverrides(overrides ctxmgr.PromptRuntime) ctxmgr.PromptRuntime {
	runtime := e.promptRuntime()

	overrideProvider := strings.TrimSpace(overrides.Provider)
	if overrideProvider != "" && !strings.EqualFold(overrideProvider, runtime.Provider) {
		runtime = e.promptRuntimeForProvider(overrideProvider, strings.TrimSpace(overrides.Model))
	}

	if provider := strings.TrimSpace(overrides.Provider); provider != "" {
		runtime.Provider = provider
	}
	if model := strings.TrimSpace(overrides.Model); model != "" {
		runtime.Model = model
	}
	if style := strings.TrimSpace(overrides.ToolStyle); style != "" {
		runtime.ToolStyle = style
	}
	if mode := strings.TrimSpace(overrides.DefaultMode); mode != "" {
		runtime.DefaultMode = mode
	}
	if overrides.Cache {
		runtime.Cache = true
	}
	if overrides.LowLatency {
		runtime.LowLatency = true
	}
	if overrides.MaxContext > 0 {
		runtime.MaxContext = overrides.MaxContext
	}
	if len(overrides.BestFor) > 0 {
		runtime.BestFor = append([]string(nil), overrides.BestFor...)
	}

	return runtime
}

func (e *Engine) promptRuntimeForProvider(providerName, modelOverride string) ctxmgr.PromptRuntime {
	rt := ctxmgr.PromptRuntime{
		Provider: strings.TrimSpace(providerName),
		Model:    strings.TrimSpace(modelOverride),
	}
	if e.Providers == nil {
		return rt
	}
	p, ok := e.Providers.Get(rt.Provider)
	if !ok || p == nil {
		return rt
	}
	hints := p.Hints()
	if rt.Model == "" {
		rt.Model = strings.TrimSpace(p.Model())
	}
	rt.ToolStyle = strings.TrimSpace(hints.ToolStyle)
	rt.DefaultMode = strings.TrimSpace(hints.DefaultMode)
	rt.Cache = hints.Cache
	rt.LowLatency = hints.LowLatency
	rt.MaxContext = hints.MaxContext
	if rt.MaxContext <= 0 {
		rt.MaxContext = p.MaxContext()
	}
	if len(hints.BestFor) > 0 {
		rt.BestFor = append([]string(nil), hints.BestFor...)
	}
	return rt
}
func (e *Engine) buildSystemPrompt(question string, chunks []types.ContextChunk) (string, []provider.SystemBlock) {
	if e.Context == nil {
		return "", nil
	}
	bundle := e.Context.BuildSystemPromptBundle(
		e.ProjectRoot,
		question,
		chunks,
		e.ListTools(),
		e.promptRuntime(),
	)
	text, blocks := bundleToSystemBlocks(bundle)
	// L2 (REPORT.md): when the persistent memory store failed to load
	// at Init, callers of Memory.Search/Recall silently get empty
	// results. Without telling the model explicitly, it'll conclude
	// "this project has no memory yet" and start writing fresh notes —
	// which evaporate the moment the next session can't read them
	// back. Surface the gate explicitly so the model knows recall is
	// offline (not empty) and avoids relying on it.
	if e.memoryDegraded {
		notice := memoryDegradedSystemNotice(e.memoryLoadErr)
		if text != "" {
			text = text + "\n\n" + notice
		} else {
			text = notice
		}
		// Non-cacheable: the notice can flip on/off across sessions
		// (Memory may load successfully on next start), so we don't
		// want it baked into Anthropic's prompt cache.
		blocks = append(blocks, provider.SystemBlock{
			Label:     "memory-degraded",
			Text:      notice,
			Cacheable: false,
		})
	}
	return text, blocks
}

// memoryDegradedSystemNotice formats the user-invisible system-prompt
// hint that warns the model recall is offline. Lives next to its only
// caller so future tweaks (wording, label) stay obvious. The reason
// is included verbatim so the model can decide whether the failure is
// recoverable (e.g. "database locked" → suggest /doctor) or terminal
// (corrupt store → fall back to project-only context).
func memoryDegradedSystemNotice(reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		r = "store unavailable"
	}
	return "[Memory store is offline — do not rely on historical recall. " +
		"Memory.Search/Recall will return empty results regardless of what was " +
		"learned in prior sessions. Reason: " + r + "]"
}

// bundleToSystemBlocks converts a PromptBundle into the paired (flat text,
// SystemBlocks) form consumed by providers. When the bundle has no cacheable
// sections the blocks slice is nil so non-cache-aware providers keep the
// flat-string fast path.
func bundleToSystemBlocks(bundle *promptlib.PromptBundle) (string, []provider.SystemBlock) {
	if bundle == nil {
		return "", nil
	}
	text := bundle.Text()
	if !bundle.HasCacheable() {
		return text, nil
	}
	blocks := make([]provider.SystemBlock, 0, len(bundle.Sections))
	for _, s := range bundle.Sections {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		blocks = append(blocks, provider.SystemBlock{
			Label:     s.Label,
			Text:      s.Text,
			Cacheable: s.Cacheable,
		})
	}
	return text, blocks
}
