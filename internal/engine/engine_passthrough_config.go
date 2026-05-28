package engine

// engine_passthrough_config.go — engine-level config setters and the
// hot-reload pipeline. ReloadConfig swaps Providers/Tools atomically
// under e.mu.Lock; old tools are closed AFTER the lock drops so a
// long-running Close on one set of tools never blocks Status() on the
// other side.
//
// The auto-reload path (maybeAutoReloadProjectConfig) is called from
// engine_ask before each Ask so a config edit takes effect on the next
// turn without a manual /reload.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func (e *Engine) SetVerbose(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verbose = v
}

func (e *Engine) ReloadConfig(cwd string) error {
	cfg, err := config.LoadWithOptions(config.LoadOptions{CWD: cwd})
	if err != nil {
		return err
	}
	projectRoot := config.FindProjectRoot(cwd)
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = strings.TrimSpace(e.ProjectRoot)
	}
	providers, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		return err
	}
	e.attachProviderObservers(providers)
	newTools := tools.New(tools.ToToolsConfigSubset(cfg))
	newTools.SetSubagentRunner(e)
	// Re-attach the rest of initToolingStack's wiring. Without these, a
	// reload silently disables: TodoWrite persistence (falls back to in-
	// memory; todos vanish on restart), codemap-backed tool lookups, and
	// every MCP bridge tool. Storage may be nil under degraded startup —
	// guard so reload doesn't panic on `dfmc help` etc.
	if e.Storage != nil {
		newTools.SetTaskStore(taskstore.NewStore(e.Storage.DB()))
	}
	if e.CodeMap != nil {
		newTools.SetCodemap(e.CodeMap)
	}
	if mcpErr := loadMCPClients(cfg, newTools); mcpErr != nil {
		if e.AppLog != nil {
			e.AppLog.Warn("mcp clients reload failed", map[string]any{"error": mcpErr.Error()})
		}
	}
	if toolReasoningEnabledForConfig(cfg) {
		newTools.SetReasoningPublisher(func(toolName, reason string) {
			e.EventBus.Publish(Event{
				Type:   "tool:reasoning",
				Source: "engine",
				Payload: map[string]any{
					"tool":   toolName,
					"reason": reason,
				},
			})
		})
	}

	e.mu.Lock()
	oldTools := e.Tools
	e.Config = cfg
	if strings.TrimSpace(projectRoot) != "" {
		e.ProjectRoot = projectRoot
	}
	e.Providers = providers
	e.Tools = newTools
	e.mu.Unlock()
	if oldTools != nil {
		if err := oldTools.Close(); err != nil {
			return fmt.Errorf("close old tools during reload: %w", err)
		}
	}
	e.refreshProjectConfigSnapshot(e.projectConfigPath())
	return nil
}

func (e *Engine) globalConfigPath() string {
	return filepath.Join(config.UserConfigDir(), "config.yaml")
}

func (e *Engine) projectConfigPath() string {
	if e == nil {
		return ""
	}
	root := strings.TrimSpace(e.ProjectRoot)
	if root == "" {
		return ""
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml")
}

func (e *Engine) refreshProjectConfigSnapshot(path string) {
	if e == nil {
		return
	}
	path = strings.TrimSpace(path)
	var modTime time.Time
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime()
		}
	}
	e.mu.Lock()
	e.configProjectPath = path
	e.configProjectModTime = modTime
	e.mu.Unlock()
}

func (e *Engine) maybeAutoReloadProjectConfig() error {
	if e == nil {
		return nil
	}
	path := e.projectConfigPath()
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			e.refreshProjectConfigSnapshot(path)
			return nil
		}
		return err
	}

	e.mu.RLock()
	lastPath := e.configProjectPath
	lastModTime := e.configProjectModTime
	e.mu.RUnlock()
	if path == lastPath && !info.ModTime().After(lastModTime) {
		return nil
	}

	if err := e.ReloadConfig(e.ProjectRoot); err != nil {
		if e.EventBus != nil {
			e.EventBus.Publish(Event{
				Type:   "config:reload:auto_failed",
				Source: "engine",
				Payload: map[string]any{
					"path":  path,
					"error": err.Error(),
				},
			})
		}
		return fmt.Errorf("auto-reload config: %w", err)
	}
	if e.EventBus != nil {
		e.EventBus.Publish(Event{
			Type:   "config:reload:auto",
			Source: "engine",
			Payload: map[string]any{
				"path":       path,
				"updated_at": info.ModTime().Unix(),
			},
		})
	}
	return nil
}
