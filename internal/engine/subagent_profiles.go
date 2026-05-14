package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// normalizeSubagentProfiles + subagentProfileSupportsTools +
// resolveSubagentProfileTarget + resolveSubagentSkillTexts +
// shouldFallbackSubagentError + normalizeToolSource +
// appendFallbackReason live in subagent_profiles_helpers.go.

func (e *Engine) runSubagentProfiles(ctx context.Context, req tools.SubagentRequest, profiles []string) (tools.SubagentResult, error) {

	profiles, err := e.normalizeSubagentProfiles(profiles, req.Model)
	if err != nil {
		return tools.SubagentResult{}, err
	}
	if len(profiles) == 0 {
		return tools.SubagentResult{}, fmt.Errorf("sub-agent requires a provider with tool support (current: %s)", e.provider())
	}

	releaseSubagent, err := e.tryEnterSubagent(e.subagentConcurrencyLimit())
	if err != nil {
		e.publishAgentLoopEvent("agent:subagent:done", map[string]any{
			"duration_ms":      0,
			"tool_rounds":      0,
			"parked":           false,
			"err":              errString(err),
			"role":             req.Role,
			"attempts":         0,
			"fallback_used":    false,
			"subagents_active": e.currentSubagentCount(),
			"subagents_limit":  e.subagentConcurrencyLimit(),
		})
		return tools.SubagentResult{}, err
	}
	defer releaseSubagent()

	firstProvider, firstModel, err := e.resolveSubagentProfileTarget(profiles[0])
	if err != nil {
		return tools.SubagentResult{}, err
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

	activeSkills := resolveSubagentSkills(e.ProjectRoot, req.Skills)
	skillTexts := subagentSkillTexts(activeSkills)
	backendSpecs := e.Tools.BackendSpecs()
	journalSection := formatSubagentJournalSection(e.loadSubagentJournal(req.Role))
	task := buildSubagentPrompt(req, skillTexts, subagentPromptEnvironment{
		ProjectRoot:      e.ProjectRoot,
		Provider:         firstProvider,
		Model:            firstModel,
		MaxSteps:         lim.MaxSteps,
		BackendToolCount: len(backendSpecs),
		BackendToolNames: subagentPromptToolSample(backendSpecs, 16),
		JournalSection:   journalSection,
	})
	preflight := e.prepareAutonomyPreflight(ctx, task, "subagent", false)
	chunks := e.buildContextChunks(task)
	systemPrompt, systemBlocks := e.buildNativeToolSystemPromptBundle(task, chunks, preflight)
	descriptors := metaSpecsToDescriptors(e.Tools.MetaSpecs())

	contextTokens := 0
	for _, c := range chunks {
		contextTokens += c.TokenCount
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

	// Bind the AllowedTools whitelist onto the context BEFORE the loop
	// runs so executeToolWithLifecycle's gate (checkSubagentAllowlist)
	// refuses any tool outside the list at the lifecycle funnel — well
	// before approval/hooks/Execute. Empty list short-circuits to a
	// no-op inside withSubagentAllowlist, preserving the
	// "no constraint" default for delegate_task calls without
	// allowed_tools. AllowedPaths is the sibling write-scope gate;
	// same lifecycle funnel, same empty-list-is-no-op contract.
	ctx = withSubagentAllowlist(ctx, req.AllowedTools)
	ctx = withSubagentPathScope(ctx, req.AllowedPaths)

	// If the delegating skill set is enforced (every active skill
	// declares allowed_tools), apply the union as a hard gate before
	// any tool runs. This mirrors the checkSkillAllowlist gate in
	// executeToolWithLifecycle so Drive TODOs are subject to the same
	// constraint the main agent loop already enforces. Empty skill list
	// or a skill without allowed_tools keeps the gate as a no-op.
	if len(activeSkills) > 0 {
		allowed, enforced := skills.EffectiveAllowedTools(activeSkills)
		ctx = withSkillAllowlist(ctx, allowed, enforced)
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

		// Autonomous sub-agents (currently: drive TODOs) opt into the
		// auto-resume wrapper so a budget-exhaust mid-task force-
		// compacts and re-enters transparently. Plain delegate_task
		// keeps the bare loop because its caller (the parent native
		// tool loop) is already inside its own autonomous wrapper —
		// double-wrapping would multiply the resume ceiling.
		var completion nativeToolCompletion
		var runErr error
		if req.Autonomous {
			completion, runErr = e.runNativeToolLoopAutonomous(ctx, attemptSeed, lim, "subagent", nil)
		} else {
			completion, runErr = e.runNativeToolLoop(ctx, attemptSeed, lim, nil)
		}
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
			// Persist this delegation into the per-role journal so a
			// later delegate_task with the same role sees prior
			// findings instead of re-deriving them. Best-effort:
			// loadSubagentJournal returns nil on any failure, so a
			// missed write just degrades the next call to "no prior
			// context" — never blocks the caller.
			e.appendSubagentJournal(req.Role, subagentJournalEntry{
				Task:     req.Task,
				Summary:  res.Summary,
				Provider: completion.Provider,
				Model:    completion.Model,
				Parked:   completion.Parked,
			})
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
