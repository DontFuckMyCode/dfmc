package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) runSubagentProfiles(ctx context.Context, req tools.SubagentRequest, profiles []string) (tools.SubagentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.Task) == "" {
		return tools.SubagentResult{}, fmt.Errorf("task is required")
	}
	if e == nil || e.Providers == nil {
		return tools.SubagentResult{}, fmt.Errorf("engine not initialized")
	}
	profiles, err := e.normalizeSubagentProfiles(profiles, req.Model)
	if err != nil {
		return tools.SubagentResult{}, err
	}
	if len(profiles) == 0 {
		return tools.SubagentResult{}, fmt.Errorf("sub-agent requires a provider with tool support (current: %s)", e.provider())
	}

	defer e.enterSubagent()()

	skillTexts := resolveSubagentSkillTexts(e.ProjectRoot, req.Skills)
	task := buildSubagentPrompt(req, skillTexts)
	preflight := e.prepareAutonomyPreflight(ctx, task, "subagent", false)
	chunks := e.buildContextChunks(task)
	systemPrompt, systemBlocks := e.buildNativeToolSystemPromptBundle(task, chunks, preflight)
	descriptors := metaSpecsToDescriptors(e.Tools.MetaSpecs())

	contextTokens := 0
	for _, c := range chunks {
		contextTokens += c.TokenCount
	}

	firstProvider, firstModel, err := e.resolveSubagentProfileTarget(profiles[0])
	if err != nil {
		return tools.SubagentResult{}, err
	}
	baseSeed := &parkedAgentState{
		Question:      task,
		Messages:      e.buildToolLoopRequestMessages(task, chunks, systemPrompt, nil),
		Chunks:        chunks,
		SystemPrompt:  systemPrompt,
		SystemBlocks:  systemBlocks,
		Descriptors:   descriptors,
		ContextTokens: contextTokens,
		TotalTokens:   0,
		Step:          0,
		LastProvider:  firstProvider,
		LastModel:     firstModel,
		ToolSource:    normalizeToolSource(req.ToolSource),
	}

	lim := e.agentLimits()
	if req.MaxSteps > 0 && req.MaxSteps < lim.MaxSteps {
		lim.MaxSteps = req.MaxSteps
	}
	if lim.MaxTokens > 0 {
		lim.MaxTokens = lim.MaxTokens / 2
		if lim.MaxTokens < 10000 {
			lim.MaxTokens = 10000
		}
	}

	start := time.Now()
	e.publishAgentLoopEvent("agent:subagent:start", map[string]any{
		"task":                truncateString(req.Task, 200),
		"role":                req.Role,
		"max_tool_steps":      lim.MaxSteps,
		"allowed_tools":       req.AllowedTools,
		"autonomy_plan":       preflight != nil,
		"provider_candidates": append([]string(nil), profiles...),
		"provider":            firstProvider,
		"model":               firstModel,
	})

	var lastErr error
	var priorProfile string
	tried := make([]string, 0, len(profiles))
	fallbackReasons := make([]string, 0, len(profiles))
	for idx, profileName := range profiles {
		providerName, modelName, err := e.resolveSubagentProfileTarget(profileName)
		if err != nil {
			lastErr = err
			break
		}
		tried = append(tried, profileName)
		if idx > 0 {
			e.publishAgentLoopEvent("agent:subagent:fallback", map[string]any{
				"task":                truncateString(req.Task, 200),
				"role":                req.Role,
				"attempt":             idx + 1,
				"from_profile":        priorProfile,
				"to_profile":          profileName,
				"from_provider":       priorProfile,
				"to_provider":         providerName,
				"to_model":            modelName,
				"error":               errString(lastErr),
				"fallback_reasons":    append([]string(nil), fallbackReasons...),
				"provider_candidates": append([]string(nil), profiles...),
			})
		}

		attemptSeed := cloneParkedAgentState(baseSeed)
		attemptSeed.LastProvider = providerName
		attemptSeed.LastModel = modelName

		completion, runErr := e.runNativeToolLoop(ctx, attemptSeed, lim)
		if runErr == nil {
			dur := time.Since(start).Milliseconds()
			e.publishAgentLoopEvent("agent:subagent:done", map[string]any{
				"duration_ms":         dur,
				"tool_rounds":         len(completion.ToolTraces),
				"parked":              completion.Parked,
				"err":                 "",
				"role":                req.Role,
				"provider":            completion.Provider,
				"model":               completion.Model,
				"attempts":            idx + 1,
				"fallback_used":       idx > 0,
				"fallback_from":       firstProvider,
				"fallback_reasons":    append([]string(nil), fallbackReasons...),
				"provider_candidates": append([]string(nil), profiles...),
				"profiles_tried":      append([]string(nil), tried...),
			})
			res := tools.SubagentResult{
				Summary:    strings.TrimSpace(completion.Answer),
				ToolCalls:  len(completion.ToolTraces),
				DurationMs: dur,
				Data: map[string]any{
					"provider":            completion.Provider,
					"model":               completion.Model,
					"tokens":              completion.TokenCount,
					"parked":              completion.Parked,
					"context_refs":        len(completion.Context),
					"attempts":            idx + 1,
					"fallback_used":       idx > 0,
					"fallback_from":       firstProvider,
					"fallback_reasons":    append([]string(nil), fallbackReasons...),
					"provider_candidates": append([]string(nil), profiles...),
					"profiles_tried":      append([]string(nil), tried...),
				},
			}
			if completion.Parked {
				res.Summary = strings.TrimSpace(res.Summary + "\n\n[note: sub-agent reached its step budget; summary reflects partial work]")
			}
			return res, nil
		}
		lastErr = runErr
		fallbackReasons = appendFallbackReason(fallbackReasons, runErr)
		priorProfile = profileName
		if !shouldFallbackSubagentError(runErr) || idx == len(profiles)-1 {
			break
		}
	}

	dur := time.Since(start).Milliseconds()
	e.publishAgentLoopEvent("agent:subagent:done", map[string]any{
		"duration_ms":         dur,
		"tool_rounds":         0,
		"parked":              false,
		"err":                 errString(lastErr),
		"role":                req.Role,
		"attempts":            len(tried),
		"fallback_used":       len(tried) > 1,
		"fallback_from":       firstProvider,
		"fallback_reasons":    append([]string(nil), fallbackReasons...),
		"provider_candidates": append([]string(nil), profiles...),
		"profiles_tried":      append([]string(nil), tried...),
	})
	return tools.SubagentResult{DurationMs: dur}, lastErr
}

func (e *Engine) normalizeSubagentProfiles(candidates []string, override string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates)+1)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		if !e.subagentProfileSupportsTools(name) {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	if override = strings.TrimSpace(override); override != "" {
		if _, _, err := e.resolveSubagentProfileTarget(override); err != nil {
			return nil, err
		}
		add(override)
	}
	for _, name := range candidates {
		if _, _, err := e.resolveSubagentProfileTarget(name); err != nil {
			return nil, err
		}
		add(name)
	}
	if len(out) == 0 {
		if !e.subagentProfileSupportsTools(e.provider()) {
			return nil, nil
		}
		out = append(out, e.provider())
	}
	return out, nil
}

func (e *Engine) subagentProfileSupportsTools(profile string) bool {
	if e == nil || e.Providers == nil || e.Tools == nil {
		return false
	}
	if len(e.Tools.BackendSpecs()) == 0 {
		return false
	}
	providerName, _, err := e.resolveSubagentProfileTarget(profile)
	if err != nil {
		return false
	}
	p, ok := e.Providers.Get(providerName)
	if !ok || p == nil {
		return false
	}
	return p.Hints().SupportsTools
}

func (e *Engine) resolveSubagentProfileTarget(profile string) (string, string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return e.provider(), e.model(), nil
	}
	if e.Config != nil && e.Config.Providers.Profiles != nil {
		if cfg, ok := e.Config.Providers.Profiles[profile]; ok {
			return profile, strings.TrimSpace(cfg.Model), nil
		}
	}
	if e.Providers != nil {
		if p, ok := e.Providers.Get(profile); ok && p != nil {
			return p.Name(), p.Model(), nil
		}
	}
	return "", "", fmt.Errorf("unknown sub-agent model/profile override %q", profile)
}

// ensure skills import usage doesn't become unused in edits.
var _ = skills.Skill{}

func resolveSubagentSkillTexts(projectRoot string, names []string) []string {
	if len(names) == 0 || projectRoot == "" {
		return nil
	}
	var out []string
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if s, ok := skills.Lookup(projectRoot, name); ok {
			if text := strings.TrimSpace(s.SystemInstruction()); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func shouldFallbackSubagentError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func cloneParkedAgentState(seed *parkedAgentState) *parkedAgentState {
	if seed == nil {
		return nil
	}
	clone := *seed
	clone.Messages = cloneProviderMessages(seed.Messages)
	clone.Traces = cloneNativeToolTraces(seed.Traces)
	clone.Chunks = append([]types.ContextChunk(nil), seed.Chunks...)
	clone.SystemBlocks = append([]provider.SystemBlock(nil), seed.SystemBlocks...)
	clone.Descriptors = cloneToolDescriptors(seed.Descriptors)
	clone.RecentCoachHints = append([]string(nil), seed.RecentCoachHints...)
	if len(seed.LoopFileCache) > 0 {
		clone.LoopFileCache = make(map[string]string, len(seed.LoopFileCache))
		for k, v := range seed.LoopFileCache {
			clone.LoopFileCache[k] = v
		}
	}
	return &clone
}

func normalizeToolSource(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "agent"
	}
	return raw
}

func appendFallbackReason(reasons []string, err error) []string {
	text := strings.TrimSpace(errString(err))
	if text == "" {
		return reasons
	}
	return append(reasons, text)
}

func cloneProviderMessages(in []provider.Message) []provider.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]provider.Message, len(in))
	for i, msg := range in {
		out[i] = msg
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]provider.ToolCall, len(msg.ToolCalls))
			for j, call := range msg.ToolCalls {
				out[i].ToolCalls[j] = provider.ToolCall{
					ID:    call.ID,
					Name:  call.Name,
					Input: cloneStringAnyMap(call.Input),
				}
			}
		}
	}
	return out
}

func cloneNativeToolTraces(in []nativeToolTrace) []nativeToolTrace {
	if len(in) == 0 {
		return nil
	}
	out := make([]nativeToolTrace, len(in))
	for i, trace := range in {
		out[i] = trace
		out[i].Call = provider.ToolCall{
			ID:    trace.Call.ID,
			Name:  trace.Call.Name,
			Input: cloneStringAnyMap(trace.Call.Input),
		}
		out[i].Result.Data = cloneStringAnyMap(trace.Result.Data)
	}
	return out
}

func cloneToolDescriptors(in []provider.ToolDescriptor) []provider.ToolDescriptor {
	if len(in) == 0 {
		return nil
	}
	out := make([]provider.ToolDescriptor, len(in))
	for i, desc := range in {
		out[i] = desc
		out[i].InputSchema = cloneStringAnyMap(desc.InputSchema)
	}
	return out
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneAny(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
