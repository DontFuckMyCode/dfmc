package tui

// chat_commands_keys.go — `/key` slash command. The user explicitly
// asked us to phase out the project `.env` file as a key store
// ("Bizim userhome altında config tutmamız lazım … keylerimizi orada
// tutman gerekiyor, .env işine son vermek lazım"), so this command
// makes user-home `~/.dfmc/config.yaml` the FIRST-CLASS surface for
// API keys without making the user hand-edit YAML.
//
//   /key                       — show the status of every known provider key
//   /key list                  — alias for the bare command
//   /key set <provider> <api>  — write the key to ~/.dfmc/config.yaml and
//                                live-update the running engine
//   /key clear <provider>      — strip the key from ~/.dfmc/config.yaml
//   /key migrate               — read project .env, migrate ALL provider keys
//                                to ~/.dfmc/config.yaml in one shot
//
// All writes go through writeProviderAPIKeyToUserConfig, which only
// touches `providers.profiles.<name>.api_key` — model, protocol, etc.
// stay untouched. After a successful write we call reloadEngineConfig
// so the new key takes effect on the next provider call without a TUI
// restart.

// File layout: this file owns runKeyCommand dispatcher + render/collect
// + applyProviderAPIKeyInMemory + projectRootForKeyOps + maskAPIKey +
// known/isKnownProvider + providerForEnvVar. Persistence (writeProvider
// APIKeyToUserConfig + clearProviderAPIKeyFromUserConfig + migrate
// DotEnvKeysToUserConfig + readProjectDotEnvKeys + readUserConfigAPIKeys
// + readYAMLDocOrEmpty + writeYAMLDocAtomically) lives in
// chat_commands_keys_io.go.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// keyStatusRow describes the source + masked value of a single provider's
// API key — built from process env, project .env, user-home config, and
// the in-memory engine config (which reflects the merged result).
type keyStatusRow struct {
	Provider string
	EnvVar   string
	Source   string // "process_env" | "dot_env" | "user_config" | "project_config" | "missing"
	Masked   string // last-4 view of the key, or "—" when missing
}

func (m Model) runKeyCommand(args []string) (tea.Model, tea.Cmd, bool) {
	if len(args) == 0 || strings.EqualFold(strings.TrimSpace(args[0]), "list") {
		return m.appendSystemMessage(m.renderKeyStatus()), nil, true
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]
	switch sub {
	case "set":
		if len(rest) < 2 {
			m.notice = "/key set needs <provider> <api_key>"
			return m.appendSystemMessage("Usage: /key set <provider> <api_key>\nExample: /key set anthropic sk-ant-api03-…\nKeys land in ~/.dfmc/config.yaml — no .env required."), nil, true
		}
		provider := strings.ToLower(strings.TrimSpace(rest[0]))
		apiKey := strings.TrimSpace(strings.Join(rest[1:], " "))
		if !isKnownProvider(provider) {
			m.notice = "/key set: unknown provider " + provider
			return m.appendSystemMessage("Unknown provider: " + provider + "\nKnown: " + strings.Join(knownKeyProviders(), ", ")), nil, true
		}
		path, err := m.writeProviderAPIKeyToUserConfig(provider, apiKey)
		if err != nil {
			m.notice = "/key set failed: " + err.Error()
			return m.appendSystemMessage("Failed to save key: " + err.Error()), nil, true
		}
		// Live-update the running engine so the next provider call
		// uses the new key without a TUI restart.
		m.applyProviderAPIKeyInMemory(provider, apiKey)
		reloadHint := ""
		if err := m.reloadEngineConfig(); err != nil {
			reloadHint = "\n(reload warning: " + err.Error() + " — restart TUI if calls still fail)"
		}
		m.notice = fmt.Sprintf("Saved %s key → %s", provider, displayConfigPath(path))
		return m.appendSystemMessage(fmt.Sprintf(
			"Saved %s API key → %s (masked: %s)%s",
			provider, displayConfigPath(path), maskAPIKey(apiKey), reloadHint,
		)), loadStatusCmd(m.eng), true
	case "clear", "unset", "remove", "delete":
		if len(rest) == 0 {
			m.notice = "/key clear needs <provider>"
			return m.appendSystemMessage("Usage: /key clear <provider>"), nil, true
		}
		provider := strings.ToLower(strings.TrimSpace(rest[0]))
		if !isKnownProvider(provider) {
			m.notice = "/key clear: unknown provider " + provider
			return m.appendSystemMessage("Unknown provider: " + provider + "\nKnown: " + strings.Join(knownKeyProviders(), ", ")), nil, true
		}
		path, removed, err := m.clearProviderAPIKeyFromUserConfig(provider)
		if err != nil {
			m.notice = "/key clear failed: " + err.Error()
			return m.appendSystemMessage("Failed to clear key: " + err.Error()), nil, true
		}
		if !removed {
			return m.appendSystemMessage(fmt.Sprintf("No %s key found in %s — nothing to clear.", provider, displayConfigPath(path))), nil, true
		}
		m.applyProviderAPIKeyInMemory(provider, "")
		_ = m.reloadEngineConfig()
		m.notice = fmt.Sprintf("Cleared %s key from %s", provider, displayConfigPath(path))
		return m.appendSystemMessage(fmt.Sprintf("Cleared %s API key from %s.", provider, displayConfigPath(path))), loadStatusCmd(m.eng), true
	case "migrate":
		report := m.migrateDotEnvKeysToUserConfig()
		_ = m.reloadEngineConfig()
		return m.appendSystemMessage(report), loadStatusCmd(m.eng), true
	}
	m.notice = "/key: unknown sub-command " + sub
	return m.appendSystemMessage("Usage:\n  /key [list]\n  /key set <provider> <api_key>\n  /key clear <provider>\n  /key migrate"), nil, true
}

// renderKeyStatus prints a compact table: provider · env-var · source ·
// masked. Source telegraphs WHERE the live key is coming from so the
// user can decide whether to migrate / clear / overwrite.
func (m Model) renderKeyStatus() string {
	rows := m.collectKeyStatusRows()
	if len(rows) == 0 {
		return "No known providers."
	}
	var b strings.Builder
	b.WriteString("API key status — phase out .env: keys live in ~/.dfmc/config.yaml.\n\n")
	fmt.Fprintf(&b, "  %-10s  %-20s  %-14s  %s\n", "PROVIDER", "ENV VAR", "SOURCE", "KEY")
	b.WriteString("  ")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteByte('\n')
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-10s  %-20s  %-14s  %s\n", r.Provider, blankFallback(r.EnvVar, "—"), r.Source, r.Masked)
	}
	b.WriteString("\nNext: `/key set <provider> <api_key>` writes to ~/.dfmc/config.yaml.")
	b.WriteString("\nMigrating from .env? Run `/key migrate`.")
	return b.String()
}

func (m Model) collectKeyStatusRows() []keyStatusRow {
	providers := knownKeyProviders()
	out := make([]keyStatusRow, 0, len(providers))
	dotEnvKeys := m.readProjectDotEnvKeys()
	userKeys := m.readUserConfigAPIKeys()
	for _, p := range providers {
		envVar := config.EnvVarForProvider(p)
		processVal := strings.TrimSpace(os.Getenv(envVar))
		row := keyStatusRow{Provider: p, EnvVar: envVar, Source: "missing", Masked: "—"}
		switch {
		case processVal != "":
			row.Source = "process_env"
			row.Masked = maskAPIKey(processVal)
		case strings.TrimSpace(userKeys[p]) != "":
			row.Source = "user_config"
			row.Masked = maskAPIKey(userKeys[p])
		case strings.TrimSpace(dotEnvKeys[envVar]) != "":
			row.Source = "dot_env"
			row.Masked = maskAPIKey(dotEnvKeys[envVar])
		}
		out = append(out, row)
	}
	return out
}

// writeProviderAPIKeyToUserConfig surgically updates ONLY the
// `providers.profiles.<name>.api_key` field in ~/.dfmc/config.yaml.
// Other fields (model, protocol, base_url, etc.) are preserved
// verbatim. The file is created if absent. Returns the resolved path
// so the caller can show "saved → …" feedback.
// applyProviderAPIKeyInMemory updates the live engine config so a key
// change takes effect immediately. Empty key clears the field.
func (m Model) applyProviderAPIKeyInMemory(providerName, apiKey string) {
	if m.eng == nil || m.eng.Config == nil {
		return
	}
	if m.eng.Config.Providers.Profiles == nil {
		m.eng.Config.Providers.Profiles = map[string]config.ModelConfig{}
	}
	prof := m.eng.Config.Providers.Profiles[providerName]
	prof.APIKey = strings.TrimSpace(apiKey)
	m.eng.Config.Providers.Profiles[providerName] = prof
}

func (m Model) projectRootForKeyOps() string {
	if m.eng != nil && strings.TrimSpace(m.eng.ProjectRoot) != "" {
		return m.eng.ProjectRoot
	}
	if strings.TrimSpace(m.status.ProjectRoot) != "" {
		return m.status.ProjectRoot
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// maskAPIKey returns a fixed-format last-4 view of an api key. We
// always show the last 4 chars (or fewer if the key is shorter) so
// the user can confirm "yes this is the right key" without leaking
// secrets to anyone reading the screen behind them.
func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "—"
	}
	if len(key) <= 4 {
		return strings.Repeat("•", len(key))
	}
	return strings.Repeat("•", min(len(key)-4, 12)) + key[len(key)-4:]
}

func knownKeyProviders() []string {
	out := []string{"anthropic", "openai", "google", "deepseek", "kimi", "minimax", "zai", "alibaba"}
	sort.Strings(out)
	return out
}

func isKnownProvider(name string) bool {
	for _, p := range knownKeyProviders() {
		if p == name {
			return true
		}
	}
	return false
}

// providerForEnvVar reverses the canonical env-var → provider mapping
// for the .env reader. Used to decide whether a key in the .env file
// is one we know how to migrate.
func providerForEnvVar(envVar string) string {
	switch strings.ToUpper(strings.TrimSpace(envVar)) {
	case "ANTHROPIC_API_KEY":
		return "anthropic"
	case "OPENAI_API_KEY":
		return "openai"
	case "GOOGLE_AI_API_KEY":
		return "google"
	case "DEEPSEEK_API_KEY":
		return "deepseek"
	case "KIMI_API_KEY", "MOONSHOT_API_KEY":
		return "kimi"
	case "MINIMAX_API_KEY":
		return "minimax"
	case "ZAI_API_KEY":
		return "zai"
	case "ALIBABA_API_KEY":
		return "alibaba"
	}
	return ""
}
