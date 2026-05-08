package tui

// tool_runtime_helpers.go — small helpers used by the tool-runtime
// surface: result/data coercion (untyped JSON-shaped maps coming back
// from tools.Engine into typed slices/strings/ints), the
// "key=value … key='quoted'" param-string parser used by the Tools
// panel, and the param-formatter that turns a params map into a
// stable canonical render line.
//
// Sibling of tool_runtime.go which keeps the tea.Cmd factories
// (loadStatusCmd / loadWorkspaceCmd / loadFilesCmd / loadFilePreviewCmd /
// runToolCmd) and the result/error formatters used by the Tools panel.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

func coerceToolResultFileEntries(raw any) []map[string]any {
	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func toolResultString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func toolResultInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case []int:
		return len(v)
	}
	return 0
}

func toolResultIntSlice(m map[string]any, key string) []int {
	if m == nil {
		return nil
	}
	switch v := m[key].(type) {
	case []int:
		return append([]int(nil), v...)
	case []any:
		out := make([]int, 0, len(v))
		for _, item := range v {
			switch n := item.(type) {
			case int:
				out = append(out, n)
			case int32:
				out = append(out, int(n))
			case int64:
				out = append(out, int(n))
			case float64:
				out = append(out, int(n))
			}
		}
		return out
	default:
		return nil
	}
}

func formatToolParams(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, params[k]))
	}
	return strings.Join(parts, ", ")
}

// parseToolParamString turns a "key=value key='quoted value'" line
// from the Tools-panel composer into a typed param map. Strings are
// auto-coerced (true/false → bool, integer literals → int).
func parseToolParamString(raw string) (map[string]any, error) {
	tokens, err := splitToolParamTokens(raw)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("param string is empty")
	}
	params := make(map[string]any, len(tokens))
	for _, token := range tokens {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			return nil, fmt.Errorf("expected key=value token, got %q", token)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("empty param key in %q", token)
		}
		params[key] = coerceToolParamValue(value)
	}
	return params, nil
}

// splitToolParamTokens performs a small quote-aware tokeniser so a value
// like name="hello world" parses as a single token. Only `"` opens a
// quoted segment — single quote (`'`) is treated as a literal rune so
// contractions like "what's"/"don't" pass through unchanged in chat
// slash-command arguments. Unterminated quotes are reported as an
// error rather than silently swallowing the trailing value.
func splitToolParamTokens(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var (
		tokens  []string
		current strings.Builder
		quote   rune
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted value")
	}
	flush()
	return tokens, nil
}

func coerceToolParamValue(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	switch strings.ToLower(trimmed) {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.Atoi(trimmed); err == nil {
		return i
	}
	return trimmed
}

func toolResultRelativePath(eng *engine.Engine, res toolruntime.Result) string {
	if eng == nil {
		return ""
	}
	raw := strings.TrimSpace(fmt.Sprint(res.Data["path"]))
	if raw == "" || raw == "<nil>" {
		return ""
	}
	rel, err := filepath.Rel(eng.ProjectRoot, raw)
	if err != nil {
		return filepath.ToSlash(raw)
	}
	return filepath.ToSlash(rel)
}

func toolResultWorkspaceChanged(res toolruntime.Result) bool {
	if res.Data == nil {
		return false
	}
	switch v := res.Data["workspace_changed"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}
