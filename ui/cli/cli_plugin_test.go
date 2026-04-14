package cli

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAndRemovePluginFile(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	src := filepath.Join(root, "sample.py")
	if err := os.WriteFile(src, []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	info, err := installPluginFile(pluginDir, src, "my-plugin", false)
	if err != nil {
		t.Fatalf("installPluginFile: %v", err)
	}
	if !info.Installed {
		t.Fatalf("expected installed=true: %#v", info)
	}
	if !strings.EqualFold(info.Name, "my-plugin") {
		t.Fatalf("unexpected plugin name: %s", info.Name)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatalf("installed path missing: %v", err)
	}

	removed, err := removeInstalledPlugin(pluginDir, "my-plugin")
	if err != nil {
		t.Fatalf("removeInstalledPlugin: %v", err)
	}
	if strings.TrimSpace(removed) == "" {
		t.Fatalf("expected removed path")
	}
	if _, err := os.Stat(removed); !os.IsNotExist(err) {
		t.Fatalf("expected plugin file removed, got err=%v", err)
	}
}

func TestInstallPluginRejectsUnsupportedExt(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	src := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	_, err := installPluginFile(pluginDir, src, "", false)
	if err == nil {
		t.Fatal("expected unsupported extension error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unsupported plugin file extension") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePathWithinBaseRejectsEscape(t *testing.T) {
	root := t.TempDir()
	_, err := resolvePathWithinBase(root, filepath.Join(root, "..", "outside"))
	if err == nil {
		t.Fatal("expected path escape error")
	}
}

func TestDiscoverPluginsUsesManifest(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}

	dir := filepath.Join(pluginDir, "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	manifest := "" +
		"name: alpha-plugin\n" +
		"version: 1.2.3\n" +
		"type: script\n" +
		"entry: run.py\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	items := discoverPlugins(pluginDir, []string{"alpha-plugin"})
	if len(items) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(items))
	}
	p := items[0]
	if p.Name != "alpha-plugin" {
		t.Fatalf("unexpected name: %s", p.Name)
	}
	if p.Version != "1.2.3" || p.Type != "script" || p.Entry != "run.py" {
		t.Fatalf("unexpected manifest fields: %#v", p)
	}
	if !p.Enabled {
		t.Fatalf("expected plugin enabled via config match")
	}
}

func TestInstallPluginFromURL(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte("console.log('plugin')"))
	}))
	defer ts.Close()

	info, err := installPluginFile(pluginDir, ts.URL+"/plugin.mjs", "url-plugin", false)
	if err != nil {
		t.Fatalf("installPluginFile(url): %v", err)
	}
	if !strings.EqualFold(info.Name, "url-plugin") {
		t.Fatalf("unexpected plugin name: %s", info.Name)
	}
	if !strings.HasSuffix(strings.ToLower(info.Path), ".mjs") {
		t.Fatalf("expected .mjs target path, got: %s", info.Path)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatalf("installed plugin missing: %v", err)
	}
}

func TestInstallPluginFromZipWithManifestEntry(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	zipPath := filepath.Join(root, "alpha.zip")

	files := map[string]string{
		"alpha/plugin.yaml": "name: alpha\nversion: 1.0.0\ntype: script\nentry: run.py\n",
		"alpha/run.py":      "print('hello')\n",
	}
	if err := writeZipFile(zipPath, files); err != nil {
		t.Fatalf("write zip: %v", err)
	}

	info, err := installPluginFile(pluginDir, zipPath, "", false)
	if err != nil {
		t.Fatalf("installPluginFile(zip): %v", err)
	}
	if !info.Installed {
		t.Fatalf("expected installed=true")
	}
	if info.Name != "alpha" {
		t.Fatalf("unexpected name: %s", info.Name)
	}
	if info.Entry != "run.py" {
		t.Fatalf("unexpected entry: %s", info.Entry)
	}
	if info.Manifest == "" {
		t.Fatal("expected manifest path")
	}
	if st, err := os.Stat(filepath.Join(info.Path, "run.py")); err != nil || st.IsDir() {
		t.Fatalf("expected extracted run.py, err=%v", err)
	}
}

func TestInstallPluginFromZipRejectsUnsafeManifestEntry(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	zipPath := filepath.Join(root, "evil.zip")

	files := map[string]string{
		"evil/plugin.yaml": "name: evil\ntype: script\nentry: ../run.py\n",
		"evil/run.py":      "print('x')\n",
	}
	if err := writeZipFile(zipPath, files); err != nil {
		t.Fatalf("write zip: %v", err)
	}

	_, err := installPluginFile(pluginDir, zipPath, "", false)
	if err == nil {
		t.Fatal("expected unsafe manifest entry error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "manifest entry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeZipFile(path string, files map[string]string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
