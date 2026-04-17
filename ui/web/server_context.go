// Context, prompt-library, magicdoc and analysis handlers for the web API.
// Extracted from server.go to keep the construction/wiring lean. These
// endpoints share the context-budget + prompt-render plumbing (runtime
// hints, project-brief loading, dependency roll-up) so they live together.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// runtimeHintsFromQuery overlays the four runtime_* query parameters
// (runtime_provider, runtime_model, runtime_tool_style,
// runtime_max_context) onto the engine's default PromptRuntime. Three
// handlers used to inline this 13-line block; drift between copies is
// the kind of bug that hides in plain sight (one handler accepted a
// hint that the other silently dropped). Centralising it keeps the
// HTTP surface honest about which knobs every endpoint accepts.
func (s *Server) runtimeHintsFromQuery(q map[string][]string) ctxmgr.PromptRuntime {
	hints := s.engine.PromptRuntime()
	get := func(key string) string {
		if v, ok := q[key]; ok && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	if p := get("runtime_provider"); p != "" {
		hints.Provider = p
	}
	if m := get("runtime_model"); m != "" {
		hints.Model = m
	}
	if ts := get("runtime_tool_style"); ts != "" {
		hints.ToolStyle = ts
	}
	if raw := get("runtime_max_context"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			hints.MaxContext = n
		}
	}
	return hints
}

func (s *Server) handleContextBudget(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	runtimeHints := s.runtimeHintsFromQuery(r.URL.Query())
	preview := s.engine.ContextBudgetPreviewWithRuntime(query, runtimeHints)
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleContextRecommend(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	runtimeHints := s.runtimeHintsFromQuery(r.URL.Query())
	preview := s.engine.ContextBudgetPreviewWithRuntime(query, runtimeHints)
	recs := s.engine.ContextRecommendationsWithRuntime(query, runtimeHints)
	tuning := s.engine.ContextTuningSuggestionsWithRuntime(query, runtimeHints)
	writeJSON(w, http.StatusOK, map[string]any{
		"query":              query,
		"preview":            preview,
		"recommendations":    recs,
		"tuning_suggestions": tuning,
	})
}

func (s *Server) handleContextBrief(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	maxWords := 240
	if raw := strings.TrimSpace(r.URL.Query().Get("max_words")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxWords = n
		}
	}
	pathFlag := strings.TrimSpace(r.URL.Query().Get("path"))
	path := resolveMagicDocPath(root, pathFlag)
	raw, err := os.ReadFile(path)
	exists := err == nil
	brief := loadProjectBriefForPromptRender(root, pathFlag, maxWords)
	if brief == "" {
		brief = "(none)"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       filepath.ToSlash(path),
		"exists":     exists,
		"max_words":  maxWords,
		"word_count": len(strings.Fields(strings.TrimSpace(brief))),
		"brief":      brief,
		"size_bytes": func() int {
			if !exists {
				return 0
			}
			return len(raw)
		}(),
	})
}

func (s *Server) handlePrompts(w http.ResponseWriter, _ *http.Request) {
	lib := promptlib.New()
	_ = lib.LoadOverrides(s.engine.Status().ProjectRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"prompts": lib.List(),
	})
}

func (s *Server) handlePromptStats(w http.ResponseWriter, r *http.Request) {
	lib := promptlib.New()
	_ = lib.LoadOverrides(s.engine.Status().ProjectRoot)

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
	_ = lib.LoadOverrides(s.engine.Status().ProjectRoot)
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

func (s *Server) handleMagicDocShow(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	target := resolveMagicDocPath(root, strings.TrimSpace(r.URL.Query().Get("path")))
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusOK, map[string]any{
				"path":    filepath.ToSlash(target),
				"exists":  false,
				"content": "",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filepath.ToSlash(target),
		"exists":  true,
		"content": string(data),
	})
}

func (s *Server) handleMagicDocUpdate(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	req := MagicDocUpdateRequest{
		Title:    "DFMC Project Brief",
		Hotspots: 8,
		Deps:     8,
		Recent:   5,
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	out, err := s.updateMagicDoc(r.Context(), root, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	req := AnalyzeRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	// REPORT.md H3: AnalyzeWithOptions accepts an arbitrary path on
	// the CLI ("dfmc analyze /tmp/somewhere" is legitimate operator
	// usage), but over HTTP the request body is untrusted. A caller
	// asking to analyse "/etc" or "../../../home/other-user" would
	// otherwise have the engine walk that tree. Constrain to inside
	// the configured project root here — the trust boundary is the
	// HTTP handler, not the engine.
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if pathArg := strings.TrimSpace(req.Path); pathArg != "" && root != "" {
		if _, err := resolvePathWithinRoot(root, pathArg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "path must be inside the configured project root",
			})
			return
		}
	}
	report, err := s.engine.AnalyzeWithOptions(r.Context(), engine.AnalyzeOptions{
		Path:       req.Path,
		Full:       req.Full,
		Security:   req.Security,
		Complexity: req.Complexity,
		DeadCode:   req.DeadCode,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if req.MagicDoc {
		root := strings.TrimSpace(report.ProjectRoot)
		if root == "" {
			root = strings.TrimSpace(s.engine.Status().ProjectRoot)
		}
		magic, err := s.updateMagicDoc(r.Context(), root, MagicDocUpdateRequest{
			Path:     req.MagicDocPath,
			Title:    req.MagicDocTitle,
			Hotspots: req.MagicDocHotspots,
			Deps:     req.MagicDocDeps,
			Recent:   req.MagicDocRecent,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"report":   report,
			"magicdoc": magic,
		})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) updateMagicDoc(ctx context.Context, root string, req MagicDocUpdateRequest) (map[string]any, error) {
	target := resolveMagicDocPath(root, strings.TrimSpace(req.Path))
	content, err := buildMagicDocContentForWeb(ctx, s.engine, root, strings.TrimSpace(req.Title), req.Hotspots, req.Deps, req.Recent)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	prev, _ := os.ReadFile(target)
	updated := string(prev) != content
	if updated {
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"status":  "ok",
		"path":    filepath.ToSlash(target),
		"updated": updated,
		"bytes":   len(content),
	}, nil
}

func loadProjectBriefForPromptRender(projectRoot, pathFlag string, maxWords int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxWords <= 0 {
		return "(none)"
	}
	path := resolveMagicDocPath(root, pathFlag)
	data, err := os.ReadFile(path)
	if err != nil {
		return "(none)"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "(none)"
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= 48 {
			break
		}
	}
	if len(filtered) == 0 {
		return "(none)"
	}
	return trimWordsForWeb(strings.Join(filtered, "\n"), maxWords)
}

func trimWordsForWeb(text string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) <= maxWords {
		return strings.TrimSpace(text)
	}
	return strings.Join(words[:maxWords], " ")
}

// resolveMagicDocPath resolves a user-supplied magic-doc path inside
// the project root. An absolute path is only honoured if it's still
// inside the root, so an HTTP caller can't coax the web server into
// reading or writing /etc/passwd by passing `path=/etc/passwd`. A
// blank path falls back to the default .dfmc/magic/MAGIC_DOC.md.
// When the caller passes a path that escapes the root, the default
// location is returned instead — callers treat the surfaced path as
// read-only view data; downstream writers (updateMagicDoc) also stat
// the returned path, so a benign fallback is safer than a 500.
func resolveMagicDocPath(projectRoot, pathFlag string) string {
	def := filepath.Join(projectRoot, ".dfmc", "magic", "MAGIC_DOC.md")
	if strings.TrimSpace(pathFlag) == "" {
		return def
	}
	resolved, err := resolvePathWithinRoot(projectRoot, pathFlag)
	if err != nil {
		return def
	}
	return resolved
}

type webDepStat struct {
	Module string
	Count  int
}

func collectDependencyStatsForWeb(eng *engine.Engine, limit int) []webDepStat {
	if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, e := range eng.CodeMap.Graph().Edges() {
		if e.Type != "imports" {
			continue
		}
		mod := strings.TrimSpace(strings.TrimPrefix(e.To, "module:"))
		if mod == "" {
			continue
		}
		counts[mod]++
	}
	out := make([]webDepStat, 0, len(counts))
	for mod, count := range counts {
		out = append(out, webDepStat{Module: mod, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Module < out[j].Module
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func buildMagicDocContentForWeb(ctx context.Context, eng *engine.Engine, projectRoot, title string, hotspotLimit, depLimit, recentLimit int) (string, error) {
	if hotspotLimit <= 0 {
		hotspotLimit = 8
	}
	if depLimit <= 0 {
		depLimit = 8
	}
	if recentLimit <= 0 {
		recentLimit = 5
	}
	if strings.TrimSpace(title) == "" {
		title = "DFMC Project Brief"
	}

	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{Path: projectRoot})
	if err != nil {
		return "", err
	}
	hotspots := report.HotSpots
	if len(hotspots) > hotspotLimit {
		hotspots = hotspots[:hotspotLimit]
	}
	deps := collectDependencyStatsForWeb(eng, depLimit)
	toolsList := eng.ListTools()
	sort.Strings(toolsList)

	w := eng.MemoryWorking()
	recentFiles := clipStringListForWeb(w.RecentFiles, recentLimit)

	active := eng.ConversationActive()
	conversationID := "(none)"
	conversationBranch := "(none)"
	messageCount := 0
	recentUser := []string{}
	recentAssistant := []string{}
	if active != nil {
		conversationID = strings.TrimSpace(active.ID)
		conversationBranch = strings.TrimSpace(active.Branch)
		msgs := active.Messages()
		messageCount = len(msgs)
		recentUser = recentMessagesForWeb(msgs, types.RoleUser, recentLimit)
		recentAssistant = recentMessagesForWeb(msgs, types.RoleAssistant, recentLimit)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# MAGIC DOC: %s\n\n", title)
	b.WriteString("_Current-state project brief optimized for low-token context reuse._\n\n")

	b.WriteString("## Current State\n")
	fmt.Fprintf(&b, "- Generated at: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Project root: `%s`\n", filepath.ToSlash(projectRoot))
	fmt.Fprintf(&b, "- Provider/model: `%s` / `%s`\n", eng.Status().Provider, eng.Status().Model)
	fmt.Fprintf(&b, "- Source files scanned: %d\n", report.Files)
	fmt.Fprintf(&b, "- Graph: nodes=%d edges=%d cycles=%d\n", report.Nodes, report.Edges, report.Cycles)

	b.WriteString("\n## Hotspots\n")
	if len(hotspots) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, n := range hotspots {
			name := strings.TrimSpace(n.Name)
			if name == "" {
				name = strings.TrimSpace(n.ID)
			}
			kind := strings.TrimSpace(n.Kind)
			path := relativeProjectPathForWeb(projectRoot, strings.TrimSpace(n.Path))
			line := "- `" + name + "`"
			if kind != "" {
				line += " kind=" + kind
			}
			if path != "" {
				line += " path=`" + path + "`"
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n## Top Dependencies\n")
	if len(deps) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, d := range deps {
			fmt.Fprintf(&b, "- `%s` (%d imports)\n", d.Module, d.Count)
		}
	}

	b.WriteString("\n## Conversation Snapshot\n")
	fmt.Fprintf(&b, "- Active conversation: `%s` (branch `%s`, %d messages)\n", fallbackStringForWeb(conversationID, "(none)"), fallbackStringForWeb(conversationBranch, "(none)"), messageCount)
	b.WriteString("- Recent user intents:\n")
	if len(recentUser) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentUser {
			b.WriteString("  - " + item + "\n")
		}
	}
	b.WriteString("- Recent assistant outcomes:\n")
	if len(recentAssistant) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentAssistant {
			b.WriteString("  - " + item + "\n")
		}
	}

	b.WriteString("\n## Active Surface\n")
	b.WriteString("- Recent context files:\n")
	if len(recentFiles) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, p := range recentFiles {
			b.WriteString("  - `" + relativeProjectPathForWeb(projectRoot, p) + "`\n")
		}
	}
	b.WriteString("- Registered tools:\n")
	if len(toolsList) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipStringListForWeb(toolsList, 16) {
			b.WriteString("  - `" + name + "`\n")
		}
	}

	b.WriteString("\n## Workflow\n")
	b.WriteString("- Build: `go build ./cmd/dfmc`\n")
	b.WriteString("- Tests: `go test ./...`\n")
	b.WriteString("- Context budget preview: `go run ./cmd/dfmc context budget --query \"security audit\"`\n")
	b.WriteString("- Prompt preview: `go run ./cmd/dfmc prompt render --query \"review auth module\"`\n")
	b.WriteString("- Refresh this file: `go run ./cmd/dfmc magicdoc update`\n")

	return b.String(), nil
}

func recentMessagesForWeb(messages []types.Message, role types.MessageRole, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		if messages[i].Role != role {
			continue
		}
		text := strings.TrimSpace(strings.ReplaceAll(messages[i].Content, "\n", " "))
		if text == "" {
			continue
		}
		if len(text) > 160 {
			text = text[:160] + "..."
		}
		out = append(out, text)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func clipStringListForWeb(list []string, limit int) []string {
	if limit <= 0 || len(list) <= limit {
		out := make([]string, len(list))
		copy(out, list)
		return out
	}
	out := make([]string, limit)
	copy(out, list[:limit])
	return out
}

func relativeProjectPathForWeb(root, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	absP, errP := filepath.Abs(p)
	absR, errR := filepath.Abs(strings.TrimSpace(root))
	if errP == nil && errR == nil && strings.TrimSpace(absR) != "" {
		if rel, err := filepath.Rel(absR, absP); err == nil {
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(p)
}

func fallbackStringForWeb(v, alt string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return alt
	}
	return v
}
