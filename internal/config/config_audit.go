package config

// config_audit.go — surface-level audit of where DFMC's config files
// live, so startup and `dfmc doctor` can flag group/world-writable
// configs before a hostile co-tenant inherits DFMC's tool authority
// via injected hooks (VULN-036).
//
// The actual mode check lives in internal/hooks.CheckConfigPermissions
// (it grew up next to the hook dispatcher). This helper just enumerates
// the candidate paths the same way Load() does, so callers don't have
// to recompute UserConfigDir / FindProjectRoot / ".dfmc/config.yaml".

import (
	"path/filepath"
)

// ConfigPaths returns the absolute paths of the global and project
// config files DFMC will read at Load time. Either may be empty: no
// global if the user never created one, no project if cwd isn't
// inside a project root.
//
// `cwd` is the working directory used to walk upward for a project
// root. Pass an empty string to use os.Getwd() via FindProjectRoot.
func ConfigPaths(cwd string) (global string, project string) {
	if dir := UserConfigDir(); dir != "" {
		global = filepath.Join(dir, "config.yaml")
	}
	if root := FindProjectRoot(cwd); root != "" {
		project = filepath.Join(root, DefaultDirName, "config.yaml")
	}
	return global, project
}
