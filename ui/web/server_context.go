// Context, prompt-library, magicdoc and analysis handlers for the web API.
// Extracted from server.go to keep the construction/wiring lean. These
// endpoints share the context-budget + prompt-render plumbing (runtime
// hints, project-brief loading, dependency roll-up) so they live together.
//
// Companion siblings (extracted to keep the handler file scannable):
//
//   - server_prompt.go           prompt-library handlers (handlePrompts,
//                                handlePromptStats, handlePromptRecommend,
//                                handlePromptRender)
//   - server_context_magicdoc.go magic-doc generation (updateMagicDoc,
//                                buildMagicDocContentForWeb, dep stats,
//                                recent-message tail extraction)
//   - server_context_helpers.go  loadProjectBriefForPromptRender,
//                                resolveMagicDocPath (trust boundary),
//                                trimWordsForWeb / clipStringListForWeb /
//                                relativeProjectPathForWeb / fallbackStringForWeb

package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/engine"
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

// handleContextGC is the single endpoint for the context-GC dominance
// pass. GET returns the preview (what would be dropped) without
// mutating the active branch; POST actually prunes and reports the
// drop count. Both paths return the same drop_ids + reasons shape so
// the workbench UI can render a unified summary.
func (s *Server) handleContextGC(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		decision := s.engine.PreviewContextGC()
		writeJSON(w, http.StatusOK, map[string]any{
			"drop_ids": decision.DropIDs,
			"reasons":  decision.Reasons,
			"count":    len(decision.DropIDs),
		})
	case http.MethodPost:
		decision, dropped := s.engine.RunContextGC()
		writeJSON(w, http.StatusOK, map[string]any{
			"drop_ids": decision.DropIDs,
			"reasons":  decision.Reasons,
			"count":    len(decision.DropIDs),
			"dropped":  dropped,
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
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
