// server_prompt.go — prompt-library HTTP handlers extracted from
// server_context.go so the latter focuses on context-budget +
// magicdoc + analyze. Shared trust-boundary helpers (resolveMagicDocPath,
// loadProjectBriefForPromptRender, runtimeHintsFromQuery overlay)
// stay in their original siblings; this file only reaches in for the
// per-handler logic.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func (s *Server) handlePrompts(w http.ResponseWriter, _ *http.Request) {
	lib := promptlib.New()
	if err := lib.LoadOverrides(s.engine.Status().ProjectRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "[DFMC] prompt library warning: %v\n", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prompts": lib.List(),
	})
}

func (s *Server) handlePromptStats(w http.ResponseWriter, r *http.Request) {
	lib := promptlib.New()
	if err := lib.LoadOverrides(s.engine.Status().ProjectRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "[DFMC] prompt library warning: %v\n", err)
	}

	maxTemplateTokens := 450
	if raw := strings.TrimSpace(r.URL.Query().Get("max_template_tokens")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxTemplateTokens = n
		}
	}
	allowVars := []string{}
	for _, entry := range r.URL.Query()["allow_var"] {
		for _, part := range strings.Split(entry, ",") {
			if p := strings.TrimSpace(part); p != "" {
				allowVars = append(allowVars, p)
			}
		}
	}

	report := promptlib.BuildStatsReport(lib.List(), promptlib.StatsOptions{
		MaxTemplateTokens: maxTemplateTokens,
		AllowVars:         allowVars,
	})
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handlePromptRecommend(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	runtimeHints := s.runtimeHintsFromQuery(r.URL.Query())
	writeJSON(w, http.StatusOK, map[string]any{
		"query":          query,
		"recommendation": s.engine.PromptRecommendationWithRuntime(query, runtimeHints),
	})
}

func (s *Server) handlePromptRender(w http.ResponseWriter, r *http.Request) {
	req := PromptRenderRequest{
		Type:     "system",
		Task:     "auto",
		Language: "auto",
		Profile:  "auto",
		Vars:     map[string]string{},
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if strings.TrimSpace(req.Type) == "" {
		req.Type = "system"
	}
	resolvedTask := strings.TrimSpace(req.Task)
	if strings.EqualFold(resolvedTask, "auto") || resolvedTask == "" {
		resolvedTask = promptlib.DetectTask(req.Query)
	}
	resolvedLang := strings.TrimSpace(req.Language)
	if strings.EqualFold(resolvedLang, "auto") || resolvedLang == "" {
		resolvedLang = promptlib.InferLanguage(req.Query, nil)
	}
	resolvedProfile := strings.TrimSpace(req.Profile)
	runtimeHints := s.engine.PromptRuntime()
	if p := strings.TrimSpace(req.RuntimeProvider); p != "" {
		runtimeHints.Provider = p
	}
	if m := strings.TrimSpace(req.RuntimeModel); m != "" {
		runtimeHints.Model = m
	}
	if ts := strings.TrimSpace(req.RuntimeToolStyle); ts != "" {
		runtimeHints.ToolStyle = ts
	}
	if req.RuntimeMaxContext > 0 {
		runtimeHints.MaxContext = req.RuntimeMaxContext
	}
	if strings.EqualFold(resolvedProfile, "auto") || resolvedProfile == "" {
		resolvedProfile = ctxmgr.ResolvePromptProfile(req.Query, resolvedTask, runtimeHints)
	}
	resolvedRole := strings.TrimSpace(req.Role)
	if strings.EqualFold(resolvedRole, "auto") || resolvedRole == "" {
		resolvedRole = ctxmgr.ResolvePromptRole(req.Query, resolvedTask)
	}
	budget := ctxmgr.ResolvePromptRenderBudget(resolvedTask, resolvedProfile, runtimeHints)
	vars := map[string]string{
		"project_root":     s.engine.Status().ProjectRoot,
		"task":             resolvedTask,
		"language":         resolvedLang,
		"profile":          resolvedProfile,
		"role":             resolvedRole,
		"project_brief":    loadProjectBriefForPromptRender(s.engine.Status().ProjectRoot, "", budget.ProjectBriefTokens),
		"user_query":       strings.TrimSpace(req.Query),
		"context_files":    strings.TrimSpace(req.ContextFiles),
		"injected_context": ctxmgr.BuildInjectedContextWithBudget(s.engine.Status().ProjectRoot, req.Query, budget),
		"tools_overview":   strings.Join(s.engine.ListTools(), ", "),
		"tool_call_policy": ctxmgr.BuildToolCallPolicy(resolvedTask, runtimeHints),
		"response_policy":  ctxmgr.BuildResponsePolicy(resolvedTask, resolvedProfile),
	}
	for k, v := range req.Vars {
		vars[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	lib := promptlib.New()
	if err := lib.LoadOverrides(s.engine.Status().ProjectRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "[DFMC] prompt library warning: %v\n", err)
	}
	prompt := lib.Render(promptlib.RenderRequest{
		Type:     req.Type,
		Task:     resolvedTask,
		Language: resolvedLang,
		Profile:  resolvedProfile,
		Role:     resolvedRole,
		Vars:     vars,
	})
	promptBudgetTokens := ctxmgr.PromptTokenBudget(resolvedTask, resolvedProfile, runtimeHints)
	trimmed := false
	if promptBudgetTokens > 0 {
		before := promptlib.EstimateTokens(prompt)
		prompt = strings.TrimSpace(ctxmgr.TrimPromptToBudget(prompt, promptBudgetTokens))
		after := promptlib.EstimateTokens(prompt)
		trimmed = after < before
	}
	promptTokensEstimate := promptlib.EstimateTokens(prompt)
	writeJSON(w, http.StatusOK, map[string]any{
		"type":                   req.Type,
		"task":                   resolvedTask,
		"language":               resolvedLang,
		"profile":                resolvedProfile,
		"role":                   resolvedRole,
		"vars":                   vars,
		"prompt":                 prompt,
		"prompt_tokens_estimate": promptTokensEstimate,
		"prompt_budget_tokens":   promptBudgetTokens,
		"prompt_trimmed":         trimmed,
	})
}
