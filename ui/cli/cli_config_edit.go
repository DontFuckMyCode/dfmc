package cli

// cli_config_edit.go — `config edit` handler. Drops the user into
// $VISUAL / $EDITOR (or notepad/vi) on the active config file, then
// reloads the engine after the editor exits.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runConfigEdit(eng *engine.Engine, args []string) int {
	fs := flag.NewFlagSet("config edit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	global := fs.Bool("global", false, "edit ~/.dfmc/config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config edit error: %v\n", err)
		return 1
	}
	targetPath := projectConfigPath(cwd)
	if *global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
		if err := saveConfigFileMap(targetPath, map[string]any{}); err != nil {
			fmt.Fprintf(os.Stderr, "config edit error: %v\n", err)
			return 1
		}
	}

	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}
	editorParts := strings.Fields(editor)
	if len(editorParts) == 0 {
		fmt.Fprintln(os.Stderr, "config edit error: no editor configured")
		return 1
	}
	for _, part := range editorParts {
		if strings.ContainsAny(part, "\x00\r\n") {
			fmt.Fprintln(os.Stderr, "config edit error: editor contains illegal control characters")
			return 1
		}
	}
	editorBin, err := exec.LookPath(editorParts[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config edit error: editor %q not found on PATH: %v\n", editorParts[0], err)
		return 1
	}
	cmdArgs := append(editorParts[1:], targetPath)
	cmd := exec.Command(editorBin, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "config edit error: %v\n", err)
		return 1
	}

	if err := eng.ReloadConfig(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "config reload failed after edit: %v\n", err)
		return 1
	}
	return 0
}
