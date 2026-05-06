// slash_agents.go — the /agents slash-command family. Surfaces the role
// catalog (system.role.* prompt overlays) plus the registered provider
// profiles, separating the two: roles describe a sub-agent's *personality*
// (planner / reviewer / researcher / …) while profiles bind a runtime
// (anthropic / openai / deepseek / …). Together they answer the user's
// "what sub-agents can I actually spawn?" question without making them
// grep config and prompt YAML.
//
//	/agents               → list (alias: /agents list)
//	/agents show <name>   → role body or provider profile detail
//
// Read-only, no provider call — safe on the offline/placeholder router.

package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

// agentRoleEntry collapses every overlay that targets a given role into one
// row. The most descriptive overlay (longest non-empty Description, falling
// back to the first sentence of the longest Body) wins for the summary.
type agentRoleEntry struct {
	Role    string
	Summary string
	Bodies  []agentRoleBody
}

type agentRoleBody struct {
	ID       string
	Task     string
	Priority int
	Body     string
}

// agentProfileEntry is one provider profile with the bits a user cares about
// when picking a sub-agent runtime: name, model, tool support, configured
// state.
type agentProfileEntry struct {
	Name       string
	Model      string
	Protocol   string
	Tools      bool
	Configured bool
	Active     bool
}

func (m Model) agentsSlash(args []string) string {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}

	roles := m.collectAgentRoles()
	profiles := m.collectAgentProfiles()

	switch sub {
	case "", "list", "ls":
		return formatAgentsList(roles, profiles)
	case "show", "describe", "cat":
		if len(rest) == 0 {
			return "Usage: /agents show <role-or-profile> — pass a name from /agents list."
		}
		target := strings.TrimSpace(rest[0])
		for _, r := range roles {
			if strings.EqualFold(r.Role, target) {
				return formatAgentRoleShow(r)
			}
		}
		for _, p := range profiles {
			if strings.EqualFold(p.Name, target) {
				return formatAgentProfileShow(p)
			}
		}
		return fmt.Sprintf("No role or profile named %q. Run /agents list to see what's registered.", target)
	default:
		return "agents: unknown subcommand. Try: list | show <name>"
	}
}

func (m Model) collectAgentRoles() []agentRoleEntry {
	lib := promptlib.New()
	if m.eng != nil {
		_ = lib.LoadOverrides(m.eng.ProjectRoot)
	}
	byRole := map[string]*agentRoleEntry{}
	for _, t := range lib.List() {
		role := strings.TrimSpace(t.Role)
		if role == "" || strings.EqualFold(role, "generalist") {
			continue
		}
		entry := byRole[role]
		if entry == nil {
			entry = &agentRoleEntry{Role: role}
			byRole[role] = entry
		}
		entry.Bodies = append(entry.Bodies, agentRoleBody{
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
	out := make([]agentRoleEntry, 0, len(byRole))
	for _, v := range byRole {
		sort.Slice(v.Bodies, func(i, j int) bool { return v.Bodies[i].Priority > v.Bodies[j].Priority })
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

func (m Model) collectAgentProfiles() []agentProfileEntry {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	out := make([]agentProfileEntry, 0, len(m.eng.Config.Providers.Profiles))
	for name, prof := range m.eng.Config.Providers.Profiles {
		entry := agentProfileEntry{
			Name:       name,
			Model:      strings.TrimSpace(prof.BestModel()),
			Protocol:   strings.TrimSpace(prof.Protocol),
			Configured: strings.TrimSpace(prof.APIKey) != "",
		}
		if m.eng.Providers != nil {
			if p, ok := m.eng.Providers.Get(name); ok && p != nil {
				entry.Tools = p.Hints().SupportsTools
				if entry.Model == "" {
					entry.Model = strings.TrimSpace(p.Model())
				}
			}
			if strings.EqualFold(name, m.eng.Providers.Primary()) {
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

func formatAgentsList(roles []agentRoleEntry, profiles []agentProfileEntry) string {
	var b strings.Builder
	b.WriteString("▸ Sub-agent catalog\n\n")

	b.WriteString("Roles (orchestrate / delegate_task / drive `role` field):\n")
	if len(roles) == 0 {
		b.WriteString("  (none — only the generalist baseline is loaded)\n")
	} else {
		for _, r := range roles {
			summary := strings.TrimSpace(r.Summary)
			if summary == "" {
				summary = "(no description)"
			}
			fmt.Fprintf(&b, "  · %-18s %s\n", r.Role, truncateSingleLine(summary, 80))
		}
	}

	b.WriteString("\nProfiles (sub-agent `model` / `provider_tag` field, `★` = active):\n")
	if len(profiles) == 0 {
		b.WriteString("  (none registered — check providers.profiles in config.yaml)\n")
	} else {
		for _, p := range profiles {
			tools := "⚠ no-tools"
			if p.Tools {
				tools = "✓ tools"
			}
			cfg := "⚠ no-key"
			if p.Configured {
				cfg = "✓ ready"
			}
			star := "  "
			if p.Active {
				star = "★ "
			}
			model := p.Model
			if model == "" {
				model = "?"
			}
			fmt.Fprintf(&b, "  %s%-14s %-26s %-8s %-10s %s\n", star, p.Name, model, p.Protocol, tools, cfg)
		}
	}

	b.WriteString("\nTip: /agents show <name> for details. Sub-agents inherit the active profile by default; pass `model:\"openai\"` (or another profile) on delegate_task / orchestrate stages to switch.")
	return b.String()
}

func formatAgentRoleShow(r agentRoleEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ Role: %s\n", r.Role)
	if r.Summary != "" {
		b.WriteString("  summary: ")
		b.WriteString(r.Summary)
		b.WriteByte('\n')
	}
	b.WriteString("  overlays loaded (highest priority first):\n")
	for _, body := range r.Bodies {
		fmt.Fprintf(&b, "    · %s  task=%s  priority=%d\n", body.ID, defaultStr(body.Task, "general"), body.Priority)
	}
	if len(r.Bodies) > 0 {
		b.WriteString("\n  primary overlay body:\n")
		body := strings.TrimSpace(r.Bodies[0].Body)
		for _, line := range strings.Split(body, "\n") {
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatAgentProfileShow(p agentProfileEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ Profile: %s", p.Name)
	if p.Active {
		b.WriteString("  ★ active")
	}
	b.WriteByte('\n')
	if p.Model != "" {
		fmt.Fprintf(&b, "  model:      %s\n", p.Model)
	}
	if p.Protocol != "" {
		fmt.Fprintf(&b, "  protocol:   %s\n", p.Protocol)
	}
	tools := "no — falls back to text-bridge or refuses sub-agent dispatch"
	if p.Tools {
		tools = "yes — eligible for delegate_task / orchestrate"
	}
	fmt.Fprintf(&b, "  tool calls: %s\n", tools)
	cfg := "missing API key — set via env var or providers.profiles." + p.Name + ".api_key"
	if p.Configured {
		cfg = "API key set"
	}
	fmt.Fprintf(&b, "  configured: %s\n", cfg)
	return strings.TrimRight(b.String(), "\n")
}

// firstSentence pulls the first sentence-like fragment out of a body so a
// dense YAML overlay still gives a usable one-line summary in the catalog.
// Splits on the first ". " or newline and strips leading list markers /
// extra whitespace.
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
