// project_info.go — Phase 7 tool for structured project metadata and config inspection.
// Part of the richer filesystem and config/schema inspection tools expansion.
//
// Provides the model with a quick structured snapshot of project identity,
// config state, and basic metrics — collapses what would be multiple
// file reads + shell commands into one deterministic tool call.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ProjectInfoTool struct {
	engine *Engine
}

func NewProjectInfoTool() *ProjectInfoTool { return &ProjectInfoTool{} }
func (t *ProjectInfoTool) Name() string    { return "project_info" }
func (t *ProjectInfoTool) Description() string {
	return "Structured snapshot of project identity, config state, and basic metrics."
}

// SetEngine wires the engine for config access.
func (t *ProjectInfoTool) SetEngine(e *Engine) { t.engine = e }

func (t *ProjectInfoTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "project_info",
		Title:   "Project info",
		Summary: "Structured project metadata: module, Go version, dependency count, config state, and file stats.",
		Purpose: `Use at the start of a session or when exploring a new project to understand what you're working with — module name, Go version, configured providers, enabled tools, and basic file statistics. One deterministic call replaces multiple file reads and shell commands.`,
		Prompt: `Returns a structured snapshot of the current project:
- module path, Go version, module root
- dependency count (go list -m all | wc -l)
- tools config: enabled tools, shell timeouts, blocked commands, limits
- provider config: configured profiles, active model
- basic file stats: total files, breakdown by language
- session info: data dir, project root

Does not scan the full codebase — file stats are lightweight (walk skip). For deep code stats use codemap or analyze.`,
		Risk:     RiskRead,
		Tags:     []string{"config", "project", "metadata", "tools", "providers"},
		Args: []Arg{
			{Name: "section", Type: ArgString, Description: `Scope: "all" | "module" | "tools" | "providers" | "files". Default: all.`},
			{Name: "include_config", Type: ArgBoolean, Default: true, Description: `Include merged config state (tools, providers, agent limits). Default true.`},
		},
		Returns:        "Structured JSON: {module, go_version, deps, tools_config, provider_config, file_stats, session}",
		Idempotent:     true,
		CostHint:       "io-bound",
	}
}

func (t *ProjectInfoTool) Execute(ctx context.Context, req Request) (Result, error) {
	section := strings.TrimSpace(asString(req.Params, "section", "all"))
	includeConfig := asBool(req.Params, "include_config", true)

	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "."
	}

	data := make(map[string]any)

	// Module info section.
	modInfo := fetchModuleInfo(projectRoot)
	data["module"] = modInfo

	// File stats section.
	fileStats := fetchFileStats(projectRoot)
	data["file_stats"] = fileStats

	// Config section.
	if includeConfig {
		configInfo := fetchConfigInfo(t.engine, projectRoot)
		data["tools_config"] = configInfo["tools"]
		data["provider_config"] = configInfo["providers"]
		data["agent_config"] = configInfo["agent"]
	}

	summary := fmt.Sprintf("project_info: %s module=%s", section, modInfo["module_path"])
	return Result{
		Output: summary,
		Data:   data,
	}, nil
}

// --- module info ---

func fetchModuleInfo(projectRoot string) map[string]any {
	info := map[string]any{
		"module_path": "unknown",
		"go_version":  "unknown",
		"deps_count":  0,
		"module_root": projectRoot,
		"has_go_mod":  false,
		"has_go_sum":  false,
	}
	goModPath := filepath.Join(projectRoot, "go.mod")
	if data, err := os.ReadFile(goModPath); err == nil {
		info["has_go_mod"] = true
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				info["module_path"] = strings.TrimPrefix(line, "module ")
			}
			if strings.HasPrefix(line, "go ") {
				info["go_version"] = strings.TrimPrefix(line, "go ")
			}
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "go.sum")); err == nil {
		info["has_go_sum"] = true
	}
	// Count deps (non-test imports).
	if depsOut, err := runCommandContext(ctxBg(), projectRoot, "go", "list", "-m", "all"); err == nil {
		lines := strings.Split(depsOut, "\n")
		info["deps_count"] = max(0, len(lines)-1) // subtract header or empty
	}
	return info
}

// --- file stats ---

type fileStatsInfo struct {
	TotalFiles    int               `json:"total_files"`
	TotalLines    int               `json:"total_lines"`
	ByExtension   map[string]int   `json:"by_extension"`
	ByLanguage    map[string]int    `json:"by_language"`
	SkippedDirs   []string          `json:"skipped_dirs"`
	TestFileCount int               `json:"test_files"`
	Generated     int               `json:"generated"`
}

var projectSkipDirs = []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__", ".venv", ".idea", ".vscode"}

func fetchFileStats(projectRoot string) map[string]any {
	stats := fileStatsInfo{
		ByExtension: make(map[string]int),
		ByLanguage:  make(map[string]int),
		SkippedDirs: projectSkipDirs,
	}

	langMap := map[string]string{
		".go":   "go",
		".ts":   "typescript",
		".tsx":  "typescript",
		".js":   "javascript",
		".jsx":  "javascript",
		".py":   "python",
		".java": "java",
		".rs":   "rust",
		".c":    "c",
		".cpp":  "cpp",
		".h":    "c-header",
		".cs":   "csharp",
		".rb":   "ruby",
		".php":  "php",
		".md":   "markdown",
		".yaml": "yaml",
		".yml":  "yaml",
		".json": "json",
		".toml": "toml",
		".sh":   "shell",
	}

	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			for _, skip := range projectSkipDirs {
				if info.Name() == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		stats.TotalFiles++
		ext := filepath.Ext(path)
		stats.ByExtension[ext]++

		if lang, ok := langMap[ext]; ok {
			stats.ByLanguage[lang]++
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_test.ts") || strings.HasSuffix(path, "_test.py") {
			stats.TestFileCount++
		}
		if strings.Contains(path, "pb.go") || strings.HasSuffix(path, ".gen.go") {
			stats.Generated++
		}
		return nil
	}

	filepath.Walk(projectRoot, walkFn)

	return map[string]any{
		"total_files":    stats.TotalFiles,
		"total_lines":    stats.TotalLines, // 0 — actual count requires reading files
		"by_extension":   stats.ByExtension,
		"by_language":    stats.ByLanguage,
		"skipped_dirs":   stats.SkippedDirs,
		"test_file_count": stats.TestFileCount,
		"generated_files": stats.Generated,
	}
}

// --- config info ---

func fetchConfigInfo(engine *Engine, _ string) map[string]any {
	out := map[string]any{
		"tools":     map[string]any{},
		"providers": map[string]any{},
		"agent":     map[string]any{},
	}
	if engine == nil {
		return out
	}
	cfg := engine.cfg

	// Tools config.
	toolsCfg := map[string]any{
		"shell_timeout":    cfg.Tools.Shell.Timeout,
		"blocked_commands": cfg.Tools.Shell.BlockedCommands,
	}
	if len(cfg.Tools.Enabled) > 0 {
		toolsCfg["enabled_tools"] = cfg.Tools.Enabled
	}
	if len(cfg.Tools.Limits) > 0 {
		toolsCfg["limits"] = cfg.Tools.Limits
	}
	if len(cfg.Tools.RequireApproval) > 0 {
		toolsCfg["require_approval"] = cfg.Tools.RequireApproval
	}
	out["tools"] = toolsCfg

	// Provider config.
	if cfg.Providers.Primary != "" {
		out["providers"] = map[string]any{
			"primary":   cfg.Providers.Primary,
			"fallbacks": cfg.Providers.Fallback,
		}
	}
	if len(cfg.Providers.Profiles) > 0 {
		profileNames := make([]string, 0, len(cfg.Providers.Profiles))
		for name := range cfg.Providers.Profiles {
			profileNames = append(profileNames, name)
		}
		out["provider_profiles"] = profileNames
	}

	// Agent config.
	agentCfg := map[string]any{
		"max_tool_steps":       cfg.Agent.MaxToolSteps,
		"max_tool_tokens":      cfg.Agent.MaxToolTokens,
		"autonomous_resume":    cfg.Agent.AutonomousResume,
		"tool_reasoning":       cfg.Agent.ToolReasoning,
	}
	out["agent"] = agentCfg

	return out
}

// ctxBg returns a background context for non-critical bg ops.
func ctxBg() context.Context { return context.Background() }

// runCommandContext is a minimal exec helper for background data collection.
func runCommandContext(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}