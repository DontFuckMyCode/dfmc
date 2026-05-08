package cli

// cli_config_helpers.go — config-file IO + dotted-path tree walkers
// shared by every config subcommand. The subcommand handlers in
// cli_config_show.go / _set.go / _sync.go / _edit.go all read or
// write through these.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"gopkg.in/yaml.v3"
)

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
