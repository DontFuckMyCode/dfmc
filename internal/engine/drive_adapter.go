// Engine-side adapter that lets internal/drive talk to the engine
// without an import cycle. drive.Runner has two methods (PlannerCall
// and ExecuteTodo); this file implements both on top of the existing
// engine surface (Providers.Complete and RunSubagent respectively).
//
// Lives in internal/engine — not internal/drive — so it can reach
// engine internals (provider/router selection, sub-agent runner,
// event publishing) without exporting them.

package engine

import (
	"context"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	supervisorbridge "github.com/dontfuckmycode/dfmc/internal/supervisor/bridge"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// driveRunner is the engine-side implementation of drive.Runner. The
// fields are read-only after construction; concurrent use is safe
// because every operation goes through the engine's own synchronized
// surface.
type driveRunner struct {
	e *Engine
}

// NewDriveRunner returns a drive.Runner backed by this Engine. Pass
// the result to drive.NewDriver. Returns nil if the engine is not
// initialized (Providers == nil) — callers should guard against that
// rather than panic mid-run.
func (e *Engine) NewDriveRunner() drive.Runner {
	if e == nil || e.Providers == nil {
		return nil
	}
	return &driveRunner{e: e}
}

// PlannerCall issues a single completion against the active provider
// (or the per-call Model override) with no tool loop, no conversation
// history, no codebase context. The planner is stateless by design.
//
// Why not Ask/AskWithMetadata: those run the intent layer, the auto-
// handoff check, and the native tool loop — none of which the planner
// needs. A direct Providers.Complete call keeps planner runs cheap
// and predictable across providers.
func (r *driveRunner) PlannerCall(ctx context.Context, req drive.PlannerRequest) (drive.PlannerResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.e == nil || r.e.Providers == nil {
		return drive.PlannerResponse{}, ErrEngineNotInitialized
	}
	providerName := r.e.provider()
	model := r.e.model()
	if strings.TrimSpace(req.Model) != "" {
		// Model override: treat as a provider profile name (matches
		// how AskRaced and orchestrate already interpret the field).
		providerName = strings.TrimSpace(req.Model)
		model = ""
	}
	creq := provider.CompletionRequest{
		Provider: providerName,
		Model:    model,
		System:   req.System,
		Messages: []provider.Message{
			{Role: types.RoleUser, Content: req.User},
		},
	}
	resp, used, err := r.e.Providers.Complete(ctx, creq)
	if err != nil {
		return drive.PlannerResponse{}, err
	}
	return drive.PlannerResponse{
		Text:     resp.Text,
		Provider: used,
		Model:    resp.Model,
		Tokens:   resp.Usage.TotalTokens,
	}, nil
}

// ExecuteTodo dispatches one TODO as a sub-agent. Maps to RunSubagent
// directly so the TODO inherits all the existing safety machinery:
// fresh sub-conversation, bounded steps/tokens, parking on budget
// exhaust, parent state preservation. The returned summary is the
// sub-agent's final answer (already condensed by the model when the
// sub-agent prompt asks for a brief).
func (r *driveRunner) ExecuteTodo(ctx context.Context, req drive.ExecuteTodoRequest) (drive.ExecuteTodoResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.e == nil {
		return drive.ExecuteTodoResponse{}, ErrEngineNotInitialized
	}
	req = supervisorbridge.NormalizeDriveExecution(req)
	profiles := map[string]config.ModelConfig(nil)
	if r.e.Config != nil {
		profiles = r.e.Config.Providers.Profiles
	}
	req.ProfileCandidates = supervisorbridge.SelectDriveProfiles(req, profiles, r.e.provider(), 4)
	req.Model = supervisorbridge.SelectDriveProfile(req, profiles, r.e.provider())
	task := buildDriveTodoPrompt(req)
	subReq := tools.SubagentRequest{
		Task:         decorateDriveTaskWithSkills(req.Skills, task),
		Role:         driveExecutorRole(req.Role),
		AllowedTools: req.AllowedTools,
		MaxSteps:     req.MaxSteps,
		Model:        req.Model,
		ToolSource:   "drive",
		Skills:       req.Skills,
	}
	res, err := r.e.runSubagentProfiles(ctx, subReq, req.ProfileCandidates)
	if err != nil {
		return drive.ExecuteTodoResponse{
			DurationMs:  res.DurationMs,
			LastContext: r.e.lastContextSnapshot,
		}, err
	}
	parked := false
	if v, ok := res.Data["parked"].(bool); ok {
		parked = v
	}
	attempts := 0
	if v, ok := res.Data["attempts"].(int); ok {
		attempts = v
	}
	fallbackUsed := false
	if v, ok := res.Data["fallback_used"].(bool); ok {
		fallbackUsed = v
	}
	fallbackFrom, _ := res.Data["fallback_from"].(string)
	finalProvider, _ := res.Data["provider"].(string)
	finalModel, _ := res.Data["model"].(string)
	var chain []string
	if raw, ok := res.Data["profiles_tried"].([]string); ok {
		chain = append([]string(nil), raw...)
	}
	reasons := stringSliceFromAny(res.Data["fallback_reasons"])
	return drive.ExecuteTodoResponse{
		Summary:         res.Summary,
		ToolCalls:       res.ToolCalls,
		DurationMs:      res.DurationMs,
		Parked:          parked,
		Provider:        finalProvider,
		Model:           finalModel,
		Attempts:        attempts,
		FallbackUsed:    fallbackUsed,
		FallbackFrom:    fallbackFrom,
		FallbackChain:   chain,
		FallbackReasons: reasons,
		LastContext:     r.e.lastContextSnapshot,
	}, nil
}

// buildDriveTodoPrompt + decorateDriveTaskWithSkills + driveExecutorRole +
// stringSliceFromAny + BeginAutoApprove (with driveAutoApprover +
// newDriveAutoApprover + RequestApproval) live in
// drive_adapter_helpers.go.

// publishDriveEvent is the bridge from drive.Publisher (a generic
// func) to engine.EventBus. Used by callers to wire driver events
// into the engine event stream so TUI/web consumers see drive:*
// events alongside agent:* and provider:* events without needing a
// second subscription.
func (e *Engine) PublishDriveEvent(eventType string, payload map[string]any) {
	if e == nil || e.EventBus == nil {
		return
	}
	e.EventBus.Publish(Event{
		Type:    eventType,
		Source:  "drive",
		Payload: payload,
	})
}
