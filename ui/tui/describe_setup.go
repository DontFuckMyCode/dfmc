package tui

// describe_setup.go — `/setup` slash command renderer.
//
// Built specifically for the "her girişte neden /provider minimax
// yazıyorum" failure mode: the user types a slash command and the
// system snapshots the EXACT facts needed to understand and resolve
// the layering conflict. One screen, no panel hopping.
//
// The output is plain text (no styling) because it lands in the chat
// transcript via appendSystemMessage. Stays compact — first 6 lines
// must contain everything load-bearing so the user can act without
// reading further.

import (
	"fmt"
	"strings"
)

func (m Model) providerSetupSummary() string {
	var b strings.Builder

	resolved := strings.TrimSpace(m.currentProvider())
	resolvedModel := strings.TrimSpace(m.currentModel())
	if resolved == "" {
		resolved = "(none)"
	}
	if resolvedModel == "" {
		resolvedModel = "-"
	}

	b.WriteString("DFMC setup — provider config layering\n")
	b.WriteString("═══════════════════════════════════════\n\n")

	// Resolved (the truth) — what is the engine ACTUALLY using right
	// now. This is the single most important line.
	b.WriteString(fmt.Sprintf("Active   : %s / %s\n", resolved, resolvedModel))

	// Layering — what each file says about primary, side by side.
	userPrimary, projectPrimary, conflict := m.detectProviderConfigConflict()
	userPath, _ := m.userConfigPath()
	projectPath, _ := m.projectConfigPath()

	b.WriteString(fmt.Sprintf("User     : %s   (%s)\n", blankFallback(userPrimary, "—"), displayConfigPath(userPath)))
	b.WriteString(fmt.Sprintf("Project  : %s   (%s)\n", blankFallback(projectPrimary, "—"), displayConfigPath(projectPath)))

	// Save target — answers "if I click set primary, where does it go?"
	saveTarget, _ := m.configPathForScope(m.effectivePersistScope())
	b.WriteString(fmt.Sprintf("Saves to : %s\n", displayConfigPath(saveTarget)))

	// Save rule — provider settings are GLOBAL. They always land in
	// user-home. Project config is reserved for project-specific
	// stuff (drive routing, pipelines, hooks) — never providers.
	b.WriteString("\nSave rule: provider/model/key settings → user-home, ALWAYS.\n")

	// Conflict callout — when project config also defines providers
	// it WINS on load even though we never write there any more.
	// This is the source of "her girişte /provider X yazıyorum" —
	// the user changed minimax in the panel, panel wrote to
	// user-home, but next entry the project's stale primary
	// shadowed it. Make this loud and actionable.
	if conflict {
		b.WriteString("\n⚠ STALE PROJECT OVERRIDE\n")
		b.WriteString(fmt.Sprintf("   Project config still has providers.primary: %s\n", projectPrimary))
		b.WriteString(fmt.Sprintf("   Your user-home primary (%s) is being shadowed on load.\n", userPrimary))
		b.WriteString("   Run /setup clean to remove the project's providers block and let user-home win.\n")
	} else if userPrimary == "" && projectPrimary == "" {
		b.WriteString("\n⚠ No primary set anywhere. Use alt+P (provider picker) or /provider NAME.\n")
	} else if projectPrimary != "" && userPrimary == "" {
		b.WriteString("\n⚠ Project config defines providers but user-home does not.\n")
		b.WriteString("   Pick a provider via alt+P so it lands in user-home, then /setup clean to drop the project block.\n")
	}

	// API-key sanity for the resolved provider — second-most-likely
	// reason "I clicked set provider, but calls fail".
	b.WriteString("\n")
	if profile := m.providerProfile(resolved); !strings.EqualFold(resolved, "offline") && resolved != "(none)" {
		if !profile.Configured {
			b.WriteString(fmt.Sprintf("⚠ %s has no API key. ", resolved))
			if envVar := providerEnvVarLookup(resolved); envVar != "" {
				b.WriteString(fmt.Sprintf("Set %s in your .env, or add\n  providers.profiles.%s.api_key in config.yaml.\n", envVar, resolved))
			} else {
				b.WriteString(fmt.Sprintf("Add providers.profiles.%s.api_key in config.yaml.\n", resolved))
			}
		} else {
			b.WriteString(fmt.Sprintf("✓ %s is configured (api_key or base_url present).\n", resolved))
		}
	}

	b.WriteString("\nNext actions:\n")
	b.WriteString("  alt+m         model picker (one keystroke, auto-saves)\n")
	b.WriteString("  alt+P         provider picker\n")
	b.WriteString("  alt+p         providers list (read-only browse)\n")
	b.WriteString("  /provider X   switch + auto-save\n")
	b.WriteString("  f4            full Providers panel (api_key, base_url, max_context edits)\n")

	return b.String()
}
