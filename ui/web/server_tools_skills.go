// Tools, skills, providers, commands, codemap and memory handlers for the
// web API. Extracted from server.go to keep the construction/wiring lean.
// These endpoints all expose read-mostly capability inventories (plus the
// two exec endpoints that invoke tools/skills by name), so they cluster
// naturally into one file.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
	"gopkg.in/yaml.v3"
)

func (s *Server) handleCommands(w http.ResponseWriter, _ *http.Request) {
	reg := commands.DefaultRegistry()
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": reg.ListByCategory(commands.SurfaceWeb),
	})
}

func (s *Server) handleCommandDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command name is required"})
		return
	}
	reg := commands.DefaultRegistry()
	cmd, ok := reg.Lookup(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("command not found: %s", name)})
		return
	}
	writeJSON(w, http.StatusOK, cmd)
}

func (s *Server) handleCodeMap(w http.ResponseWriter, _ *http.Request) {
	if s.engine.CodeMap == nil || s.engine.CodeMap.Graph() == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes": []any{},
			"edges": []any{},
		})
		return
	}
	graph := s.engine.CodeMap.Graph()
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": graph.Nodes(),
		"edges": graph.Edges(),
	})
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tools": s.engine.ListTools(),
	})
}

// handleToolSpec serves the structured ToolSpec for a single tool so the
// workbench (and any scripting consumer) can render parameter shape and
// risk without duplicating the CLI pretty-printer.
func (s *Server) handleToolSpec(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool name is required"})
		return
	}
	if s.engine == nil || s.engine.Tools == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tools engine not initialized"})
		return
	}
	spec, ok := s.engine.Tools.Spec(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("unknown tool: %s", name)})
		return
	}
	writeJSON(w, http.StatusOK, spec)
}

func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	status := s.engine.Status()
	names := make([]string, 0, len(s.engine.Config.Providers.Profiles)+1)
	seen := map[string]struct{}{}
	for name := range s.engine.Config.Providers.Profiles {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if _, ok := seen["offline"]; !ok {
		names = append(names, "offline")
	}
	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		item := map[string]any{
			"name":   name,
			"active": strings.EqualFold(name, status.Provider),
		}
		if prof, ok := s.engine.Config.Providers.Profiles[name]; ok {
			item["model"] = prof.Model
			item["configured"] = strings.TrimSpace(prof.APIKey) != "" || strings.TrimSpace(prof.BaseURL) != ""
		} else {
			item["configured"] = name == "offline"
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_provider": status.Provider,
		"current_model":    status.Model,
		"providers":        items,
	})
}

func (s *Server) handleSkills(w http.ResponseWriter, _ *http.Request) {
	items := []map[string]any{
		{"name": "review", "source": "builtin", "builtin": true},
		{"name": "explain", "source": "builtin", "builtin": true},
		{"name": "refactor", "source": "builtin", "builtin": true},
		{"name": "test", "source": "builtin", "builtin": true},
		{"name": "doc", "source": "builtin", "builtin": true},
	}
	seen := map[string]struct{}{
		"review":   {},
		"explain":  {},
		"refactor": {},
		"test":     {},
		"doc":      {},
	}

	roots := []struct {
		path   string
		source string
	}{
		{path: filepath.Join(s.engine.Status().ProjectRoot, ".dfmc", "skills"), source: "project"},
		{path: filepath.Join(config.UserConfigDir(), "skills"), source: "global"},
	}
	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root.path, "*.y*ml"))
		for _, p := range files {
			name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, map[string]any{
				"name":    name,
				"source":  root.source,
				"builtin": false,
				"path":    p,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(fmt.Sprint(items[i]["name"])) < strings.ToLower(fmt.Sprint(items[j]["name"]))
	})
	writeJSON(w, http.StatusOK, map[string]any{"skills": items})
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	tier := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("tier")))
	limit := 50
	if tier == "working" {
		writeJSON(w, http.StatusOK, s.engine.MemoryWorking())
		return
	}
	memTier := types.MemoryEpisodic
	if tier == "semantic" {
		memTier = types.MemorySemantic
	}
	items, err := s.engine.MemoryList(memTier, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleToolExec(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool name is required"})
		return
	}
	req := ToolExecRequest{Params: map[string]any{}}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	res, err := s.engine.CallTool(r.Context(), name, req.Params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSkillExec(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "skill name is required"})
		return
	}

	req := SkillExecRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		input = strings.TrimSpace(req.Message)
	}

	prompt, source, ok := s.resolveSkillPrompt(name, input)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("skill not found: %s", name)})
		return
	}

	answer, err := s.engine.Ask(r.Context(), prompt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skill":  name,
		"source": source,
		"input":  input,
		"answer": answer,
	})
}

func (s *Server) resolveSkillPrompt(name, input string) (prompt string, source string, ok bool) {
	builtin := map[string]string{
		"review":   "Perform a strict code review. Prioritize bugs, risks, behavioral regressions, and missing tests.\n\nRequest:\n{input}",
		"explain":  "Explain the target code in a clear and structured way, including key flows and important caveats.\n\nRequest:\n{input}",
		"refactor": "Provide a safe refactor plan and concrete edits with minimal regression risk.\n\nRequest:\n{input}",
		"test":     "Create or improve automated tests for the target, including edge cases and failure paths.\n\nRequest:\n{input}",
		"doc":      "Write practical documentation for the requested code or module.\n\nRequest:\n{input}",
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if tpl, exists := builtin[key]; exists {
		return applySkillPromptTemplate(tpl, input), "builtin", true
	}

	roots := []string{
		filepath.Join(s.engine.Status().ProjectRoot, ".dfmc", "skills"),
		filepath.Join(config.UserConfigDir(), "skills"),
	}
	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root, "*.y*ml"))
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			raw := map[string]any{}
			if err := yaml.Unmarshal(data, &raw); err != nil {
				continue
			}
			skillName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if v, exists := raw["name"]; exists {
				n := strings.TrimSpace(fmt.Sprint(v))
				if n != "" {
					skillName = n
				}
			}
			if !strings.EqualFold(skillName, name) {
				continue
			}
			tpl := ""
			if v, exists := raw["prompt"]; exists {
				tpl = strings.TrimSpace(fmt.Sprint(v))
			}
			if tpl == "" {
				if v, exists := raw["template"]; exists {
					tpl = strings.TrimSpace(fmt.Sprint(v))
				}
			}
			if tpl == "" {
				return "", "", false
			}
			src := "project"
			if strings.Contains(strings.ToLower(path), strings.ToLower(filepath.Join(config.UserConfigDir(), "skills"))) {
				src = "global"
			}
			return applySkillPromptTemplate(tpl, input), src, true
		}
	}
	return "", "", false
}

func applySkillPromptTemplate(tpl, input string) string {
	p := strings.TrimSpace(tpl)
	if strings.Contains(p, "{input}") {
		return strings.ReplaceAll(p, "{input}", input)
	}
	if strings.TrimSpace(input) == "" {
		return p
	}
	if p == "" {
		return input
	}
	return p + "\n\nUser request:\n" + input
}
