package context

// manager_prompt.go — system prompt rendering surface.
// BuildSystemPrompt / BuildSystemPromptWithRuntime / BuildSystemPromptBundle
// pull task/language/role/profile from the query + skill selection, render
// the promptlib bundle, append skill sections, and trim to the token budget.
// Sibling to manager.go (Manager type + Invalidate) and manager_build.go
// (context retrieval pipeline).

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m *Manager) BuildSystemPrompt(projectRoot, query string, chunks []types.ContextChunk, tools []string) string {
	return m.BuildSystemPromptWithRuntime(projectRoot, query, chunks, tools, PromptRuntime{})
}

func (m *Manager) BuildSystemPromptWithRuntime(projectRoot, query string, chunks []types.ContextChunk, tools []string, runtime PromptRuntime) string {
	return m.BuildSystemPromptBundle(projectRoot, query, chunks, tools, runtime).Text()
}

// BuildSystemPromptBundle is the cache-boundary-aware sibling of
// BuildSystemPromptWithRuntime. It returns the rendered prompt as an
// ordered list of PromptSections so providers that support prompt caching
// (Anthropic) can emit cache_control annotations on the stable prefix.
// Callers that need a flat string should call bundle.Text().
func (m *Manager) BuildSystemPromptBundle(projectRoot, query string, chunks []types.ContextChunk, tools []string, runtime PromptRuntime) *promptlib.PromptBundle {
	if m == nil || m.prompts == nil {
		return &promptlib.PromptBundle{
			Sections: []promptlib.PromptSection{
				{Label: "fallback", Text: "You are DFMC, a code intelligence assistant. Be concise, practical, and safe.", Cacheable: true},
			},
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	overrideWarning := ""
	if err := m.prompts.LoadOverrides(projectRoot); err != nil {
		overrideWarning = "Prompt override warning: " + err.Error() + ". Falling back to embedded defaults for unreadable override roots."
	}

	task := promptlib.DetectTask(query)
	skillSelection := skills.ResolveForQuery(projectRoot, query, task)
	cleanQuery := skillSelection.Query
	if cleanQuery == "" {
		cleanQuery = strings.TrimSpace(query)
	}
	task = promptlib.DetectTask(cleanQuery)
	if primary, ok := skillSelection.Primary(); ok && strings.TrimSpace(primary.Task) != "" {
		task = strings.TrimSpace(primary.Task)
	}
	language := promptlib.InferLanguage(cleanQuery, chunks)
	role := ResolvePromptRole(cleanQuery, task)
	if primary, ok := skillSelection.Primary(); ok && strings.TrimSpace(primary.Role) != "" {
		role = strings.TrimSpace(primary.Role)
	}
	profile := ResolvePromptProfile(cleanQuery, task, runtime)
	if primary, ok := skillSelection.Primary(); ok && strings.TrimSpace(primary.Profile) != "" {
		profile = strings.TrimSpace(primary.Profile)
	}
	// Surface active skill names in the system prompt so the model knows
	// which skill overlays are active and can honour Preferred/Allowed lists.
	activeSkillNames := make([]string, 0, len(skillSelection.Skills))
	for _, s := range skillSelection.Skills {
		if n := strings.TrimSpace(s.Name); n != "" {
			activeSkillNames = append(activeSkillNames, n)
		}
	}
	runtime.ActiveSkills = activeSkillNames
	limits := ResolvePromptRenderBudget(task, profile, runtime)
	injected := BuildInjectedContextWithBudget(projectRoot, cleanQuery, limits)
	// Project brief auto-injection gated by IncludeProjectBrief — same
	// opt-in policy as workspace evidence. Without this gate, every
	// system prompt carried 180-320 tokens of MAGIC_DOC.md whether the
	// user asked for project context or not. Explicit user markers
	// ([[file:...]] / [[workspace-context]] / #ctx-files) flip the gate
	// on for that turn via the engine's contextBuildOptions wiring.
	projectBrief := "(none)"
	if runtime.IncludeProjectBrief {
		projectBrief = loadProjectBrief(projectRoot, cleanQuery, task, limits.ProjectBriefTokens)
	}
	bundle := m.prompts.RenderBundle(promptlib.RenderRequest{
		Type:     "system",
		Task:     task,
		Language: language,
		Profile:  profile,
		Role:     role,
		Vars: map[string]string{
			"project_root":                   projectRoot,
			"task":                           task,
			"language":                       language,
			"profile":                        profile,
			"role":                           role,
			"project_brief":                  projectBrief,
			"project_brief_relevant_section": projectBrief,
			"user_query":                     strings.TrimSpace(cleanQuery),
			"context_files":                  summarizeContextFiles(projectRoot, chunks, limits.ContextFiles),
			"injected_context":               injected,
			"tools_overview":                 summarizeTools(tools, limits.ToolList, task),
			"tool_call_policy":               BuildToolCallPolicy(task, runtime),
			"response_policy":                BuildResponsePolicy(task, profile),
			"active_skills":                  summarizeActiveSkills(skillSelection.Skills),
			"skills_inventory":               summarizeSkillInventory(projectRoot, skillSelection.Skills, 10),
		},
	})
	bundle = appendSkillSections(bundle, skillSelection.Skills)
	bundle = appendSkillInventorySection(bundle, summarizeActiveSkills(skillSelection.Skills), summarizeSkillInventory(projectRoot, skillSelection.Skills, 10))
	if budget := PromptTokenBudget(task, profile, runtime); budget > 0 {
		bundle = trimBundleToBudget(bundle, budget)
	}
	if overrideWarning != "" {
		bundle.Sections = append([]promptlib.PromptSection{{
			Label:     "prompt_override_warning",
			Text:      overrideWarning,
			Cacheable: false,
		}}, bundle.Sections...)
	}
	return bundle
}
