package tools

// meta_search.go — `tool_search` meta tool. Searches the backend tool
// registry by name/tag/topic and returns ranked short descriptions so
// the model can pick the right backend tool before paying tool_help's
// schema-fetch cost.

import (
	"context"
	"strings"
)

type toolSearchTool struct{ engine *Engine }

func (t *toolSearchTool) Name() string { return "tool_search" }
func (t *toolSearchTool) Description() string {
	return "Search the backend tool registry by name, tag, or summary."
}
func (t *toolSearchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "tool_search",
		Title:   "Search tools",
		Summary: "Find backend tools by query; returns ranked short descriptions.",
		Purpose: "Discover which tools exist before calling one. Query matches name, tags, summary.",
		Risk:    RiskRead,
		Tags:    []string{"meta", "discovery"},
		Args: []Arg{
			{Name: "query", Type: ArgString, Required: true, Description: "Free-text search (name, tag, or topic)."},
			{Name: "limit", Type: ArgInteger, Default: defaultSearchLimit, Description: "Max number of results (default 10)."},
		},
		Returns:    "{query, count, results:[{name, summary, risk, tags}]}",
		Examples:   []string{`{"query":"grep"}`, `{"query":"write files","limit":5}`},
		Idempotent: true,
		CostHint:   "cheap",
	}
}
func (t *toolSearchTool) Execute(_ context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(asString(req.Params, "query", ""))
	if query == "" {
		return Result{}, missingParamError("tool_search", "query", req.Params,
			`{"query":"grep"} or {"query":"write files","limit":5}`,
			`query is a free-text search across tool names + descriptions. Returns the top backend tools (meta tools are filtered out — call them directly, not via tool_search).`)
	}
	limit := asInt(req.Params, "limit", defaultSearchLimit)
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	hits := t.engine.Search(query, limit)
	// Filter meta tools out of search results so the model doesn't burn
	// turns listing the tools it already has.
	visible := make([]ToolSpec, 0, len(hits))
	for _, s := range hits {
		if isMetaTool(s.Name) {
			continue
		}
		visible = append(visible, s)
	}

	results := make([]map[string]any, 0, len(visible))
	var lines []string
	for _, s := range visible {
		results = append(results, map[string]any{
			"name":    s.Name,
			"summary": s.Summary,
			"risk":    string(s.Risk),
			"tags":    s.Tags,
		})
		lines = append(lines, s.ShortHelp())
	}
	output := strings.Join(lines, "\n")
	if output == "" {
		output = "(no matches)"
	}
	return Result{
		Output: output,
		Data: map[string]any{
			"query":   query,
			"count":   len(results),
			"results": results,
		},
	}, nil
}
