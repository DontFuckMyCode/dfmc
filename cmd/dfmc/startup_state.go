package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

func checkHookConfigPermissions() bool {
	// VULN-036: warn if config files are group/world-writable. A hostile
	// co-tenant on a shared host could inject hook commands.
	globalPath, projectPath := config.ConfigPaths("")
	for _, path := range []string{globalPath, projectPath} {
		if msg := hooks.CheckConfigPermissions(path); msg != "" {
			if !unsafeHooksOverrideEnabled() {
				fmt.Fprintf(os.Stderr, "[DFMC] ERROR: %s\n", msg)
				fmt.Fprintln(os.Stderr, "[DFMC] Refusing to run hooks from writable config. Fix file permissions or set DFMC_UNSAFE_HOOKS=1 to override.")
				return false
			}
			fmt.Fprintf(os.Stderr, "[DFMC] WARNING: %s (DFMC_UNSAFE_HOOKS=1 override active)\n", msg)
		}
	}
	return true
}

func autoInitProjectState(cfg *config.Config) {
	if cfg == nil {
		return
	}
	// Auto-init: if no storage exists at DataDir, run the init sequence
	// so `dfmc ask` just works in a new project without an explicit
	// `dfmc init` call first. Mirrors runInit but keeps the process alive.
	dd := cfg.DataDir()
	dbPath := filepath.Join(dd, "dfmc.db")
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		return
	}
	projectRoot := config.FindProjectRoot("")
	if projectRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectRoot = cwd
		}
	}
	if projectRoot == "" {
		return
	}
	dfmcDir := filepath.Join(projectRoot, ".dfmc")
	if mkdirErr := os.MkdirAll(dfmcDir, 0o755); mkdirErr != nil {
		return
	}
	localCfg := config.DefaultConfig()
	localCfg.DataDirPath = dd
	if cfgPathErr := localCfg.Save(filepath.Join(dfmcDir, "config.yaml")); cfgPathErr != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dfmcDir, "knowledge.json"), []byte("{}\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dfmcDir, "conventions.json"), []byte("{}\n"), 0o600)
	fmt.Fprintf(os.Stderr, "[DFMC] initialized project at %s\n", projectRoot)
}
