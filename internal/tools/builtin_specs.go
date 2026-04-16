package tools

// Spec() methods for the builtin tools. The specs are the provider-facing
// contract — changing an Arg name or Required flag here is a public surface
// change. Keep them terse; model costs a token per word in every prompt they
// show up in.

func (t *ReadFileTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "read_file",
		Title:   "Read file",
		Summary: "Read a text file, optionally scoped to a line range.",
		Purpose: "Fetch file contents for analysis. Prefer narrow line ranges for large files.",
		Risk:    RiskRead,
		Tags:    []string{"filesystem", "read", "code"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "line_start", Type: ArgInteger, Description: "1-indexed start line (default 1).", Default: 1},
			{Name: "line_end", Type: ArgInteger, Description: "1-indexed end line (inclusive).", Default: 200},
		},
		Returns:    "Text segment of the file plus {path, line_start, line_end, line_count}.",
		Examples:   []string{`{"path":"main.go","line_start":1,"line_end":80}`},
		Idempotent: true,
		CostHint:   "cheap",
	}
}

func (t *WriteFileTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "write_file",
		Title:   "Write file",
		Summary: "Create or overwrite a text file (requires prior read_file for existing files).",
		Purpose: "Materialize new files or rewrite existing ones. For small edits prefer edit_file.",
		Risk:    RiskWrite,
		Tags:    []string{"filesystem", "write", "code"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "content", Type: ArgString, Required: true, Description: "Full file contents."},
			{Name: "create_dirs", Type: ArgBoolean, Default: true, Description: "Create parent directories if missing."},
			{Name: "overwrite", Type: ArgBoolean, Default: true, Description: "Allow overwriting existing files."},
		},
		Returns:  "{path, bytes} on success.",
		CostHint: "io-bound",
	}
}

func (t *EditFileTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "edit_file",
		Title:   "Edit file",
		Summary: "Apply an exact string replacement in a text file.",
		Purpose: "Surgical edits. old_string must be unique unless replace_all is true.",
		Risk:    RiskWrite,
		Tags:    []string{"filesystem", "write", "edit", "code"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative path inside the project."},
			{Name: "old_string", Type: ArgString, Required: true, Description: "Exact text to find."},
			{Name: "new_string", Type: ArgString, Required: true, Description: "Replacement text."},
			{Name: "replace_all", Type: ArgBoolean, Default: false, Description: "Replace every occurrence instead of exactly one."},
		},
		Returns:  "{path, replacements}.",
		CostHint: "io-bound",
	}
}

func (t *ListDirTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "list_dir",
		Title:   "List directory",
		Summary: "List files and directories under a path.",
		Purpose: "Discover project layout. Use recursive=true for whole-subtree walks.",
		Risk:    RiskRead,
		Tags:    []string{"filesystem", "read", "list"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Relative directory path (use \".\" for project root)."},
			{Name: "recursive", Type: ArgBoolean, Default: false, Description: "Walk subdirectories."},
			{Name: "max_entries", Type: ArgInteger, Default: 200, Description: "Cap on returned entries (<=500)."},
		},
		Returns:    "{path, entries[], count}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

func (t *GrepCodebaseTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "grep_codebase",
		Title:   "Grep codebase",
		Summary: "Regex search across project files (skips .git, node_modules, vendor, bin, dist).",
		Purpose: "Locate symbols, patterns, or call sites. Always prefer a tight regex over broad queries.",
		Risk:    RiskRead,
		Tags:    []string{"search", "read", "code", "grep"},
		Args: []Arg{
			{Name: "pattern", Type: ArgString, Required: true, Description: "Go regexp (RE2) pattern."},
			{Name: "max_results", Type: ArgInteger, Default: 80, Description: "Cap on matches (<=500)."},
		},
		Returns:    "{pattern, matches[] (file:line:text), count}.",
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

func (t *RunCommandTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "run_command",
		Title:   "Run command",
		Summary: "Execute a whitelisted shell command inside the project sandbox.",
		Purpose: "Run build/test/lint commands. Blocked commands, timeouts, and output caps are enforced by config.",
		Risk:    RiskExecute,
		Tags:    []string{"shell", "execute", "build", "test"},
		Args: []Arg{
			{Name: "command", Type: ArgString, Required: true, Description: "Command string (argv[0] must be allowed by the sandbox)."},
			{Name: "timeout_ms", Type: ArgInteger, Description: "Optional per-call timeout override in ms (<=120000)."},
		},
		Returns:  "stdout/stderr combined Output plus {exit_code, duration_ms}.",
		CostHint: "network",
	}
}
