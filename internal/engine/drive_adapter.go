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
	"fmt"
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
		return drive.PlannerResponse{}, fmt.Errorf("engine not initialized")
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
		return drive.ExecuteTodoResponse{}, fmt.Errorf("engine not initialized")
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

// buildDriveTodoPrompt is the prompt the executor sub-agent sees. The
// brief from prior TODOs goes first so the model has context before
// the actual instructions; success criteria and "report a brief" are
// pinned at the end so the model lands on them when generating its
// final answer.
//
// Keeping this in one place (not per-call template strings) means
// adjusting executor behavior is a one-file change.
func buildDriveTodoPrompt(req drive.ExecuteTodoRequest) string {
	var b strings.Builder
	if strings.TrimSpace(req.Brief) != "" {
		b.WriteString("Context from prior TODOs in this drive run:\n")
		b.WriteString(req.Brief)
		b.WriteString("\n\n")
	}
	b.WriteString("You are working on TODO ")
	b.WriteString(req.TodoID)
	b.WriteString(": ")
	b.WriteString(req.Title)
	if role := strings.TrimSpace(req.Role); role != "" {
		b.WriteString("\nWorker role: ")
		b.WriteString(role)
	}
	if len(req.Labels) > 0 {
		b.WriteString("\nLabels: ")
		b.WriteString(strings.Join(req.Labels, ", "))
	}
	if strings.TrimSpace(req.Verification) != "" {
		b.WriteString("\nVerification expectation: ")
		b.WriteString(req.Verification)
	}
	b.WriteString("\n\nInstructions:\n")
	b.WriteString(strings.TrimSpace(req.Detail))
	if len(req.Skills) > 0 {
		b.WriteString("\n\nSuggested runtime capabilities: ")
		b.WriteString(strings.Join(req.Skills, ", "))
		b.WriteString(". Use them if they sharpen the approach.")
	}
	b.WriteString("\n\nWhen finished, return a SHORT (under 200 tokens) brief covering: what you changed, which files, and how to verify. The brief is the only thing the next TODO will see from your work, so be concrete.")
	b.WriteString("\nIf you discover truly necessary follow-up child tasks that are missing from the plan, append one final JSON object after the brief with this exact top-level key: {\"spawn_todos\":[...]}.")
	b.WriteString("\nEach spawned todo should include title, detail, optional depends_on, file_scope, provider_tag, worker_class, skills, allowed_tools, labels, verification, and confidence. Keep spawned tasks to at most 4 and only emit them when the missing work is real.")
	return b.String()
}

func decorateDriveTaskWithSkills(names []string, input string) string {
	input = strings.TrimSpace(input)
	if len(names) == 0 {
		return input
	}
	out := input
	seen := map[string]struct{}{}
	for i := len(names) - 1; i >= 0; i-- {
		name := strings.TrimSpace(names[i])
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if out == "" {
			out = "[[skill:" + name + "]]"
			continue
		}
		out = "[[skill:" + name + "]]\n" + out
	}
	return out
}

func driveExecutorRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "drive-executor"
	}
	return role
}

func stringSliceFromAny(raw any) []string {
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

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

// BeginAutoApprove activates a scoped auto-approval override for
// drive-owned tool calls. The previous Approver is preserved and
// restored when the returned release function is called. The wildcard
// "*" approves every drive-owned tool.
//
// Returns a no-op release when tools is empty so callers can always
// `defer release()` regardless of config.
func (r *driveRunner) BeginAutoApprove(tools []string) func() {
	if r == nil || r.e == nil || len(tools) == 0 {
		return func() {}
	}
	prev := r.e.approver()
	override := newDriveAutoApprover(prev, tools, "drive")
	r.e.SetApprover(override)
	return func() {
		// Restore the previous approver. Nil prev correctly clears
		// the slot (SetApprover deletes when passed nil).
		r.e.SetApprover(prev)
	}
}

// driveAutoApprover wraps an underlying Approver and unconditionally
// approves any tool in its allowlist. Tools NOT in the allowlist
// fall through to the wrapped Approver — so the user's TUI modal /
// stdin prompt still fires for sensitive operations they didn't
// pre-approve.
type driveAutoApprover struct {
	wrapped Approver
	allow   map[string]struct{}
	source  string
	any     bool // true when "*" is in the allowlist
}

func newDriveAutoApprover(wrapped Approver, tools []string, source string) *driveAutoApprover {
	a := &driveAutoApprover{
		wrapped: wrapped,
		allow:   make(map[string]struct{}, len(tools)),
		source:  strings.ToLower(strings.TrimSpace(source)),
	}
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == "*" {
			a.any = true
			continue
		}
		a.allow[strings.ToLower(t)] = struct{}{}
	}
	return a
}

// RequestApproval implements Approver. Auto-approves on hit, falls
// through to wrapped on miss. When wrapped is nil and the tool is
// not auto-approved we deny — matches the engine's existing fail-
// safe default in askToolApproval.
func (a *driveAutoApprover) RequestApproval(ctx context.Context, req ApprovalRequest) ApprovalDecision {
	name := strings.ToLower(strings.TrimSpace(req.Tool))
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if a.source != "" && source != a.source {
		if a.wrapped == nil {
			return ApprovalDecision{Approved: false, Reason: "no approver registered"}
		}
		return a.wrapped.RequestApproval(ctx, req)
	}
	if a.any {
		return ApprovalDecision{Approved: true, Reason: "drive auto-approve (*)"}
	}
	if _, ok := a.allow[name]; ok {
		return ApprovalDecision{Approved: true, Reason: "drive auto-approve"}
	}
	if a.wrapped == nil {
		return ApprovalDecision{Approved: false, Reason: "no approver registered (drive auto-approve list does not cover " + req.Tool + ")"}
	}
	return a.wrapped.RequestApproval(ctx, req)
}
