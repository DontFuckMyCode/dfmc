// cli_plugin_install.go — everything that reads or writes the
// plugins directory on disk. Split out of cli_plugin_skill.go
// because the install / discovery / manifest / archive-download
// pipeline is heavier than the command dispatcher that calls it, and
// colocating them made the dispatcher file harder to skim. Five
// surfaces:
//
//   - discoverPlugins: scans pluginDir, merges manifest.yaml
//     metadata, reconciles against the enabled-list from config, and
//     returns a sorted merged view.
//   - installPluginFile: accepts a path OR http(s) URL, handles
//     archive expansion, sanitises the target name, enforces
//     --force, and validates the manifest before committing.
//     removeInstalledPlugin is its inverse (path-safe delete).
//   - resolvePluginSource / isHTTPPluginSource / downloadPluginSource:
//     let install accept URLs without a custom flag.
//   - readPluginManifest: tolerant YAML reader for plugin.yaml /
//     plugin.yml; returns false if the file is absent or structurally
//     empty (a totally-blank manifest shouldn't "count").
//   - updatePluginEnabled: round-trips through the user or project
//     config file to toggle the plugin's entry in plugins.enabled,
//     reverts on reload failure, and leaves unrelated keys untouched.
//
// sanitizePluginName and containsCI are small helpers used by the
// above; they stay here because nothing else reaches for them.

package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"gopkg.in/yaml.v3"
)

func discoverPlugins(pluginDir string, enabled []string) []pluginInfo {
	seen := map[string]pluginInfo{}
	entries, err := os.ReadDir(pluginDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			base := strings.TrimSuffix(name, filepath.Ext(name))
			path := filepath.Join(pluginDir, name)
			if e.IsDir() {
				base = name
			} else {
				ext := strings.ToLower(filepath.Ext(name))
				if !pluginFileExtSupported(ext) {
					continue
				}
			}
			info := pluginInfo{
				Name:      base,
				Path:      path,
				Installed: true,
				Enabled:   containsCI(enabled, base),
			}
			if mf, mfPath, ok := readPluginManifest(path); ok {
				if strings.TrimSpace(mf.Name) != "" {
					info.Name = strings.TrimSpace(mf.Name)
				}
				info.Version = strings.TrimSpace(mf.Version)
				info.Type = strings.TrimSpace(mf.Type)
				info.Entry = strings.TrimSpace(mf.Entry)
				info.Manifest = mfPath
				info.Enabled = info.Enabled || containsCI(enabled, info.Name)
			}
			key := strings.ToLower(strings.TrimSpace(info.Name))
			if key == "" {
				continue
			}
			seen[key] = info
		}
	}

	for _, name := range enabled {
		n := strings.TrimSpace(name)
		key := strings.ToLower(n)
		if n == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = pluginInfo{
			Name:      n,
			Installed: false,
			Enabled:   true,
		}
	}

	out := make([]pluginInfo, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func installPluginFile(pluginDir, sourcePath, nameOverride string, force bool) (pluginInfo, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return pluginInfo{}, fmt.Errorf("source path is required")
	}

	resolvedSource, cleanup, err := resolvePluginSource(sourcePath)
	if err != nil {
		return pluginInfo{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	srcAbs, err := filepath.Abs(resolvedSource)
	if err != nil {
		return pluginInfo{}, err
	}
	srcAbs, archiveCleanup, err := expandPluginSourceIfArchive(srcAbs)
	if err != nil {
		return pluginInfo{}, err
	}
	if archiveCleanup != nil {
		defer archiveCleanup()
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return pluginInfo{}, err
	}
	if !srcInfo.IsDir() {
		if !pluginSourceFileExtSupported(strings.ToLower(filepath.Ext(srcAbs))) {
			return pluginInfo{}, fmt.Errorf("unsupported plugin file extension: %s", filepath.Ext(srcAbs))
		}
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return pluginInfo{}, err
	}
	pluginDirAbs, err := filepath.Abs(pluginDir)
	if err != nil {
		return pluginInfo{}, err
	}

	targetName := strings.TrimSpace(nameOverride)
	if targetName == "" {
		if srcInfo.IsDir() {
			if mf, _, ok := readPluginManifest(srcAbs); ok && strings.TrimSpace(mf.Name) != "" {
				targetName = strings.TrimSpace(mf.Name)
			}
		}
		if srcInfo.IsDir() {
			if targetName == "" {
				targetName = filepath.Base(srcAbs)
			}
		} else {
			targetName = strings.TrimSuffix(filepath.Base(srcAbs), filepath.Ext(srcAbs))
		}
	}
	targetName = sanitizePluginName(targetName)
	if targetName == "" {
		return pluginInfo{}, fmt.Errorf("invalid plugin name")
	}

	targetPath := filepath.Join(pluginDirAbs, targetName)
	if !srcInfo.IsDir() {
		ext := filepath.Ext(srcAbs)
		if ext != "" {
			targetPath = targetPath + ext
		}
	}
	targetPath, err = resolvePathWithinBase(pluginDirAbs, targetPath)
	if err != nil {
		return pluginInfo{}, err
	}

	if _, err := os.Stat(targetPath); err == nil {
		if !force {
			return pluginInfo{}, fmt.Errorf("target already exists: %s (use --force)", targetPath)
		}
		if err := removePathSafe(pluginDirAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
	}

	if srcInfo.IsDir() {
		if err := copyDir(srcAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
		if err := validatePluginManifestEntry(targetPath); err != nil {
			_ = removePathSafe(pluginDirAbs, targetPath)
			return pluginInfo{}, err
		}
	} else {
		if err := copyFile(srcAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
	}

	info := pluginInfo{
		Name:      targetName,
		Path:      targetPath,
		Installed: true,
		Enabled:   false,
	}
	if srcInfo.IsDir() {
		if mf, mfPath, ok := readPluginManifest(targetPath); ok {
			info.Version = strings.TrimSpace(mf.Version)
			info.Type = strings.TrimSpace(mf.Type)
			info.Entry = strings.TrimSpace(mf.Entry)
			info.Manifest = mfPath
			if strings.TrimSpace(mf.Name) != "" && strings.TrimSpace(nameOverride) == "" {
				info.Name = strings.TrimSpace(mf.Name)
			}
		}
	}
	return info, nil
}

func removeInstalledPlugin(pluginDir, name string) (string, error) {
	items := discoverPlugins(pluginDir, nil)
	for _, item := range items {
		if !item.Installed || strings.TrimSpace(item.Path) == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
		if !strings.EqualFold(item.Name, name) && !strings.EqualFold(base, name) {
			continue
		}
		pluginDirAbs, err := filepath.Abs(pluginDir)
		if err != nil {
			return "", err
		}
		targetPath, err := resolvePathWithinBase(pluginDirAbs, item.Path)
		if err != nil {
			return "", err
		}
		if err := removePathSafe(pluginDirAbs, targetPath); err != nil {
			return "", err
		}
		return targetPath, nil
	}
	return "", nil
}

func resolvePluginSource(source string) (resolved string, cleanup func(), err error) {
	if isHTTPPluginSource(source) {
		path, err := downloadPluginSource(source)
		if err != nil {
			return "", nil, err
		}
		return path, func() { _ = os.Remove(path) }, nil
	}
	return source, nil, nil
}

func isHTTPPluginSource(source string) bool {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	if u == nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return strings.TrimSpace(u.Host) != ""
	default:
		return false
	}
}

func downloadPluginSource(src string) (string, error) {
	resp, err := http.Get(src) //nolint:gosec // plugin install intentionally fetches user-provided URL.
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

	ext := ".plugin"
	if u, err := url.Parse(src); err == nil {
		if e := strings.TrimSpace(filepath.Ext(u.Path)); e != "" {
			ext = e
		}
	}
	tmp, err := os.CreateTemp("", "dfmc-plugin-*"+ext)
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func readPluginManifest(path string) (pluginManifest, string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return pluginManifest{}, "", false
	}
	if !info.IsDir() {
		return pluginManifest{}, "", false
	}

	candidates := []string{
		filepath.Join(path, "plugin.yaml"),
		filepath.Join(path, "plugin.yml"),
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var mf pluginManifest
		if err := yaml.Unmarshal(data, &mf); err != nil {
			continue
		}
		if strings.TrimSpace(mf.Name) == "" &&
			strings.TrimSpace(mf.Version) == "" &&
			strings.TrimSpace(mf.Type) == "" &&
			strings.TrimSpace(mf.Entry) == "" {
			continue
		}
		return mf, candidate, true
	}
	return pluginManifest{}, "", false
}

func sanitizePluginName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, ":", "_")
	return name
}

func updatePluginEnabled(ctx context.Context, eng *engine.Engine, name string, enabled, global bool) error {
	_ = ctx
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	targetPath := projectConfigPath(cwd)
	if global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		return err
	}
	var list []string
	raw, _ := getConfigPath(currentMap, "plugins.enabled")
	switch arr := raw.(type) {
	case []any:
		for _, item := range arr {
			v := strings.TrimSpace(fmt.Sprint(item))
			if v != "" {
				list = append(list, v)
			}
		}
	case []string:
		for _, item := range arr {
			v := strings.TrimSpace(item)
			if v != "" {
				list = append(list, v)
			}
		}
	}

	if enabled {
		if !containsCI(list, name) {
			list = append(list, name)
		}
	} else {
		next := make([]string, 0, len(list))
		for _, item := range list {
			if !strings.EqualFold(item, name) {
				next = append(next, item)
			}
		}
		list = next
	}

	values := make([]any, 0, len(list))
	for _, item := range list {
		values = append(values, item)
	}
	if err := setConfigPath(currentMap, "plugins.enabled", values); err != nil {
		return err
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		return err
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		return fmt.Errorf("config reload failed, reverted: %w", err)
	}
	return nil
}

func containsCI(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
