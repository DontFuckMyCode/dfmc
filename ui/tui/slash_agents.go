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
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) agentsSlash(args []string) string {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}

	if m.eng == nil {
		return "Engine unavailable."
	}
	cat := m.eng.Agents()
	roles := cat.Roles
	profiles := cat.Profiles

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

// FormatAgentsList renders the public AgentCatalog as the multi-line block
// shown in /agents and `dfmc agents`. Exported so the CLI surface can reuse
// the exact same rendering without re-implementing.
func FormatAgentsList(roles []engine.AgentRole, profiles []engine.AgentProfile) string {
	return formatAgentsList(roles, profiles)
}

// FormatAgentRoleShow renders one role.
func FormatAgentRoleShow(r engine.AgentRole) string { return formatAgentRoleShow(r) }

// FormatAgentProfileShow renders one profile.
func FormatAgentProfileShow(p engine.AgentProfile) string { return formatAgentProfileShow(p) }

func formatAgentsList(roles []engine.AgentRole, profiles []engine.AgentProfile) string {
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

func formatAgentRoleShow(r engine.AgentRole) string {
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

func formatAgentProfileShow(p engine.AgentProfile) string {
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

