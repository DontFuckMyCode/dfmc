// Admin/operator endpoints — the four CLI commands that previously had no
// HTTP equivalent: scan, doctor, hooks, config. Read-only views only;
// mutation requires the CLI (config edit, doctor --fix) to keep the
// remote attack surface tight. Every handler lands a JSON shape that
// matches the corresponding `dfmc <cmd> --json` output so an automation
// script can swap CLI for HTTP without reshaping its parser.

package web

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

// handleScan exposes engine.AnalyzeWithOptions's security pass without the
// other analyzer passes. Path defaults to project root; pass ?path=sub/dir
// to scope the scan. Always returns the report's Security section so a CI
// script can wc -l the secrets array directly.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if path != "" && root != "" {
		if _, err := resolvePathWithinRoot(root, path); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "path must be inside the configured project root",
			})
			return
		}
	}
	report, err := s.engine.AnalyzeWithOptions(r.Context(), engine.AnalyzeOptions{
		Path:     path,
		Security: true,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if report.Security == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"path":            path,
			"files_scanned":   0,
			"secrets":         []any{},
			"vulnerabilities": []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, report.Security)
}

// handleHooks mirrors `dfmc hooks --json`. Returns the registered-hook
// inventory grouped by event, plus the total count for quick health
// checks. Empty inventory → empty per_event map and total=0.
func (s *Server) handleHooks(w http.ResponseWriter, _ *http.Request) {
	inv := map[hooks.Event][]hooks.HookInventoryEntry{}
	if s.engine != nil && s.engine.Hooks != nil {
		inv = s.engine.Hooks.Inventory()
	}
	total := 0
	perEvent := make(map[string]any, len(inv))
	for event, entries := range inv {
		key := strings.TrimSpace(string(event))
		if key == "" {
			continue
		}
		list := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			list = append(list, map[string]any{
				"name":      e.Name,
				"command":   e.Command,
				"condition": e.Condition,
				"timeout":   e.Timeout.String(),
			})
			total++
		}
		perEvent[key] = list
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":     total,
		"per_event": perEvent,
	})
}

// handleConfigGet returns the active runtime config as a JSON tree, with
// every API key / secret / token redacted to "***REDACTED***". The CLI
// uses `--raw` to opt out of redaction; the web endpoint never serves
// raw values to keep credentials off the wire.
func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	if s.engine == nil || s.engine.Config == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine config unavailable"})
		return
	}
	cfgMap, err := configToMapForWeb(s.engine.Config)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sanitizeConfigValueForWeb(cfgMap, ""))
}

// handleDoctor mirrors the JSON shape of `dfmc doctor --json` for the
// most actionable subset: config validity, AST backend, memory tier,
// project root, provider configuration. The full CLI doctor adds
// network/MagicDoc/dependency checks that the web endpoint omits to
// keep the call shape predictable for monitoring scrapers.
func (s *Server) handleDoctor(w http.ResponseWriter, _ *http.Request) {
	type doctorCheck struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Details string `json:"details"`
	}
	checks := make([]doctorCheck, 0, 16)
	add := func(name, status, details string) {
		checks = append(checks, doctorCheck{Name: name, Status: status, Details: details})
	}

	if s.engine == nil || s.engine.Config == nil {
		add("config.loaded", "fail", "engine config is nil")
	} else if err := s.engine.Config.Validate(); err != nil {
		add("config.valid", "fail", err.Error())
	} else {
		add("config.valid", "pass", "configuration is valid")
	}

	if s.engine != nil {
		st := s.engine.Status()
		if st.MemoryDegraded {
			details := "episodic/semantic memory unavailable"
			if reason := strings.TrimSpace(st.MemoryLoadErr); reason != "" {
				details += ": " + reason
			}
			add("memory.tier", "warn", details)
		} else {
			add("memory.tier", "pass", "episodic/semantic tiers loaded")
		}
		if backend := strings.TrimSpace(st.ASTBackend); backend == "" {
			add("ast.backend", "warn", "ast engine backend is unavailable")
		} else if backend == "regex" {
			add("ast.backend", "warn", backend)
		} else {
			add("ast.backend", "pass", backend)
		}
		if root := strings.TrimSpace(st.ProjectRoot); root == "" {
			add("project.root", "warn", "project root is empty")
		} else {
			add("project.root", "pass", root)
		}
		if s.engine.Config != nil {
			names := make([]string, 0, len(s.engine.Config.Providers.Profiles))
			for name := range s.engine.Config.Providers.Profiles {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				prof := s.engine.Config.Providers.Profiles[name]
				if strings.TrimSpace(prof.Model) == "" || strings.TrimSpace(prof.Protocol) == "" {
					add("provider."+name+".profile", "warn", "missing model or protocol")
				} else {
					add("provider."+name+".profile", "pass", prof.Protocol+" "+prof.Model)
				}
			}
		}
	}

	failN, warnN, passN := 0, 0, 0
	for _, c := range checks {
		switch c.Status {
		case "fail":
			failN++
		case "warn":
			warnN++
		default:
			passN++
		}
	}
	overall := "ok"
	if failN > 0 {
		overall = "fail"
	} else if warnN > 0 {
		overall = "warn"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     overall,
		"summary":    map[string]int{"pass": passN, "warn": warnN, "fail": failN},
		"checks":     checks,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// configToMapForWeb does the YAML round-trip used by the CLI's `dfmc
// config show` to surface the merged runtime config as a generic map.
// Duplicated rather than imported from cli to keep the layering clean
// (web doesn't depend on cli).
func configToMapForWeb(cfg *config.Config) (map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// sanitizeConfigValueForWeb walks the config tree and replaces any leaf
// whose key is sensitive (api_key, token, secret, password, …) with a
// constant placeholder. Always-on for the web surface.
func sanitizeConfigValueForWeb(value any, path string) any {
	if isSensitiveConfigPath(path) {
		return "***REDACTED***"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			nextPath := k
			if path != "" {
				nextPath = path + "." + k
			}
			out[k] = sanitizeConfigValueForWeb(inner, nextPath)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			nextPath := strconv.Itoa(i)
			if path != "" {
				nextPath = path + "." + nextPath
			}
			out[i] = sanitizeConfigValueForWeb(inner, nextPath)
		}
		return out
	default:
		return v
	}
}

func isSensitiveConfigPath(path string) bool {
	if path == "" {
		return false
	}
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return false
	}
	key := strings.ToLower(parts[len(parts)-1])
	switch key {
	case "api_key", "apikey", "secret", "secret_key", "client_secret", "password", "passphrase", "token":
		return true
	}
	return strings.HasSuffix(key, "_token")
}
