package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
}

func Load() (*Config, error) {
	return LoadWithOptions(LoadOptions{})
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
	if projectPath != "" {
		if err := loadYAML(projectPath, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load project config: %w", err)
		}
		cfg.normalizeAliases()
		if !allowProjectHooks {
			cfg.Hooks = globalHooks
		}
	}

	dotEnv, err := loadDotEnv(projectRoot)
	if err != nil {
		return nil, err
	}
	cfg.applyEnv(dotEnv)
	cfg.Providers.Profiles = MergeProviderProfilesFromModelsDev(cfg.Providers.Profiles, nil, ModelsDevMergeOptions{})
	if catalog, err := LoadModelsDevCatalog(ModelsDevCachePath()); err == nil {
		cfg.Providers.Profiles = MergeProviderProfilesFromModelsDev(cfg.Providers.Profiles, catalog, ModelsDevMergeOptions{})
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

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
	return filepath.Join(UserConfigDir(), "data")
}

func ModelsDevCachePath() string {
	return filepath.Join(UserConfigDir(), "cache", "models.dev.json")
}

func (c *Config) PluginDir() string {
	if strings.TrimSpace(c.Plugins.Directory) != "" {
		return c.Plugins.Directory
	}
	return filepath.Join(UserConfigDir(), "plugins")
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

