package tui

// slash_catalog.go — the static data half of the slash-command picker.
//
// slashCommandCatalog builds the full list of slash-command entries the
// picker offers: TUI-only extras (/clear, /compact, /doctor, …), then
// every SurfaceTUI entry from the shared internal/commands registry.
// slashTemplateOverrides supplies per-command template strings with
// context-filled placeholders (current file, provider, model). Both
// live here — separate from slash_picker.go — so the "what commands
// exist" table isn't tangled with the "what to suggest next" autocomplete
// logic. Adding a new slash command usually means touching just this file.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/commands"
)

func (m Model) slashCommandCatalog() []slashCommandItem {
	reg := commands.DefaultRegistry()
	overrides := m.slashTemplateOverrides()
	seen := map[string]struct{}{}
	out := make([]slashCommandItem, 0, 40)

	add := func(name, template, desc string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		if template == "" {
			template = "/" + key
		}
		seen[key] = struct{}{}
		out = append(out, slashCommandItem{Command: key, Template: template, Description: desc})
	}

	// TUI-only slash shortcuts come first so that when a prefix matches both a
	// TUI extra and a registry command (e.g. `/prov` → `/providers` vs.
	// `/provider`), the TUI-friendly plural form wins — that matches the
	// established pre-registry behavior users built muscle memory around.
	coachLabel := "mute"
	if m.ui.coachMuted {
		coachLabel = "unmute"
	}
	hintsLabel := "show"
	if m.ui.hintsVerbose {
		hintsLabel = "hide"
	}
	extras := []slashCommandItem{
		{Command: "reload", Template: "/reload", Description: "reload config + env"},
		{Command: "clear", Template: "/clear", Description: "clear transcript (memory untouched)"},
		{Command: "compact", Template: "/compact", Description: "collapse older transcript into a summary (keeps last 6; /compact N for custom)"},
		{Command: "approve", Template: "/approve", Description: "show the tool-approval gate state (which tools prompt agent calls)"},
		{Command: "hooks", Template: "/hooks", Description: "list lifecycle hooks registered per event (pre_tool, post_tool, user_prompt_submit, …)"},
		{Command: "doctor", Template: "/doctor", Description: "in-chat health snapshot (provider, ast, tools, gate, hooks, denials)"},
		{Command: "setup", Template: "/setup", Description: "provider config layering snapshot — resolved primary, save target, conflicts, next action"},
		{Command: "setup clean", Template: "/setup clean", Description: "strip the providers block from project config so user-home preferences win on next load"},
		{Command: "stats", Template: "/stats", Description: "session metrics: tool rounds, rtk savings, agent progress, context fill"},
		{Command: "workflow", Template: "/workflow", Description: "show todos, active subagents, drive progress, and the latest plan"},
		{Command: "todos", Template: "/todos", Description: "print the shared todo list the agent is currently tracking"},
		{Command: "todos clear", Template: "/todos clear", Description: "wipe the shared todo list (the agent will start a new plan on the next turn)"},
		{Command: "tasks", Template: "/tasks", Description: "open the task store panel (j/k, enter, esc to close)"},
		{Command: "tasks clear", Template: "/tasks clear", Description: "wipe non-drive tasks from the store · drive-owned tasks kept"},
		{Command: "subagents", Template: "/subagents", Description: "show current subagent fan-out and recent delegation activity"},
		{Command: "cancel", Template: "/cancel", Description: "cancel the active agent turn (slash equivalent of Ctrl+C) · /drive stop for drive runs"},
		{Command: "shortcuts", Template: "/shortcuts", Description: "open the Shortcuts cheat sheet (alt+h) · /keys is an alias"},
		{Command: "queue", Template: "/queue", Description: "inspect or clear queued follow-up prompts"},
		{Command: "export", Template: "/export", Description: "save the current transcript to .dfmc/exports/*.md (or /export path.md)"},
		{Command: "pin", Template: "/pin ", Description: "pin an assistant turn as a transcript anchor — chip flips to ★"},
		{Command: "unpin", Template: "/unpin ", Description: "remove the pin from an assistant turn"},
		{Command: "fork", Template: "/fork ", Description: "branch the conversation from an assistant turn (defaults the branch name to fork-from-<n>-<stamp>)"},
		{Command: "save", Template: "/save ", Description: "/save <n> exports a single assistant turn → .dfmc/exports/turn-<n>-*.md (no arg = full transcript via /export)"},
		{Command: "quit", Template: "/quit", Description: "exit DFMC"},
		{Command: "providers", Template: "/providers", Description: "list configured providers"},
		{Command: "models", Template: "/models", Description: "show configured model"},
		{Command: "key", Template: "/key", Description: "show API key status (provider · source · masked)"},
		{Command: "key set", Template: "/key set anthropic ", Description: "save an API key to ~/.dfmc/config.yaml (no .env required)"},
		{Command: "key clear", Template: "/key clear anthropic", Description: "remove an API key from ~/.dfmc/config.yaml"},
		{Command: "key migrate", Template: "/key migrate", Description: "copy keys from project .env into ~/.dfmc/config.yaml"},
		{Command: "tools", Template: "/tools", Description: "list tools and open panel"},
		{Command: "log", Template: "/log", Description: "show recent provider calls (model · in/out tokens · preview)"},
		{Command: "ls", Template: "/ls .", Description: "list project files"},
		{Command: "read", Template: "/read " + blankFallback(m.toolTargetFile(), "path/to/file.go"), Description: "read file lines"},
		{Command: "grep", Template: "/grep TODO", Description: "search codebase (regex)"},
		{Command: "run", Template: "/run go test ./...", Description: "run a guarded command"},
		{Command: "diff", Template: "/diff", Description: "show worktree diff"},
		{Command: "file", Template: "/file", Description: "open the file picker (alias for @, avoids AltGr-@ conflicts)"},
		{Command: "coach", Template: "/coach", Description: coachLabel + " the background coach notes"},
		{Command: "hints", Template: "/hints", Description: hintsLabel + " between-round trajectory hints"},
		{Command: "btw", Template: "/btw ", Description: "inject a note at the next tool-loop step"},
		// Analyze family: these have TUI handlers (case "map", "scan") but
		// live at SurfaceCLI|SurfaceWeb in the shared registry, so they
		// never reach the palette through ForSurface. Surface them here so
		// the picker lists every verb the dispatcher actually runs.
		{Command: "map", Template: "/map", Description: "render the codemap (symbols, deps, cycles)"},
		{Command: "scan", Template: "/scan", Description: "scan for security + correctness smells"},
		// Template family: /refactor, /test, /doc dispatch through the same
		// runTemplateSlash handler as /review and /explain (both of which
		// come from the SurfaceAll registry entries). Pin them here so the
		// full family shows up together.
		{Command: "refactor", Template: "/refactor " + blankFallback(m.toolTargetFile(), "path/to/file.go"), Description: "propose a scoped, reversible refactor"},
		{Command: "test", Template: "/test " + blankFallback(m.toolTargetFile(), "path/to/file.go"), Description: "draft tests for a target"},
		{Command: "doc", Template: "/doc " + blankFallback(m.toolTargetFile(), "path/to/file.go"), Description: "draft or update documentation"},
		{Command: "continue", Template: "/continue", Description: "resume a parked agent loop (only works when loop is parked at a step cap or /continue)"},
		{Command: "split", Template: "/split TASK", Description: "Decompose a broad task into subtasks"},
		{Command: "context messages", Template: "/context messages", Description: "show every message the LLM is paying to carry · ID · role · ~tokens · tools · preview"},
		{Command: "context drop", Template: "/context drop ", Description: "manually remove messages from the active branch by ID (override the model's [cleanup:])"},
		{Command: "context gc", Template: "/context gc", Description: "preview engine-side garbage: failed-retry + dominated-read turns the GC would prune"},
		{Command: "context gc run", Template: "/context gc run", Description: "force the GC pass now (auto-runs every turn; this is the manual trigger)"},
		// Drive — autonomous plan/execute loop. Surfaces the full sub-command
		// family (start / stop / list / active / resume) so users discover
		// every verb without leaving the palette.
		{Command: "drive", Template: "/drive ", Description: "start an autonomous plan/execute run (DAG of TODOs; survives chat turns)"},
		{Command: "drive active", Template: "/drive active", Description: "show the currently running drive run (one-line status)"},
		{Command: "drive list", Template: "/drive list", Description: "list recent drive runs (id · state · todos · stamp)"},
		{Command: "drive stop", Template: "/drive stop", Description: "stop the active drive run (resumable; use /drive resume to continue)"},
		{Command: "drive resume", Template: "/drive resume ", Description: "resume a stopped drive run by id (prefix match accepted)"},
		// Plan-mode toggle — investigate-only vs. mutation-allowed. Header
		// renders a PLAN badge so the mode is impossible to forget.
		{Command: "plan", Template: "/plan", Description: "enter plan mode — read-only investigation, no file writes (header shows PLAN)"},
		{Command: "code", Template: "/code", Description: "exit plan mode — prompts can mutate files again"},
		// Conversation amend — pull last user turn back into the composer
		// (/edit) or regenerate the last assistant reply in place (/retry).
		{Command: "retry", Template: "/retry", Description: "regenerate the most recent assistant reply (resends the last user turn)"},
		{Command: "edit", Template: "/edit", Description: "pull the last user message into the composer to amend and resend"},
		// Intent layer — verbose toggle + last-decision inspector.
		{Command: "intent", Template: "/intent", Description: "toggle intent-rewrite verbosity in the transcript (off by default)"},
		{Command: "intent show", Template: "/intent show", Description: "print the most recent intent decision (raw, decision, confidence, reason)"},
		// Utility toggles — useful enough to discover via palette even
		// though they're typically bound to a key chord too.
		{Command: "mouse", Template: "/mouse", Description: "toggle mouse capture (off lets the terminal handle drag-select natively)"},
		{Command: "select", Template: "/select", Description: "selection mode — chat-only width, mouse off, terminal drag-select focused"},
		{Command: "copy", Template: "/copy", Description: "copy the most recent assistant reply to the system clipboard"},
		{Command: "version", Template: "/version", Description: "print the DFMC build version (in-chat)"},
	}
	for _, x := range extras {
		add(x.Command, x.Template, x.Description)
	}

	for _, cmd := range reg.ForSurface(commands.SurfaceTUI) {
		template := overrides[cmd.Name]
		add(cmd.Name, template, cmd.Summary)
		for _, sub := range cmd.Subcommands {
			key := cmd.Name + " " + sub.Name
			add(key, "/"+key, sub.Summary)
		}
	}
	return out
}

func (m Model) slashTemplateOverrides() map[string]string {
	return map[string]string{
		"tool":         "/tool read_file path=" + blankFallback(m.toolTargetFile(), "README.md"),
		"provider":     "/provider " + blankFallback(m.currentProvider(), "openai"),
		"model":        "/model " + blankFallback(m.currentModel(), "model-name"),
		"review":       "/review " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"explain":      "/explain " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"refactor":     "/refactor " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"test":         "/test " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"doc":          "/doc " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"ask":          "/ask your question...",
		"conversation": "/conversation list",
		"memory":       "/memory list",
		"magicdoc":     "/magicdoc update",
		"context":      "/context",
	}
}
