package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultDirName = ".dfmc"
	DefaultVersion = 1
)

type LoadOptions struct {
	GlobalPath  string
	ProjectPath string
	CWD         string
	// DataDirPath, when non-empty, overrides the default ~/.dfmc/data path.
	// Intended for multi-instance deployments where each project has its
	// own SQLite store to avoid file-lock contention.
	DataDirPath string
}

func LoadWithOptions(opts LoadOptions) (*Config, error) {
	cfg := DefaultConfig()

	globalPath := opts.GlobalPath
	if globalPath == "" {
		globalPath = filepath.Join(UserConfigDir(), "config.yaml")
	}
	if err := loadYAML(globalPath, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load global config: %w", err)
	}
	// Silent on not-exist: defaults from DefaultConfig() (including primary:minimax)
	// are preserved. Any other parse error still returns.
	cfg.normalizeAliases()
	globalHooks := cloneHooksConfig(cfg.Hooks)
	allowProjectHooks := cfg.Hooks.AllowProject

	projectPath := opts.ProjectPath
	projectRoot := FindProjectRoot(opts.CWD)
	if projectPath == "" {
		if projectRoot != "" {
			projectPath = filepath.Join(projectRoot, DefaultDirName, "config.yaml")
		}
	} else if projectRoot == "" {
		projectRoot = filepath.Dir(filepath.Dir(projectPath))
	}
	// security: refuse to merge hooks from group/world-writable project configs.
	// A co-tenant who can make the file group/world-writable could otherwise
	// inject hook commands. Global hooks are still loaded — only project hooks
	// from an insecure file are discarded. This supplements the existing
	// AllowProject flag; both must pass for project hooks to load.
	if projectPath != "" {
		if err := loadYAML(projectPath, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load project config: %w", err)
		}
		cfg.normalizeAliases()
		projectHooksSecure := isProjectConfigSecure(projectPath)
		if !allowProjectHooks || !projectHooksSecure {
			cfg.Hooks = globalHooks
		}
	}

	dotEnv, err := loadDotEnv(projectRoot)
	if err != nil {
		return nil, err
	}
	cfg.applyEncryptedProviderKeys()
	cfg.applyEnv(dotEnv)
	cfg.applyTelegramEnv()
	cfg.Providers.Profiles = MergeProviderProfilesFromModelsDev(cfg.Providers.Profiles, nil, ModelsDevMergeOptions{})
	if catalog, err := LoadModelsDevCatalog(ModelsDevCachePath()); err == nil {
		cfg.Providers.Profiles = MergeProviderProfilesFromModelsDev(cfg.Providers.Profiles, catalog, ModelsDevMergeOptions{})
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// isProjectConfigSecure returns true unless the project config file is
// group-writable or world-writable. Dumping hooks from a group/world-writable
// config file is dangerous because any co-tenant who can make the file
// group/world-writable could inject arbitrary shell commands via hook entries.
// On Windows the POSIX group/world bits are not meaningful — Go simulates 0o666
// for any read-write file regardless of ACLs — so we check the DACL instead.
func isProjectConfigSecure(path string) bool {
	if runtime.GOOS == "windows" {
		return isWindowsSecureACL(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return true // file doesn't exist — no risk
	}
	mode := info.Mode().Perm()
	return mode&0020 == 0 && mode&0002 == 0
}

// isWindowsSecureACL lives in config_windows.go (build-tagged for Windows).

func cloneHooksConfig(in HooksConfig) HooksConfig {
	out := HooksConfig{
		AllowProject: in.AllowProject,
		Entries:      map[string][]HookEntry{},
	}
	for event, entries := range in.Entries {
		cp := make([]HookEntry, len(entries))
		copy(cp, entries)
		out.Entries[event] = cp
	}
	return out
}

func (c *Config) normalizeAliases() {
	if c == nil {
		return
	}
	if c.Security.Sandbox.AllowCommand != nil {
		c.Security.Sandbox.AllowShell = *c.Security.Sandbox.AllowCommand
		c.Security.Sandbox.AllowCommand = nil
	}
}

func loadYAML(path string, out *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	const maxConfigSize = 1 << 20 // 1 MB — prevents memory exhaustion from maliciously large configs
	if len(data) > maxConfigSize {
		return fmt.Errorf("%s is %d bytes (max 1 MB); refusing to parse", path, len(data))
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func UserConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return DefaultDirName
	}
	return filepath.Join(home, DefaultDirName)
}

func FindProjectRoot(start string) string {
	cwd := start
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return ""
		}
	}

	markers := []string{
		DefaultDirName,
		".git",
		"go.mod",
		"package.json",
		"Cargo.toml",
		"pyproject.toml",
	}

	dir := cwd
	for {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return cwd
}

func (c *Config) DataDir() string {
	if c.DataDirPath != "" {
		return c.DataDirPath
	}
	return filepath.Join(UserConfigDir(), "data")
}

func ModelsDevCachePath() string {
	return filepath.Join(UserConfigDir(), "cache", "models.dev.json")
}

// ProjectLearnedPatternsDir returns the .dfmc/ directory path for project-local
// learned patterns (e.g., projectRoot/.dfmc/learned_patterns/).
// Returns empty string if ProjectRoot is not set.
func (c *Config) ProjectLearnedPatternsDir() string {
	if c.ProjectRoot == "" {
		return ""
	}
	return filepath.Join(c.ProjectRoot, ".dfmc", "learned_patterns")
}

// SetProjectRoot sets the project root path. Used by the engine to locate
// project-local resources (e.g., .dfmc/ directory).
func (c *Config) SetProjectRoot(path string) {
	c.ProjectRoot = path
}

func (c *Config) PluginDir() string {
	if strings.TrimSpace(c.Plugins.Directory) != "" {
		return c.Plugins.Directory
	}
	return filepath.Join(UserConfigDir(), "plugins")
}

// GetKey returns the API key for the named provider. Returns "" if the
// provider is not configured.
func (c *Config) GetKey(provider string) string {
	if c == nil || c.Providers.Profiles == nil {
		return ""
	}
	prof, ok := c.Providers.Profiles[provider]
	if !ok {
		return ""
	}
	return prof.APIKey
}

// SetKey updates (or inserts) the API key for the named provider,
// expanding the Providers.Profiles map as needed.
func (c *Config) SetKey(provider, key string) {
	if c == nil {
		return
	}
	if c.Providers.Profiles == nil {
		c.Providers.Profiles = map[string]ModelConfig{}
	}
	prof, ok := c.Providers.Profiles[provider]
	if !ok {
		prof = ModelConfig{}
	}
	prof.APIKey = key
	c.Providers.Profiles[provider] = prof
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
