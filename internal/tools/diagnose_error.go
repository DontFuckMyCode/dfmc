package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DiagnoseErrorTool struct {
	engine *Engine
}

func NewDiagnoseErrorTool() *DiagnoseErrorTool { return &DiagnoseErrorTool{} }
func (t *DiagnoseErrorTool) Name() string       { return "diagnose_error" }
func (t *DiagnoseErrorTool) SetEngine(e *Engine) { t.engine = e }

func (t *DiagnoseErrorTool) Description() string {
	return "Analyze a tool failure and provide actionable suggestions based on the project context."
}

func (t *DiagnoseErrorTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "diagnose_error",
		Title:   "Error diagnosis",
		Summary: "Explains tool failures and suggests fixes.",
		Purpose: "Use when a tool (like edit_file or run_command) fails and you're not sure why or how to fix it. This tool checks for common pitfalls like path typos, permission issues, or environment mismatches.",
		Prompt: `Analyzes tool errors.
Args:
- tool: The name of the tool that failed.
- params: The parameters sent to the failed tool.
- error: The error message returned by the tool.

Returns a diagnostic report with suggested next steps.`,
		Risk: RiskRead,
		Tags: []string{"debug", "helper", "diagnosis"},
		Args: []Arg{
			{Name: "tool", Type: ArgString, Required: true, Description: "Name of the failed tool."},
			{Name: "params", Type: ArgObject, Description: "Parameters used in the failed call."},
			{Name: "error", Type: ArgString, Required: true, Description: "The error message."},
		},
		Idempotent: true,
	}
}

func (t *DiagnoseErrorTool) Execute(ctx context.Context, req Request) (Result, error) {
	toolName := asString(req.Params, "tool", "")
	errMsg := asString(req.Params, "error", "")
	params, _ := req.Params["params"].(map[string]any)

	var suggestions []string

	// Common Path Diagnosis
	if path := asString(params, "path", ""); path != "" {
		absPath := path
		if !filepath.IsAbs(path) {
			absPath = filepath.Join(req.ProjectRoot, path)
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			suggestions = append(suggestions, fmt.Sprintf("The file %q does not exist. Use list_dir or glob to find the correct path.", path))
			
			// Try to find similar files
			base := filepath.Base(path)
			similar := t.findSimilarFiles(req.ProjectRoot, base)
			if len(similar) > 0 {
				suggestions = append(suggestions, "Similar files found: "+strings.Join(similar, ", "))
			}
		}
	}

	// Tool-specific diagnosis
	switch toolName {
	case "edit_file":
		if strings.Contains(errMsg, "could not find old_string") {
			suggestions = append(suggestions, "The 'old_string' must be an EXACT literal match, including whitespace and indentation. Call read_file first to copy the exact block.")
		}
	case "run_command":
		if strings.Contains(errMsg, "not recognized as the name of a cmdlet") || strings.Contains(errMsg, "not found") {
			suggestions = append(suggestions, "The command might not be in the PATH or requires a specific environment. Try using a full path or check the environment with 'env' (on Unix) or 'set' (on Windows).")
		}
	}

	if len(suggestions) == 0 {
		suggestions = append(suggestions, "No specific diagnosis found. Try simplifying the parameters or checking the tool documentation with tool_help.")
	}

	output := "### Diagnostic Report\n"
	for _, s := range suggestions {
		output += "- " + s + "\n"
	}

	return Result{
		Output: output,
		Data: map[string]any{
			"tool":        toolName,
			"suggestions": suggestions,
		},
	}, nil
}

func (t *DiagnoseErrorTool) findSimilarFiles(root, filename string) []string {
	var matches []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(info.Name(), filename) {
			rel, _ := filepath.Rel(root, path)
			matches = append(matches, rel)
		}
		if len(matches) >= 5 {
			return filepath.SkipDir
		}
		return nil
	})
	return matches
}
