package commands

// defaults.go — the shipped command catalog. Every DFMC verb is described
// exactly once here and then consulted by the CLI help renderer, the TUI
// `/help` output, and the web `/api/v1/commands` discovery endpoint.
//
// Keep this file descriptive, not prescriptive: the registry is a manifest
// of what exists, not a dispatch table. Adding a new verb is two steps —
// (1) add its handler in the relevant UI, (2) add a Command entry here —
// and the three help surfaces absorb the new entry automatically.
//
// Conventions:
//   * Summary is one sentence, <=72 chars, starts with a verb.
//   * Description is multi-line prose; omit for trivially obvious commands.
//   * Usage lists the argument shape without re-stating the name.
//   * Aliases never include the canonical name; Register() rejects dupes.

// DefaultRegistry returns a Registry pre-populated with the complete shipped
// command catalog. Panics on internal registration errors (those are
// programmer bugs in this file, not a user-visible condition).
func DefaultRegistry() *Registry {
	r := NewRegistry()
	for _, cmd := range defaultCommands() {
		r.MustRegister(cmd)
	}
	return r
}

func defaultCommands() []Command {
	return []Command{
		// ---------------- Query ----------------
		{
			Name:     "ask",
			Summary:  "Ask the model a single question; prints the answer.",
			Category: CategoryQuery,
			Surfaces: SurfaceAll,
			Usage:    "ask QUESTION [--provider NAME] [--model ID]",
			Examples: []string{
				"dfmc ask \"what does server.go handle?\"",
				"dfmc ask \"review [[file:internal/foo.go]]\"",
			},
		},
		{
			Name:     "chat",
			Summary:  "Enter an interactive chat session scoped to the project.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "chat [--provider NAME]",
		},
		{
			Name:     "review",
			Summary:  "Review changed code for correctness, risk, and tests.",
			Category: CategoryQuery,
			Surfaces: SurfaceAll,
			Usage:    "review [PATH...] [--diff]",
		},
		{
			Name:     "explain",
			Summary:  "Explain what a file, function, or region does.",
			Category: CategoryQuery,
			Surfaces: SurfaceAll,
			Usage:    "explain PATH[:LINE] [--depth quick|deep]",
		},
		{
			Name:     "refactor",
			Summary:  "Propose a refactor plan with scoped, reversible edits.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "refactor PATH [--goal TEXT]",
		},
		{
			Name:     "debug",
			Summary:  "Reproduce, bisect, and fix a bug with a regression test.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "debug DESCRIPTION_OR_PATH",
		},
		{
			Name:     "test",
			Summary:  "Draft tests for the target file or recent changes.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "test PATH [--framework NAME]",
		},
		{
			Name:     "doc",
			Summary:  "Draft or update documentation for a target.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "doc PATH [--style prose|reference]",
		},
		{
			Name:     "generate",
			Summary:  "Generate new code that obeys project conventions with tests.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "generate DESCRIPTION_OR_PATH",
		},
		{
			Name:     "onboard",
			Summary:  "Codebase walkthrough: hot paths, surprises, where to start.",
			Category: CategoryQuery,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "onboard [PATH]",
		},

		// ---------------- Analyze ----------------
		{
			Name:     "analyze",
			Summary:  "Run static analysis: AST, codemap, hotspots, heuristics.",
			Category: CategoryAnalyze,
			Surfaces: SurfaceAll,
			Usage:    "analyze [--json] [--depth quick|deep]",
		},
		{
			Name:     "map",
			Summary:  "Render the codemap (symbols, deps, cycles) as DOT/SVG/JSON.",
			Category: CategoryAnalyze,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "map [--format dot|svg|json] [--out FILE]",
		},
		{
			Name:     "scan",
			Summary:  "Scan for security smells and dangerous patterns.",
			Category: CategoryAnalyze,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "scan [--severity low|medium|high]",
		},
		{
			Name:     "audit",
			Summary:  "Security audit: triaged findings with file:line and fix direction.",
			Category: CategoryAnalyze,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "audit [PATH...]",
		},

		// ---------------- Project ----------------
		{
			Name:     "init",
			Summary:  "Initialize .dfmc/ project state (knowledge, conventions).",
			Category: CategoryProject,
			Surfaces: SurfaceCLI,
			Usage:    "init [--force]",
		},
		{
			Name:     "context",
			Summary:  "Inspect, tune, and curate the context budget.",
			Category: CategoryProject,
			Surfaces: SurfaceAll,
			Usage:    "context SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "show", Summary: "Show the current context selection."},
				{Name: "budget", Summary: "Print the active token budget breakdown."},
				{Name: "recommend", Summary: "Suggest context adjustments for a query."},
				{Name: "brief", Summary: "Dump the MAGIC_DOC-style project brief."},
				{Name: "add", Summary: "Force-include a path in the context pool."},
				{Name: "rm", Summary: "Exclude a path from the context pool."},
			},
		},
		{
			Name:     "magicdoc",
			Summary:  "Manage MAGIC_DOC.md — the low-token project brief.",
			Category: CategoryProject,
			Surfaces: SurfaceAll,
			Aliases:  []string{"magic"},
			Usage:    "magicdoc SUBCOMMAND",
			Subcommands: []Subcommand{
				{Name: "update", Aliases: []string{"sync", "generate"}, Summary: "Regenerate the brief from current project state."},
				{Name: "show", Aliases: []string{"cat"}, Summary: "Print the current brief."},
			},
		},
		{
			Name:     "prompt",
			Summary:  "Inspect and preview the layered prompt library.",
			Category: CategoryProject,
			Surfaces: SurfaceAll,
			Usage:    "prompt SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "list", Summary: "List registered prompt templates."},
				{Name: "show", Summary: "Render a template with sample vars."},
				{Name: "recommend", Summary: "Suggest a prompt surface for a query."},
				{Name: "render", Summary: "Produce the full composed prompt for a query."},
			},
		},

		// ---------------- Memory ----------------
		{
			Name:     "memory",
			Summary:  "Query and edit persistent working/episodic/semantic memory.",
			Category: CategoryMemory,
			Surfaces: SurfaceAll,
			Usage:    "memory SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "list", Summary: "List memory entries."},
				{Name: "search", Summary: "Search memory for a term."},
				{Name: "add", Summary: "Append a new memory entry."},
				{Name: "clear", Summary: "Clear memory (scope-aware)."},
			},
		},
		{
			Name:     "conversation",
			Summary:  "Manage JSONL-persisted conversations with branching.",
			Category: CategoryMemory,
			Surfaces: SurfaceAll,
			Aliases:  []string{"conv"},
			Usage:    "conversation SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "list", Summary: "List saved conversations."},
				{Name: "search", Summary: "Search across conversation history."},
				{Name: "active", Summary: "Show the currently active conversation."},
				{Name: "new", Summary: "Start a new conversation."},
				{Name: "save", Summary: "Persist the active conversation."},
				{Name: "load", Summary: "Reopen a saved conversation by id."},
				{Name: "undo", Summary: "Drop the last assistant message."},
				{Name: "branch", Summary: "Create, switch, list, or compare branches."},
			},
		},

		// ---------------- Tools ----------------
		{
			Name:     "tool",
			Summary:  "Execute a registered tool directly (bypasses the model).",
			Category: CategoryTools,
			Surfaces: SurfaceAll,
			Usage:    "tool NAME key=value ...",
			Examples: []string{"dfmc tool read_file path=README.md"},
		},
		{
			Name:     "skill",
			Summary:  "List or run reusable skill templates (review/explain/...).",
			Category: CategoryTools,
			Surfaces: SurfaceAll,
			Usage:    "skill SUBCOMMAND [args...]",
		},
		{
			Name:     "plugin",
			Summary:  "List or manage plugin bundles; call plugin methods over JSON-RPC.",
			Category: CategoryTools,
			Surfaces: SurfaceCLI | SurfaceWeb,
			Usage:    "plugin SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "list", Summary: "List installed and enabled plugins."},
				{Name: "info", Summary: "Show metadata for one plugin."},
				{Name: "install", Summary: "Install a plugin from a local path or URL."},
				{Name: "remove", Summary: "Remove an installed plugin."},
				{Name: "enable", Summary: "Enable a plugin in config."},
				{Name: "disable", Summary: "Disable a plugin without removing it."},
				{Name: "run", Aliases: []string{"call"}, Summary: "Invoke a method on a plugin over JSON-RPC."},
			},
		},

		// ---------------- Config ----------------
		{
			Name:     "config",
			Summary:  "Inspect and edit configuration (global + project).",
			Category: CategoryConfig,
			Surfaces: SurfaceCLI,
			Usage:    "config SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "show", Summary: "Print the merged effective config."},
				{Name: "edit", Summary: "Open the project config in $EDITOR."},
				{Name: "set", Summary: "Set a single key (dotted path)."},
				{Name: "sync-models", Summary: "Rewrite providers.profiles.* from models.dev."},
			},
		},
		{
			Name:     "provider",
			Summary:  "Switch the active provider/model for this session.",
			Category: CategoryConfig,
			Surfaces: SurfaceTUI,
			Usage:    "/provider NAME [MODEL] [--persist]",
		},
		{
			Name:     "model",
			Summary:  "Switch the active model within the current provider.",
			Category: CategoryConfig,
			Surfaces: SurfaceTUI,
			Usage:    "/model NAME [--persist]",
		},

		// ---------------- Server ----------------
		{
			Name:     "serve",
			Summary:  "Run the embedded HTTP+SSE server (default :7777).",
			Category: CategoryServer,
			Surfaces: SurfaceCLI,
			Usage:    "serve [--addr HOST:PORT]",
		},
		{
			Name:     "remote",
			Summary:  "Client subcommands that talk to a running DFMC server.",
			Category: CategoryServer,
			Surfaces: SurfaceCLI,
			Usage:    "remote SUBCOMMAND [args...]",
			Subcommands: []Subcommand{
				{Name: "start", Summary: "Launch gRPC+WS on 7778/7779."},
				{Name: "ask", Summary: "Ask a running server over the wire."},
				{Name: "status", Summary: "Probe a running server."},
			},
		},
		{
			Name:     "tui",
			Summary:  "Launch the bubbletea terminal workbench.",
			Category: CategoryServer,
			Surfaces: SurfaceCLI,
			Usage:    "tui",
		},
		{
			Name:     "mcp",
			Summary:  "Serve DFMC tools to MCP-compatible hosts over stdio (JSON-RPC 2.0).",
			Category: CategoryServer,
			Surfaces: SurfaceCLI,
			Usage:    "mcp",
		},

		// ---------------- System ----------------
		{
			Name:     "status",
			Summary:  "Show engine state: provider, model, index, memory footprint.",
			Category: CategorySystem,
			Surfaces: SurfaceAll,
		},
		{
			Name:     "doctor",
			Summary:  "Run self-diagnostics (CGO, AST backend, tool surface, store).",
			Category: CategorySystem,
			Surfaces: SurfaceCLI,
		},
		{
			Name:     "hooks",
			Summary:  "List lifecycle hooks registered around tool execution.",
			Category: CategorySystem,
			Surfaces: SurfaceCLI | SurfaceTUI,
			Usage:    "hooks [--json]",
		},
		{
			Name:     "approvals",
			Aliases:  []string{"approve", "permissions"},
			Summary:  "Show the tool approval gate and recent denials.",
			Category: CategorySystem,
			Surfaces: SurfaceCLI | SurfaceTUI,
			Usage:    "approvals [--json]",
		},
		{
			Name:     "version",
			Summary:  "Print the DFMC build version.",
			Category: CategorySystem,
			Surfaces: SurfaceCLI | SurfaceWeb,
		},
		{
			Name:     "update",
			Summary:  "Check for a newer DFMC release on GitHub.",
			Category: CategorySystem,
			Surfaces: SurfaceCLI,
			Usage:    "update [--channel stable|prerelease] [--json]",
		},
		{
			Name:     "help",
			Summary:  "Show help for DFMC or a specific command.",
			Category: CategorySystem,
			Surfaces: SurfaceAll,
			Usage:    "help [COMMAND]",
		},
		{
			Name:     "completion",
			Summary:  "Emit shell completion for bash/zsh/fish/powershell.",
			Category: CategorySystem,
			Surfaces: SurfaceCLI,
			Usage:    "completion SHELL",
		},
		{
			Name:     "man",
			Summary:  "Print the man-page style reference for a command.",
			Category: CategorySystem,
			Surfaces: SurfaceCLI,
			Usage:    "man [COMMAND]",
		},
	}
}
