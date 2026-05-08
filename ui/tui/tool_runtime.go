// Tool-runtime plumbing for the TUI: tea.Cmd factories that load panel
// data or invoke tools via the engine, plus the formatters and param
// parsers that shape results for the Tools panel. Extracted from
// tui.go. No Model dependency — everything here operates on plain
// engine.Engine / toolruntime.Result values.

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

const defaultGitDiffTimeout = 30 * time.Second

func tuiGitDiffTimeout(eng *engine.Engine) time.Duration {
	if eng == nil || eng.Config == nil || eng.Config.TUI.GitDiffTimeoutSeconds <= 0 {
		return defaultGitDiffTimeout
	}
	return time.Duration(eng.Config.TUI.GitDiffTimeoutSeconds) * time.Second
}

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
		diff, err := gitWorkingDiff(root, 120_000, tuiGitDiffTimeout(eng))
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

// Result/data coercion (coerceToolResultFileEntries, toolResultString,
// toolResultInt, toolResultIntSlice, toolResultRelativePath,
// toolResultWorkspaceChanged) and the param string parser
// (parseToolParamString, splitToolParamTokens, coerceToolParamValue,
// formatToolParams) live in tool_runtime_helpers.go.
