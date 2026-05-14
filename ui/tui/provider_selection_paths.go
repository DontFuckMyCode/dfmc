package tui

// provider_selection_paths.go — path resolution and scope rules for the
// provider/model persistence layer. The user-vs-project layering rule
// is the load-bearing decision here:
//
//   PROVIDER + MODEL + API-KEY settings ALWAYS go to user-home.
//
// They're personal preferences, not project state — keeping them in
// per-project `.dfmc/config.yaml` files leads to "why doesn't my pick
// stick?" confusion every time the user changes directories. Project
// config (`<project>/.dfmc/config.yaml`) stays reserved for genuinely
// project-scoped concerns like drive routing, pipelines, tool gating,
// and hooks — those continue to use projectConfigPath() directly.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) projectConfigPath() (string, error) {
	root := ""
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		root = strings.TrimSpace(m.status.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("project root unavailable: %w", err)
		}
		root = cwd
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml"), nil
}

// userConfigPath returns ~/.dfmc/config.yaml — the user-global config
// that survives across projects. Used as the DEFAULT save location for
// provider preferences (primary, fallback, model selection) so the
// user doesn't have to re-pick a model every time they switch
// directories.
func (m Model) userConfigPath() (string, error) {
	dir := config.UserConfigDir()
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("user config dir unavailable (HOME not set)")
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// persistScope determines whether saved settings go to the user-global
// config (~/.dfmc/config.yaml — survives across projects, the default)
// or to the project-local config (<project>/.dfmc/config.yaml — only
// for cases where the user wants per-project overrides).
type persistScope int

const (
	persistScopeUser persistScope = iota
	persistScopeProject
)

func (m Model) configPathForScope(scope persistScope) (string, error) {
	switch scope {
	case persistScopeProject:
		return m.projectConfigPath()
	default:
		return m.userConfigPath()
	}
}

// effectivePersistScope picks the right save target for panel-driven
// provider/model toggles. The rule (final, after iteration):
//
//	PROVIDER + MODEL + API-KEY settings ALWAYS go to user-home.
//
// These are personal preferences, not project state — keeping them in
// per-project `.dfmc/config.yaml` files leads to "why doesn't my
// minimax pick stick?" confusion every time the user changes
// directories. The user's load-bearing rule: "bu projeye özel bir
// settings değilse ana global tüm ayarların user home içinde .dfmc/
// içinde olması gerekiyor". Provider preferences are not project-
// specific, so they always land in `~/.dfmc/config.yaml`.
//
// Project config (`<project>/.dfmc/config.yaml`) is reserved for
// genuinely project-specific things: drive routing, pipelines, tool
// gating, hooks. Those continue to use projectConfigPath() directly
// — they don't go through this resolver.
//
// If a project's config DOES have a `providers:` block (typically
// from older DFMC versions or migrated yaml), it still wins on load
// per the merge order, but we never WRITE provider settings there
// any more. detectProviderConfigConflict() + the startup notice +
// /setup tell the user about the layering so they can clean the
// project file manually.
func (m Model) effectivePersistScope() persistScope {
	return persistScopeUser
}

// detectProviderConfigConflict returns the user-home primary, the
// project primary, and whether they differ. Used at TUI startup to
// flag the layering conflict that confuses users into typing
// `/provider X` every entry — e.g. user saved `minimax` to global but
// the project's config.yaml hard-codes `anthropic`. Returns ok=false
// when there's no conflict (only one layer has a primary, or both
// agree, or neither has a providers block).
func (m Model) detectProviderConfigConflict() (userPrimary, projectPrimary string, conflict bool) {
	userPath, err := m.userConfigPath()
	if err != nil {
		return "", "", false
	}
	projectPath, err := m.projectConfigPath()
	if err != nil {
		return "", "", false
	}
	userPrimary = readProvidersPrimary(userPath)
	projectPrimary = readProvidersPrimary(projectPath)
	if userPrimary == "" || projectPrimary == "" {
		return userPrimary, projectPrimary, false
	}
	conflict = !strings.EqualFold(userPrimary, projectPrimary)
	return userPrimary, projectPrimary, conflict
}

// readProvidersPrimary parses a config.yaml and returns providers.primary
// (trimmed). Returns "" when the file is missing, malformed, or has no
// providers/primary entry.
func readProvidersPrimary(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	providers, ok := toStringAnyMap(doc["providers"])
	if !ok {
		return ""
	}
	primary, _ := providers["primary"].(string)
	return strings.TrimSpace(primary)
}

// displayConfigPath shortens an absolute config path for user-facing
// notices — replaces the user's home dir with `~` so the line stays
// readable in narrow terminals.
func displayConfigPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	home = filepath.ToSlash(strings.TrimSpace(home))
	if home == "" {
		return path
	}
	if strings.HasPrefix(strings.ToLower(path), strings.ToLower(home)) {
		return "~" + path[len(home):]
	}
	return path
}
