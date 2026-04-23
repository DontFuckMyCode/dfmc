// Config CLI subcommands: list, get, set, sync-models, edit. Extracted
// from cli_admin.go. Everything here revolves around the YAML config
// file — reading it into a map[string]any tree, walking it by dotted
// path, redacting sensitive values, and writing it back.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"gopkg.in/yaml.v3"
)

func runConfig(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"list"}
	}

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("config list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		raw := fs.Bool("raw", false, "show sensitive values")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		cfgMap, err := configToMap(eng.Config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config list error: %v\n", err)
			return 1
		}
		out := sanitizeConfigValue(cfgMap, "", !*raw)
		if jsonMode {
			mustPrintJSON(out)
			return 0
		}
		data, err := yaml.Marshal(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config list error: %v\n", err)
			return 1
		}
		fmt.Print(string(data))
		return 0

	case "get":
		fs := flag.NewFlagSet("config get", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		raw := fs.Bool("raw", false, "show sensitive values")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dfmc config get [--raw] <path>")
			return 2
		}
		keyPath := strings.TrimSpace(fs.Args()[0])
		cfgMap, err := configToMap(eng.Config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config get error: %v\n", err)
			return 1
		}
		value, ok := getConfigPath(cfgMap, keyPath)
		if !ok {
			fmt.Fprintf(os.Stderr, "config path not found: %s\n", keyPath)
			return 1
		}
		out := sanitizeConfigValue(value, keyPath, !*raw)
		if jsonMode {
			_ = printJSON(map[string]any{
				"path":  keyPath,
				"value": out,
			})
			return 0
		}
		switch v := out.(type) {
		case string:
			fmt.Println(v)
		default:
			data, err := yaml.Marshal(v)
			if err != nil {
				fmt.Fprintf(os.Stderr, "config get error: %v\n", err)
				return 1
			}
			fmt.Print(string(data))
		}
		return 0

	case "set":
		fs := flag.NewFlagSet("config set", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
		if err := fs.Parse(args[1:]); err != nil {
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

	case "sync-models":
		fs := flag.NewFlagSet("config sync-models", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
		apiURL := fs.String("url", config.DefaultModelsDevAPIURL, "models.dev catalog url")
		rewriteBaseURL := fs.Bool("rewrite-base-url", true, "replace provider base_url values from models.dev")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config sync-models error: %v\n", err)
			return 1
		}
		targetPath := projectConfigPath(cwd)
		if *global {
			targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
		}

		catalog, err := config.FetchModelsDevCatalog(ctx, *apiURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config sync-models fetch error: %v\n", err)
			return 1
		}
		if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
			fmt.Fprintf(os.Stderr, "config sync-models cache error: %v\n", err)
			return 1
		}

		cloned, err := cloneConfig(eng.Config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config sync-models error: %v\n", err)
			return 1
		}
		beforeProfiles := map[string]config.ModelConfig{}
		for name, prof := range cloned.Providers.Profiles {
			beforeProfiles[name] = prof
		}
		cloned.Providers.Profiles = config.MergeProviderProfilesFromModelsDev(cloned.Providers.Profiles, catalog, config.ModelsDevMergeOptions{
			RewriteBaseURL: *rewriteBaseURL,
		})
		if strings.TrimSpace(cloned.Providers.Primary) == "" {
			cloned.Providers.Primary = eng.Config.Providers.Primary
		}
		if err := cloned.Save(targetPath); err != nil {
			fmt.Fprintf(os.Stderr, "config sync-models save error: %v\n", err)
			return 1
		}
		if err := eng.ReloadConfig(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "config sync-models reload error: %v\n", err)
			return 1
		}

		changes := diffProviderProfiles(beforeProfiles, cloned.Providers.Profiles)
		if jsonMode {
			_ = printJSON(map[string]any{
				"status":       "ok",
				"config_file":  targetPath,
				"cache_file":   config.ModelsDevCachePath(),
				"providers":    changes,
				"provider_n":   len(changes),
				"catalog_url":  strings.TrimSpace(*apiURL),
				"rewrite_base": *rewriteBaseURL,
			})
			return 0
		}
		fmt.Printf("Synced %d provider profile(s) from %s into %s\n", len(changes), strings.TrimSpace(*apiURL), targetPath)
		for _, line := range changes {
			fmt.Printf("- %s\n", line)
		}
		return 0

	case "edit":
		fs := flag.NewFlagSet("config edit", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "edit ~/.dfmc/config.yaml")
		if err := fs.Parse(args[1:]); err != nil {
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
		cmdArgs := append(editorParts[1:], targetPath)
		cmd := exec.Command(editorParts[0], cmdArgs...)
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

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc config [list|get|set|sync-models|edit]")
		return 2
	}
}

func projectConfigPath(cwd string) string {
	root := config.FindProjectRoot(cwd)
	if strings.TrimSpace(root) == "" {
		root = cwd
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml")
}

func cloneConfig(cfg *config.Config) (*config.Config, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := &config.Config{}
	if err := yaml.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

func diffProviderProfiles(before, after map[string]config.ModelConfig) []string {
	names := make([]string, 0, len(after))
	for name := range after {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]string, 0, len(names))
	for _, name := range names {
		prev, ok := before[name]
		curr := after[name]
		if ok &&
			prev.Model == curr.Model &&
			prev.BaseURL == curr.BaseURL &&
			prev.MaxTokens == curr.MaxTokens &&
			prev.MaxContext == curr.MaxContext &&
			prev.Protocol == curr.Protocol {
			continue
		}
		out = append(out, fmt.Sprintf("%s => model=%s protocol=%s max_context=%d max_tokens=%d base_url=%s",
			name,
			blankFallback(curr.Model, "-"),
			blankFallback(curr.Protocol, "-"),
			curr.MaxContext,
			curr.MaxTokens,
			blankFallback(curr.BaseURL, "(default)"),
		))
	}
	return out
}

func configToMap(cfg *config.Config) (map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func loadConfigFileMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	out := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return out, nil
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveConfigFileMap(path string, data map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	blob, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o644)
}

func parseConfigValue(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	var v any
	if err := yaml.Unmarshal([]byte(s), &v); err == nil {
		return v, nil
	}

	if b, err := strconv.ParseBool(s); err == nil {
		return b, nil
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	return raw, nil
}

func getConfigPath(root map[string]any, path string) (any, bool) {
	parts := splitConfigPath(path)
	if len(parts) == 0 {
		return root, true
	}
	var current any = root
	for _, part := range parts {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			current = node[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func setConfigPath(root map[string]any, path string, value any) error {
	parts := splitConfigPath(path)
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}
	current := root
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, exists := current[part]
		if !exists {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("path segment %q is not an object", strings.Join(parts[:i+1], "."))
		}
		current = nextMap
	}
	current[parts[len(parts)-1]] = value
	return nil
}

func splitConfigPath(path string) []string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func sanitizeConfigValue(value any, path string, enabled bool) any {
	if !enabled {
		return value
	}
	if isSensitivePath(path) {
		return "***REDACTED***"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			nextPath := k
			if path != "" {
				nextPath = path + "." + k
			}
			out[k] = sanitizeConfigValue(inner, nextPath, enabled)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			nextPath := strconv.Itoa(i)
			if path != "" {
				nextPath = path + "." + nextPath
			}
			out[i] = sanitizeConfigValue(inner, nextPath, enabled)
		}
		return out
	default:
		return v
	}
}

func isSensitivePath(path string) bool {
	if path == "" {
		return false
	}
	parts := splitConfigPath(path)
	if len(parts) == 0 {
		return false
	}
	key := strings.ToLower(parts[len(parts)-1])
	switch key {
	case "api_key", "apikey", "secret", "secret_key", "client_secret", "password", "passphrase", "token":
		return true
	}
	return strings.HasSuffix(key, "_token")
}
