package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigPaths_GlobalAlwaysComputed asserts the helper returns
// some global path even on a host that hasn't created one yet — so
// callers (startup, doctor) can stat-and-skip rather than guessing
// at the location. Project may be empty if cwd isn't inside a
// project root, but global is always derivable.
func TestConfigPaths_GlobalAlwaysComputed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)
	g, _ := ConfigPaths("")
	if g == "" {
		t.Fatalf("global config path must be derivable from UserConfigDir, got empty")
	}
	if !strings.HasSuffix(filepath.ToSlash(g), "/config.yaml") {
		t.Fatalf("global path %q should end in config.yaml", g)
	}
}

// TestConfigPaths_ProjectFromCWD pins the project-side resolution:
// if cwd is inside a directory tree containing .dfmc/, the project
// path is that tree's .dfmc/config.yaml. Without a marker the
// project path is empty (callers must tolerate that).
func TestConfigPaths_ProjectFromCWD(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)

	root := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(filepath.Join(root, DefaultDirName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Sub-directory of the project — FindProjectRoot must walk up.
	deep := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}

	_, p := ConfigPaths(deep)
	want := filepath.Join(root, DefaultDirName, "config.yaml")
	if p != want {
		t.Fatalf("project path = %q, want %q", p, want)
	}
	// We don't pin the "outside any project" branch because the
	// CI runner's cwd may itself be inside an ancestor project tree
	// (e.g. the temp dir root may have a stray .dfmc), and
	// FindProjectRoot walks up indefinitely. The project-found
	// branch above is the load-bearing assertion.
}
