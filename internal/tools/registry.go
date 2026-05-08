package tools

// registry.go — tool registry CRUD: Register / Get / List / Specs /
// Spec / Search, plus the BackendSpecs / MetaSpecs filters used by
// status output and the meta-tool surface. The registry is append-only
// at runtime — there is no Unregister path, so the implementation
// can take read-locked snapshots without copying the underlying map.
//
// MCP-bridged tools enter the registry via mcpToolAdapter at New()
// time so they show up alongside native tools; that adapter lives
// here too so registry concerns stay together.

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/dontfuckmycode/dfmc/internal/mcp"
)

func (e *Engine) Register(tool Tool) {
	if tool == nil {
		return
	}
	e.lifecycleMu.RLock()
	if e.closed {
		e.lifecycleMu.RUnlock()
		return
	}
	defer e.lifecycleMu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	e.registry[tool.Name()] = tool
}

func (e *Engine) Get(name string) (Tool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.registry[name]
	return t, ok
}

func (e *Engine) List() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.registry))
	for name := range e.registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Specs returns a stable-sorted slice of ToolSpec for every registered tool.
// Tools that don't implement Specer get a synthetic spec derived from
// Name()/Description() with Risk=RiskRead. This is the entry point every
// provider serializer and the meta-tool surface read from.
func (e *Engine) Specs() []ToolSpec {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]ToolSpec, 0, len(e.registry))
	for _, tool := range e.registry {
		out = append(out, specForTool(tool))
	}
	SortSpecs(out)
	return out
}

// Spec returns the ToolSpec for a named tool, or (zero, false) if not found.
func (e *Engine) Spec(name string) (ToolSpec, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	tool, ok := e.registry[name]
	if !ok {
		return ToolSpec{}, false
	}
	return specForTool(tool), true
}

// Search ranks registered tools against a query and returns the top `limit`
// specs. Pass limit<=0 for all matches. Non-matching tools are omitted.
func (e *Engine) Search(query string, limit int) []ToolSpec {
	specs := e.Specs()
	type scored struct {
		spec  ToolSpec
		score int
	}
	ranked := make([]scored, 0, len(specs))
	for _, s := range specs {
		if score := ScoreMatch(s, query); score > 0 {
			ranked = append(ranked, scored{spec: s, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].spec.Name < ranked[j].spec.Name
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]ToolSpec, len(ranked))
	for i, r := range ranked {
		out[i] = r.spec
	}
	return out
}

// MetaSpecs returns the 4 meta-tool specs (tool_search, tool_help, tool_call,
// tool_batch_call). Provider serializers send only these to the model; the
// rest of the registry stays backend-only and is reached via tool_call.
func (e *Engine) MetaSpecs() []ToolSpec {
	out := make([]ToolSpec, 0, 4)
	for _, name := range []string{"tool_search", "tool_help", "tool_call", "tool_batch_call"} {
		if spec, ok := e.Spec(name); ok {
			out = append(out, spec)
		}
	}
	return out
}

// BackendSpecs returns every spec EXCEPT the meta tools. Useful for status
// output, docs, and tests that want to see what the registry actually
// contains.
func (e *Engine) BackendSpecs() []ToolSpec {
	all := e.Specs()
	out := make([]ToolSpec, 0, len(all))
	for _, s := range all {
		if isMetaTool(s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func specForTool(tool Tool) ToolSpec {
	if s, ok := tool.(Specer); ok {
		spec := s.Spec()
		if spec.Risk == "" {
			spec.Risk = RiskRead
		}
		return spec
	}
	return ToolSpec{
		Name:    tool.Name(),
		Summary: tool.Description(),
		Risk:    RiskRead,
	}
}

// mcpToolAdapter exposes one MCP bridge tool as a tools.Tool so it
// appears in the same registry as native tools.
type mcpToolAdapter struct {
	bridge mcp.ToolBridge
	name   string
}

func (a *mcpToolAdapter) Name() string        { return a.name }
func (a *mcpToolAdapter) Description() string { return "MCP tool: " + a.name }

func (a *mcpToolAdapter) Spec() ToolSpec {
	return ToolSpec{
		Name:       a.name,
		Risk:       RiskExecute,
		Idempotent: true,
	}
}

func (a *mcpToolAdapter) Execute(ctx context.Context, req Request) (Result, error) {
	argBytes, _ := json.Marshal(req.Params)
	result, err := a.bridge.Call(ctx, a.name, argBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: result.Content[0].Text, Success: !result.IsError}, nil
}
