package cli

// cli_plugin_install_remote.go — remote-fetch + config-toggle side of
// the plugin installer. resolvePluginSource lets `dfmc plugin install`
// take an HTTP(S) URL transparently; downloadPluginSource pulls it
// with a hard timeout + size cap. updatePluginEnabled round-trips the
// project or user-home config file to flip the plugin's slot in
// plugins.enabled with revert-on-reload-failure. Sibling to
// cli_plugin_install.go which owns the on-disk plumbing
// (discoverPlugins, installPluginFile, removeInstalledPlugin,
// readPluginManifest, sanitizePluginName, containsCI).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

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
	// A dedicated client with an explicit timeout. http.DefaultClient
	// has no timeout — a slow or deliberately-stalled server would
	// hang the CLI until the user Ctrl+C'd. The redirect cap defends
	// against redirect loops on top of Go's default 10-hop limit.
	client := &http.Client{
		Timeout: pluginDownloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}
	resp, err := client.Get(src) //nolint:gosec // plugin install intentionally fetches user-provided URL; timeout + size cap enforced below.
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = tmp.Close() }()

	// Cap the copy so a malicious host can't stream gigabytes into
	// /tmp. +1 lets us tell "hit the cap" apart from "finished at
	// exactly the cap" — if we read more than the cap, reject.
	limited := io.LimitReader(resp.Body, pluginDownloadMaxSize+1)
	written, err := io.Copy(tmp, limited)
	if err != nil {
		return "", err
	}
	if written > pluginDownloadMaxSize {
		return "", fmt.Errorf("download exceeded %d bytes — refusing", pluginDownloadMaxSize)
	}
	return tmp.Name(), nil
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
