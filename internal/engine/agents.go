// agents.go — sub-agent catalog: the role overlays loaded from the prompt
// library plus the provider profiles configured for runtime delegation.
// Surfaced over CLI (`dfmc agents`), TUI (`/agents`), web (`/api/v1/agents`),
// and remote (`dfmc remote agents`). One source of truth so the four layers
// don't drift.
//
// The two sub-agent surfaces are deliberately separate:
//   - Role  = personality (planner / reviewer / researcher / debugger / …)
//   - Profile = runtime (anthropic / openai / deepseek / …; tool-capable or
//     not, configured or not)
//
// A sub-agent dispatch picks one of each: orchestrate / delegate_task /
// drive stages take both `role` and `model` (= profile name).

package engine

import (
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

// AgentRole captures one role entry: every system.role.* overlay collapsed
// into a single row keyed by the role name. Bodies is sorted highest-priority
// first so callers that want to render "the" overlay take Bodies[0].
type AgentRole struct {
	Role    string          `json:"role"`
	Summary string          `json:"summary"`
	Bodies  []AgentRoleBody `json:"bodies,omitempty"`
}

type AgentRoleBody struct {
	ID       string `json:"id"`
	Task     string `json:"task,omitempty"`
	Priority int    `json:"priority"`
	Body     string `json:"body,omitempty"`
}

// AgentProfile captures the runtime side: a provider profile bound to a
// model with the bits that decide whether it can back a sub-agent
// (Tools=true, Configured=true).
type AgentProfile struct {
	Name       string `json:"name"`
	Model      string `json:"model,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Tools      bool   `json:"tools"`
	Configured bool   `json:"configured"`
	Active     bool   `json:"active,omitempty"`
}

// AgentCatalog is the top-level result returned by Engine.Agents().
type AgentCatalog struct {
	Roles    []AgentRole    `json:"roles"`
	Profiles []AgentProfile `json:"profiles"`
}

// Agents builds the catalog: roles from the prompt library (embedded
// defaults + project / global overrides) plus profiles from the active
// engine config. Pure read; no provider call.
func (e *Engine) Agents() AgentCatalog {
	return AgentCatalog{
		Roles:    e.agentRoles(),
		Profiles: e.agentProfiles(),
	}
}

func (e *Engine) agentRoles() []AgentRole {
	lib := promptlib.New()
	if e != nil {
		_ = lib.LoadOverrides(e.ProjectRoot)
	}
	byRole := map[string]*AgentRole{}
	for _, t := range lib.List() {
		role := strings.TrimSpace(t.Role)
		// "generalist" is the implicit baseline every agent gets — surfacing
		// it in the catalog adds noise without informing decisions.
		if role == "" || strings.EqualFold(role, "generalist") {
			continue
		}
		entry := byRole[role]
		if entry == nil {
			entry = &AgentRole{Role: role}
			byRole[role] = entry
		}
		entry.Bodies = append(entry.Bodies, AgentRoleBody{
			ID:       t.ID,
			Task:     t.Task,
			Priority: t.Priority,
			Body:     t.Body,
		})
		if entry.Summary == "" {
			if d := strings.TrimSpace(t.Description); d != "" {
				entry.Summary = d
			} else if s := firstSentence(t.Body); s != "" {
				entry.Summary = s
			}
		}
	}
	out := make([]AgentRole, 0, len(byRole))
	for _, v := range byRole {
		sort.Slice(v.Bodies, func(i, j int) bool { return v.Bodies[i].Priority > v.Bodies[j].Priority })
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

func (e *Engine) agentProfiles() []AgentProfile {
	if e == nil || e.Config == nil {
		return nil
	}
	out := make([]AgentProfile, 0, len(e.Config.Providers.Profiles))
	for name, prof := range e.Config.Providers.Profiles {
		entry := AgentProfile{
			Name:       name,
			Model:      strings.TrimSpace(prof.BestModel()),
			Protocol:   strings.TrimSpace(prof.Protocol),
			Configured: strings.TrimSpace(prof.APIKey) != "",
		}
		if e.Providers != nil {
			if p, ok := e.Providers.Get(name); ok && p != nil {
				entry.Tools = p.Hints().SupportsTools
				if entry.Model == "" {
					entry.Model = strings.TrimSpace(p.Model())
				}
			}
			if strings.EqualFold(name, e.Providers.Primary()) {
				entry.Active = true
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		if out[i].Tools != out[j].Tools {
			return out[i].Tools
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// firstSentence pulls a one-line summary out of an overlay body. Splits on
// the first ". " or newline and strips list markers so a YAML body's
// headline reads cleanly in catalog output.
func firstSentence(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if i := strings.Index(body, "\n"); i >= 0 {
		body = body[:i]
	}
	if i := strings.Index(body, ". "); i >= 0 {
		body = body[:i+1]
	}
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "- ")
	body = strings.TrimPrefix(body, "* ")
	body = strings.TrimPrefix(body, "• ")
	return body
}
