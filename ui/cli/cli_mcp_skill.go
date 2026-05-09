// MCP-side adapter for the skills catalog. The IDE host (Claude
// Desktop, Cursor, VSCode) sees a small set of `dfmc_skill_*`
// virtual tools alongside the regular file/search tools, so it can
// discover the project's skills, read their playbooks, lint a
// candidate SKILL.md before install, and execute one against the
// engine — all without leaving its chat surface.
//
// These tools are NOT registered in engine.Tools — they're synthetic,
// resolved entirely inside this file. Mirrors driveMCPHandler /
// taskMCPHandler in shape and lifecycle.
//
// Tool surface:
//
//   dfmc_skill_list      {}                         -> [{name, source, description, version, triggers, ...}, ...]
//   dfmc_skill_show      {name}                     -> full Skill record + body
//   dfmc_skill_validate  {content?, path?}          -> {diagnostics: [...], ok: bool}
//   dfmc_skill_run       {name, input?}             -> {skill, source, input, answer}
//
// Mirrors `/api/v1/skills` and `/api/v1/skills/validate` shapes
// one-for-one so a host that already speaks to the HTTP surface can
// flip to MCP without remapping fields. dfmc_skill_run goes through
// engine.Ask just like the HTTP variant, so approval / hooks /
// intent layer all run as in any other turn.

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/skills"
)

const skillToolPrefix = "dfmc_skill_"

// skillMCPHandler dispatches the synthetic dfmc_skill_* tools.
// Stateless — every call re-discovers the catalog so a long-lived
// MCP session sees skills installed mid-session.
type skillMCPHandler struct {
	eng *engine.Engine
}

// Handles returns true when `name` is one of our virtual tools.
func (h *skillMCPHandler) Handles(name string) bool {
	return strings.HasPrefix(name, skillToolPrefix)
}

// Tools returns the descriptors merged into tools/list. Order is
// list → show → validate → run, matching the typical author flow.
func (h *skillMCPHandler) Tools() []mcp.ToolDescriptor {
	return []mcp.ToolDescriptor{
		{
			Name:        "dfmc_skill_list",
			Description: "List all skills available in this project (builtins + project + global). Each entry includes name, source, description, version, triggers, and requires. Use this for discovery before dfmc_skill_show / dfmc_skill_run.",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_skill_show",
			Description: "Fetch the full record of one skill — frontmatter fields plus the system_prompt / markdown body that activates when the skill fires. Use after dfmc_skill_list to inspect what a skill will do.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Skill name (case-insensitive). Example: 'audit', 'review'."},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_skill_validate",
			Description: "Lint a SKILL.md / native YAML payload before install. Provide either 'content' (inline body) or 'path' (resolved against project root). Returns structured diagnostics with severity (error/warning/info), field, and message. 'ok' is true when no error-severity issues remain.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{"type": "string", "description": "Full skill file body (frontmatter + markdown for SKILL.md, or YAML for native format)"},
					"path":    map[string]any{"type": "string", "description": "Path to a skill file on disk; relative paths resolve against the project root"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_skill_run",
			Description: "Activate a skill against a free-form input prompt. Goes through engine.Ask just like a regular chat turn — approval gate, hooks, and intent layer all run. Use when the host has identified a specific skill (e.g. via dfmc_skill_list) and wants to drive it directly without typing [[skill:name]] in chat.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "Skill name (case-insensitive). Example: 'audit', 'refactor'."},
					"input": map[string]any{"type": "string", "description": "User-facing prompt for the skill. Example: 'check internal/auth for hardcoded secrets'"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_skill_explain",
			Description: "Preview which skill(s) would activate for a given query, with origin badges (explicit/trigger/task/required), matched trigger patterns, weights, and near-miss/sub-threshold rows so authors can tune trigger regexes without running a real chat turn.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "User query to preview. Example: 'find security vulnerabilities in this repo'"},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
	}
}

// Call dispatches one virtual-tool invocation. All four tools route
// through this single entry point.
func (h *skillMCPHandler) Call(ctx context.Context, name string, rawArgs []byte) (mcp.CallToolResult, error) {
	if h.eng == nil {
		return errResult("engine not initialized")
	}
	switch name {
	case "dfmc_skill_list":
		return h.callList()
	case "dfmc_skill_show":
		return h.callShow(rawArgs)
	case "dfmc_skill_validate":
		return h.callValidate(rawArgs)
	case "dfmc_skill_run":
		return h.callRun(ctx, rawArgs)
	case "dfmc_skill_explain":
		return h.callExplain(rawArgs)
	default:
		return mcp.CallToolResult{}, fmt.Errorf("skill handler: unknown tool %q", name)
	}
}

func (h *skillMCPHandler) callExplain(rawArgs []byte) (mcp.CallToolResult, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode arguments: " + err.Error())
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return errResult(`query is required. Example: {"query":"find security issues"}`)
	}
	exp := skills.Explain(h.eng.Status().ProjectRoot, query)
	return okResult(exp)
}

// skillSummary is the per-skill row returned by dfmc_skill_list. We
// don't reuse the internal Skill struct directly because the host
// rarely needs the full markdown body in a list view, and trimming
// it keeps the MCP payload small. dfmc_skill_show returns the full
// record when the host wants to drill in.
type skillSummary struct {
	Name          string   `json:"name"`
	Source        string   `json:"source"`
	Builtin       bool     `json:"builtin"`
	Description   string   `json:"description,omitempty"`
	Version       string   `json:"version,omitempty"`
	Author        string   `json:"author,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Compatibility string   `json:"compatibility,omitempty"`
	Path          string   `json:"path,omitempty"`
	Preferred     []string `json:"preferred,omitempty"`
	Allowed       []string `json:"allowed,omitempty"`
	Triggers      []string `json:"triggers,omitempty"`
	Requires      []string `json:"requires,omitempty"`
	HasBody       bool     `json:"has_body"`
	AutoActivate  bool     `json:"auto_activate"` // true when at least one trigger is present
}

func skillToSummary(s skills.Skill) skillSummary {
	triggers := make([]string, 0, len(s.Triggers))
	for _, t := range s.Triggers {
		if t.Raw == "" {
			continue
		}
		triggers = append(triggers, fmt.Sprintf("%s (w=%.2f)", t.Raw, t.Weight))
	}
	requires := make([]string, 0, len(s.Requires))
	for _, r := range s.Requires {
		if r.Reason != "" {
			requires = append(requires, fmt.Sprintf("%s (%s)", r.Skill, r.Reason))
		} else {
			requires = append(requires, r.Skill)
		}
	}
	return skillSummary{
		Name:          s.Name,
		Source:        s.Source,
		Builtin:       s.Builtin,
		Description:   s.Description,
		Version:       s.Version,
		Author:        s.Author,
		Tags:          s.Tags,
		Compatibility: s.Compatibility,
		Path:          s.Path,
		Preferred:     s.Preferred,
		Allowed:       s.Allowed,
		Triggers:      triggers,
		Requires:      requires,
		HasBody:       strings.TrimSpace(s.SystemInstruction()) != "",
		AutoActivate:  len(s.Triggers) > 0,
	}
}

func (h *skillMCPHandler) callList() (mcp.CallToolResult, error) {
	root := strings.TrimSpace(h.eng.Status().ProjectRoot)
	raw := skills.Discover(root)
	out := make([]skillSummary, 0, len(raw))
	for _, s := range raw {
		out = append(out, skillToSummary(s))
	}
	return okResult(map[string]any{"skills": out})
}

func (h *skillMCPHandler) callShow(rawArgs []byte) (mcp.CallToolResult, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode arguments: " + err.Error())
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return errResult(`name is required. Example: {"name":"audit"}`)
	}
	root := strings.TrimSpace(h.eng.Status().ProjectRoot)
	skill, ok := skills.Lookup(root, name)
	if !ok {
		return errResult(fmt.Sprintf("skill not found: %s. Run dfmc_skill_list to see what's available.", name))
	}
	body := strings.TrimSpace(skill.SystemInstruction())
	return okResult(map[string]any{
		"skill": skillToSummary(skill),
		"body":  body,
	})
}

func (h *skillMCPHandler) callValidate(rawArgs []byte) (mcp.CallToolResult, error) {
	var args struct {
		Content string `json:"content"`
		Path    string `json:"path"`
	}
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode arguments: " + err.Error())
	}
	content := strings.TrimSpace(args.Content)
	path := strings.TrimSpace(args.Path)
	if content == "" && path == "" {
		return errResult(`either 'content' or 'path' is required. Example: {"path":".dfmc/skills/audit/SKILL.md"} or {"content":"---\nname: x\n---\n# Body"}`)
	}
	var diags []skills.Diagnostic
	if content != "" {
		label := path
		if label == "" {
			label = "<inline>"
		}
		diags = skills.ValidateSkillBytes([]byte(content), label)
	} else {
		resolved := path
		if !strings.HasPrefix(resolved, "/") && !strings.Contains(resolved, ":\\") {
			root := strings.TrimSpace(h.eng.Status().ProjectRoot)
			if root != "" {
				resolved = root + "/" + resolved
			}
		}
		var err error
		diags, err = skills.ValidateSkillFile(resolved)
		if err != nil {
			return errResult(err.Error())
		}
	}
	hasError := false
	for _, d := range diags {
		if d.Severity == skills.SeverityError {
			hasError = true
			break
		}
	}
	return okResult(map[string]any{
		"diagnostics": diags,
		"ok":          !hasError,
	})
}

func (h *skillMCPHandler) callRun(ctx context.Context, rawArgs []byte) (mcp.CallToolResult, error) {
	var args struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	}
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode arguments: " + err.Error())
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return errResult(`name is required. Example: {"name":"audit","input":"check internal/auth for hardcoded secrets"}`)
	}
	root := strings.TrimSpace(h.eng.Status().ProjectRoot)
	skill, ok := skills.Lookup(root, name)
	if !ok {
		return errResult(fmt.Sprintf("skill not found: %s. Run dfmc_skill_list to see what's available.", name))
	}
	input := strings.TrimSpace(args.Input)
	if input == "" {
		input = "Apply this skill to the current project."
	}
	prompt := skills.DecorateQuery(skill.Name, input)
	answer, err := h.eng.Ask(ctx, prompt)
	if err != nil {
		return errResult(fmt.Sprintf("skill run failed: %v", err))
	}
	return okResult(map[string]any{
		"skill":  skill.Name,
		"source": skill.Source,
		"input":  input,
		"answer": answer,
	})
}

// Compile-time assertion that skillMCPHandler implements the same
// shape the bridge expects (Handles/Tools/Call). Catches signature
// drift if the bridge contract changes.
var _ interface {
	Handles(string) bool
	Tools() []mcp.ToolDescriptor
	Call(context.Context, string, []byte) (mcp.CallToolResult, error)
} = (*skillMCPHandler)(nil)
