package pluginexec

// client_spawn.go — process-spawn helpers used by Spawn: argv resolution
// (interpreter pick from manifest type or file extension), interpreter
// PATH lookup, and the minimal env allow-list. Sibling to client.go,
// which owns the Client + Spec types, the RPC roundtrip (Call/Close/
// readLoop/drainStderr), and the public Spawn entry point.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveArgv picks the interpreter and builds the argv slice for the
// given entry. Explicit `kind` wins over file extension.
func resolveArgv(entry, kind string, extra []string) ([]string, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		k = kindFromExt(entry)
	}
	switch k {
	case "exec", "binary", "executable":
		return append([]string{entry}, extra...), nil
	case "python", "py":
		interp := firstAvailable("python3", "python")
		if interp == "" {
			return nil, fmt.Errorf("python interpreter not found on PATH")
		}
		return append([]string{interp, entry}, extra...), nil
	case "node", "javascript", "js":
		interp := firstAvailable("node")
		if interp == "" {
			return nil, fmt.Errorf("node interpreter not found on PATH")
		}
		return append([]string{interp, entry}, extra...), nil
	case "shell", "sh", "bash":
		interp := firstAvailable("bash", "sh")
		if interp == "" {
			return nil, fmt.Errorf("bash/sh interpreter not found on PATH")
		}
		return append([]string{interp, entry}, extra...), nil
	default:
		return append([]string{entry}, extra...), nil
	}
}

func kindFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "node"
	case ".sh", ".bash":
		return "shell"
	case ".exe", "":
		return "exec"
	}
	return "exec"
}

func firstAvailable(candidates ...string) string {
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func buildEnv(extra []string) []string {
	passthrough := []string{
		"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TEMP", "TMP",
		"LANG", "LC_ALL", "LC_CTYPE",
	}
	base := make([]string, 0, len(passthrough)+len(extra)+1)
	for _, k := range passthrough {
		if v, ok := os.LookupEnv(k); ok {
			base = append(base, k+"="+v)
		}
	}
	base = append(base, "DFMC_PLUGIN=1")
	base = append(base, extra...)
	return base
}
