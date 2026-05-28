package tui

// setup_clean.go — `/setup clean` strips the `providers:` block from
// the project's `.dfmc/config.yaml`. Provider preferences are user-
// scoped now (effectivePersistScope always returns user-home), so a
// stale project providers block is the source of "I picked minimax
// in the panel but next entry it loaded anthropic again". This
// command gives the user one keystroke to fix it.
//
// We're conservative: we DON'T touch any other top-level key in the
// project config, and we DON'T delete the file — pipelines, drive
// routing, hooks, and tool gating may all still legitimately live
// there. Only the `providers:` map is dropped.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// cleanProjectProvidersBlock removes the `providers:` block from the
// project's .dfmc/config.yaml and returns a chat-ready report
// describing what changed. Safe to call when the file doesn't exist
// (returns "no project config to clean") and when there's no
// providers block (returns "nothing to clean").
func (m Model) cleanProjectProvidersBlock() string {
	path, err := m.projectConfigPath()
	if err != nil {
		return "/setup clean: cannot resolve project config path: " + err.Error()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "/setup clean: no project config at " + displayConfigPath(path) + " — nothing to do."
		}
		return "/setup clean: read failed: " + err.Error()
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "/setup clean: project config is empty — nothing to do."
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "/setup clean: parse failed: " + err.Error()
	}
	providersBefore, hadProviders := doc["providers"]
	if !hadProviders {
		return "/setup clean: project config has no `providers:` block — nothing to drop. (User-home settings already win on load.)"
	}
	delete(doc, "providers")

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "/setup clean: re-marshal failed: " + err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "/setup clean: mkdir failed: " + err.Error()
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "/setup clean: write failed: " + err.Error()
	}

	// Reload the engine so the change takes effect immediately —
	// otherwise the user has to restart the TUI and we'd be back to
	// the original "every entry needs /provider X" complaint.
	reloadHint := ""
	if err := m.reloadEngineConfig(); err != nil {
		reloadHint = "\n(reload failed: " + err.Error() + " — restart the TUI to pick up the change)"
	}

	summary := summarizeStrippedProviders(providersBefore)
	var b strings.Builder
	b.WriteString("/setup clean: stripped `providers:` block from ")
	b.WriteString(displayConfigPath(path))
	b.WriteString(".\n")
	if summary != "" {
		b.WriteString("  Removed: ")
		b.WriteString(summary)
		b.WriteByte('\n')
	}
	b.WriteString("\nUser-home preferences (~/.dfmc/config.yaml) now win on load.")
	b.WriteString(reloadHint)
	return b.String()
}

// summarizeStrippedProviders renders a human one-line summary of what
// the provider block contained — primary, fallback chain, named
// profiles. Helps the user confirm "yes, that's the stale stuff I
// wanted gone".
func summarizeStrippedProviders(raw any) string {
	m, ok := toStringAnyMap(raw)
	if !ok || len(m) == 0 {
		return ""
	}
	parts := []string{}
	if primary, ok := m["primary"].(string); ok && strings.TrimSpace(primary) != "" {
		parts = append(parts, "primary="+strings.TrimSpace(primary))
	}
	if rawFb, ok := m["fallback"].([]any); ok && len(rawFb) > 0 {
		fbs := make([]string, 0, len(rawFb))
		for _, f := range rawFb {
			if s, ok := f.(string); ok && strings.TrimSpace(s) != "" {
				fbs = append(fbs, strings.TrimSpace(s))
			}
		}
		if len(fbs) > 0 {
			parts = append(parts, "fallback=["+strings.Join(fbs, ",")+"]")
		}
	}
	if profiles, ok := toStringAnyMap(m["profiles"]); ok && len(profiles) > 0 {
		names := make([]string, 0, len(profiles))
		for name := range profiles {
			names = append(names, name)
		}
		parts = append(parts, fmt.Sprintf("profiles=%d (%s)", len(names), strings.Join(names, ",")))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}
