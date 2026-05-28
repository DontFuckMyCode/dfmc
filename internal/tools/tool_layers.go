package tools

// tool_layers.go — tool activation tier system.
//
// Tools are grouped into 5 layers by how they are activated:
//
//   LayerMeta       — Always advertised to the model (tool_search, tool_help, tool_call, tool_batch_call)
//   LayerCore      — Always enabled, cannot be disabled. The essential read/write/search/execute/git tools.
//   LayerSkill     — Enabled by config (Tools.Layers includes "skill"). Domain-specific or expensive tools.
//   LayerConditional — Auto-enabled when project matches certain criteria (language, framework, etc.)
//   LayerOnDemand  — Never auto-advertised. Only reachable via tool_search when user explicitly asks.
//
// The Layers config field in ToolsSection controls which non-Meta layers are active.
// When a layer is absent from the config, all tools in that layer are hidden from
// List/Specs/Search — the layer acts as a coarse pre-filter before the per-tool
// Enabled/Disabled list. Disabling a layer does NOT affect the Disabled list (you can
// still enable a hidden tool manually; it just won't be visible until the layer is on).
//
// Meta tools are ALWAYS shown regardless of the Layers config.

import (
	"slices"
	"strings"
)

// Layer classifies a tool's activation tier.
type Layer string

const (
	LayerMeta        Layer = "meta"        // always visible
	LayerCore        Layer = "core"        // always enabled, cannot be disabled
	LayerSkill       Layer = "skill"       // gated by Tools.Layers config
	LayerConditional Layer = "conditional" // gated by Tools.Layers config (auto-enable hook reserved for future use)
	LayerOnDemand    Layer = "ondemand"    // only via tool_search
)

// layerTools is the canonical list of which tools belong to which layer.
// This is the single source of truth for the layer taxonomy.
var layerTools = map[Layer][]string{
	LayerMeta: {
		"tool_search", "tool_help", "tool_call", "tool_batch_call",
	},
	LayerCore: {
		// Filesystem
		"read_file", "write_file", "edit_file", "apply_patch",
		// Core search
		"grep_codebase", "find_symbol", "call_graph",
		// Execution
		"run_command",
		// Git core
		"git_status", "git_diff", "git_branch", "git_log", "git_blame",
		"git_commit",
		// Essential analysis
		"list_dir", "glob", "codemap",
	},
	LayerSkill: {
		// Language-specific / domain tools
		"web_search", "web_fetch",
		"dependency_graph", "dependency_audit",
		"dead_code", "hunt",
		"audit", "benchmark", "benchmark_regression",
		"interface_diff",
		"doc_generate", "git_review", "changelog_generate",
		"symbol_rename", "symbol_move",
		"spec_parse", "spec_to_todo", "spec_validate",
		"test_discovery", "auto_test",
		"semantic_search", "project_info",
		"ast_query",
	},
	LayerConditional: {
		// Task orchestration — conditional on project complexity
		"task_split", "orchestrate",
		// Git worktree — conditional on active worktree count
		"git_worktree_list", "git_worktree_add", "git_worktree_remove",
		// PR — conditional on git remote being present
		"github_pullrequest",
	},
	LayerOnDemand: {
		// Heavy / dangerous tools only discoverable via tool_search
		"patch_validation",
		"think",
		"todo_write",
	},
}

// ToolLayerOf returns the layer for a tool name. Returns LayerCore for
// tools not explicitly listed (conservative default — unknown tools are
// treated as always-on rather than invisible).
func ToolLayerOf(name string) Layer {
	name = strings.ToLower(strings.TrimSpace(name))
	for layer, tools := range layerTools {
		if slices.Contains(tools, name) {
			return layer
		}
	}
	return LayerCore
}

// AllLayers returns the ordered list of all valid layer names.
func AllLayers() []Layer {
	return []Layer{LayerMeta, LayerCore, LayerSkill, LayerConditional, LayerOnDemand}
}

// layerByName returns the Layer for a string name, or LayerCore for unknown names.
func layerByName(s string) Layer {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "meta":
		return LayerMeta
	case "core":
		return LayerCore
	case "skill":
		return LayerSkill
	case "conditional":
		return LayerConditional
	case "ondemand":
		return LayerOnDemand
	default:
		return LayerCore
	}
}

// isLayerEnabled reports whether a layer is in the active set.
// Meta is always enabled; all others depend on the config.
func isLayerEnabled(layer Layer, activeLayers []Layer) bool {
	if layer == LayerMeta {
		return true
	}
	if len(activeLayers) == 0 {
		// Empty config: all layers enabled (backward-compatible default)
		return true
	}
	return slices.Contains(activeLayers, layer)
}

// activeLayersFromConfig converts the config's Tools.Layers ([]string) into
// a []Layer. Nil or empty configLayers means all layers enabled (backward-
// compatible default).
func activeLayersFromConfig(configLayers []string) []Layer {
	if len(configLayers) == 0 {
		return nil // nil means "all layers enabled"
	}
	out := make([]Layer, 0, len(configLayers))
	for _, s := range configLayers {
		out = append(out, layerByName(s))
	}
	return out
}
