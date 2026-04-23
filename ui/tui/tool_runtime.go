// Tool-runtime plumbing for the TUI: tea.Cmd factories that load panel
// data or invoke tools via the engine, plus the formatters and param
// parsers that shape results for the Tools panel. Extracted from
// tui.go. No Model dependency — everything here operates on plain
// engine.Engine / toolruntime.Result values.

package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

func loadStatusCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return statusLoadedMsg{}
		}
		return statusLoadedMsg{status: eng.Status()}
	}
}

func loadWorkspaceCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return workspaceLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		diff, err := gitWorkingDiff(root, 120_000)
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		changed, err := gitChangedFiles(root, 12)
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		return workspaceLoadedMsg{diff: diff, changed: changed}
	}
}

func loadFilesCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return filesLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		files, err := listProjectFiles(root, 5000)
		return filesLoadedMsg{files: files, err: err}
	}
}

func loadFilePreviewCmd(eng *engine.Engine, rel string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return filePreviewLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		content, size, err := readProjectFile(root, rel, 32_000)
		return filePreviewLoadedMsg{path: rel, content: content, size: size, err: err}
	}
}

func runToolCmd(ctx context.Context, eng *engine.Engine, name string, params map[string]any) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return toolRunMsg{name: name, params: params, err: fmt.Errorf("engine is nil")}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		res, err := eng.CallTool(ctx, name, params)
		return toolRunMsg{name: name, params: params, result: res, err: err}
	}
}

func formatToolResultForPanel(name string, params map[string]any, res toolruntime.Result) string {
	lines := []string{
		fmt.Sprintf("Tool: %s", name),
		fmt.Sprintf("Success: %t", res.Success),
	}
	if len(params) > 0 {
		lines = append(lines, "Params: "+formatToolParams(params))
	}
	if res.DurationMs > 0 {
		lines = append(lines, fmt.Sprintf("Duration: %dms", res.DurationMs))
	}
	if res.Truncated {
		lines = append(lines, "Output: truncated")
	}
	if warnings := toolResultWarnings(name, res); len(warnings) > 0 {
		lines = append(lines, warnings...)
	}
	output := strings.TrimSpace(res.Output)
	if output == "" {
		output = "(no text output)"
	}
	lines = append(lines, "", output)
	return strings.Join(lines, "\n")
}

func toolResultWarnings(name string, res toolruntime.Result) []string {
	if !strings.EqualFold(strings.TrimSpace(name), "apply_patch") || res.Data == nil {
		return nil
	}
	files := coerceToolResultFileEntries(res.Data["files"])
	if len(files) == 0 {
		return nil
	}
	rejected := 0
	fuzzyFiles := make([]string, 0, len(files))
	for _, file := range files {
		rejected += toolResultInt(file, "hunks_rejected")
		if offsets := toolResultIntSlice(file, "fuzzy_offsets"); len(offsets) > 0 {
			path := strings.TrimSpace(toolResultString(file, "path"))
			if path == "" {
				path = "(unknown path)"
			}
			fuzzyFiles = append(fuzzyFiles, fmt.Sprintf("%s %v", path, offsets))
		}
	}
	warnings := make([]string, 0, 2)
	if rejected > 0 {
		warnings = append(warnings, fmt.Sprintf("Warning: %d hunk(s) were rejected. Re-read the file and regenerate the patch before retrying.", rejected))
	}
	if len(fuzzyFiles) > 0 {
		warnings = append(warnings, "Warning: fuzzy anchors were used: "+strings.Join(fuzzyFiles, "; ")+". Review the touched file before continuing.")
	}
	return warnings
}

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

func formatToolErrorForPanel(name string, params map[string]any, res toolruntime.Result, err error) string {
	lines := []string{
		fmt.Sprintf("Tool: %s", name),
		"Success: false",
	}
	if len(params) > 0 {
		lines = append(lines, "Params: "+formatToolParams(params))
	}
	if res.DurationMs > 0 {
		lines = append(lines, fmt.Sprintf("Duration: %dms", res.DurationMs))
	}
	lines = append(lines, "Error: "+err.Error())
	output := strings.TrimSpace(res.Output)
	if output != "" {
		lines = append(lines, "", output)
	}
	return strings.Join(lines, "\n")
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
		case r == '"' || r == '\'':
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
