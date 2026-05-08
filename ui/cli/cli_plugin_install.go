// cli_plugin_install.go — on-disk plumbing for `dfmc plugin install`:
// scan the plugins directory + reconcile against the enabled-list
// (discoverPlugins), copy a path/URL into pluginDir with archive
// expansion + manifest validation (installPluginFile), and the
// path-safe inverse (removeInstalledPlugin). readPluginManifest is
// the tolerant YAML reader; sanitizePluginName and containsCI are
// small helpers. URL fetch, redirect/timeout/size guards, and the
// updatePluginEnabled config round-trip live in
// cli_plugin_install_remote.go.

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Hard caps for plugin downloads. 60s is long enough for a slow proxy
// but short enough that a user stuck on a broken URL learns about it
// quickly. 256 MiB is comfortably above any real plugin binary (for
// reference: go+tree-sitter dfmc itself builds to ~80 MiB) while
// keeping a malicious or misconfigured host from filling /tmp.
const (
	pluginDownloadTimeout = 60 * time.Second
	pluginDownloadMaxSize = 256 * 1024 * 1024
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

func containsCI(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
