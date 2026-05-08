package cli

// cli_config_set.go — `config set` handler. Writes one dotted-path
// value into the active config file (project by default, ~/.dfmc/
// with --global), then triggers an engine ReloadConfig. On reload
// failure the previous file bytes are restored so a typo can't
// brick the session.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runConfigSet(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dfmc config set [--global] <path> <value>")
		return 2
	}
	keyPath := strings.TrimSpace(fs.Args()[0])
	rawValue := strings.Join(fs.Args()[1:], " ")
	parsedValue, err := parseConfigValue(rawValue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config set parse error: %v\n", err)
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
		return 1
	}
	targetPath := projectConfigPath(cwd)
	if *global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
		return 1
	}
	if err := setConfigPath(currentMap, keyPath, parsedValue); err != nil {
		fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
		return 1
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		fmt.Fprintf(os.Stderr, "config set error: %v\n", err)
		return 1
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		fmt.Fprintf(os.Stderr, "config reload failed, reverted change: %v\n", err)
		return 1
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"status":      "ok",
			"path":        keyPath,
			"config_file": targetPath,
		})
		return 0
	}
	fmt.Printf("Updated %s in %s\n", keyPath, targetPath)
	return 0
}
