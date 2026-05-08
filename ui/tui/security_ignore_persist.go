package tui

// security_ignore_persist.go — load/save the per-project Security
// panel ignore (whitelist) set. Phase J item 1 persistence layer.
//
// File: <projectRoot>/.dfmc/security_ignores.yaml
// Shape: { ignored: [<fingerprint>, <fingerprint>, ...] }
//
// Fingerprints are the same hashes the toggle path produces (stable
// across reruns when file/line/rule match). Persistence is best-effort
// — read errors fall back to "no ignores" and a notice; write errors
// surface as a notice but don't block the toggle. The TUI never owns
// the engine boundary, so the file lives next to .dfmc/config.yaml.

import (
	"path/filepath"
	"sort"
)

const securityIgnoresFileName = ".dfmc/security_ignores.yaml"

// securityIgnoresPath returns the absolute path to the project's
// ignores file. Empty string when no project root is configured (in
// that case persistence is a no-op — fingerprints stay in memory).
func (m Model) securityIgnoresPath() string {
	root := m.projectRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, securityIgnoresFileName)
}

// loadSecurityIgnoresFromDisk reads the project's ignore file and
// returns the parsed fingerprint set. Missing file returns an empty
// set without an error so a fresh project starts clean.
func loadSecurityIgnoresFromDisk(path string) (map[string]bool, error) {
	out := map[string]bool{}
	if path == "" {
		return out, nil
	}
	doc, err := readYAMLDocOrEmpty(path)
	if err != nil {
		return out, err
	}
	raw, ok := doc["ignored"]
	if !ok {
		return out, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return out, nil
	}
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out[s] = true
		}
	}
	return out, nil
}

// saveSecurityIgnoresToDisk persists the current ignore set. The
// list is sorted so the file diffs cleanly across runs.
func saveSecurityIgnoresToDisk(path string, ignored map[string]bool) error {
	if path == "" {
		return nil
	}
	keys := make([]string, 0, len(ignored))
	for k, on := range ignored {
		if on {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	doc := map[string]any{}
	if len(keys) > 0 {
		list := make([]any, len(keys))
		for i, k := range keys {
			list[i] = k
		}
		doc["ignored"] = list
	}
	return writeYAMLDocAtomically(path, doc)
}
