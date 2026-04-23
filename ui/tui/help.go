// In-chat help text: the /help catalog, per-command detail pages, and
// the /split plan summary. Extracted from tui.go — pure formatters
// with no Model dependency.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func renderTUIHelp() string {
	reg := commands.DefaultRegistry()
	catalog := reg.RenderHelp(commands.SurfaceTUI, "Slash commands:")
	tail := strings.Join([]string{
		"",
		"",
		"TUI-only shortcuts:",
		"    /reload                      Reload engine configuration",
		"    /clear                       Clear transcript (memory untouched)",
		"    /compact [N]                 Collapse older transcript into a summary (keeps last N; default 6)",
		"    /approve                     Show tool-approval gate state (which tools prompt agent calls)",
		"    /hooks                       List lifecycle hooks registered per event",
		"    /doctor                      In-chat health snapshot (alias /health)",
		"    /stats                       Session metrics (alias /tokens, /cost)",
		"    /workflow                    Show todos, active subagents, drive progress, and the latest plan",
		"    /todos                       Print the shared todo list the agent is tracking",
		"    /subagents                   Show current subagent fan-out and recent delegation activity",
		"    /queue [show|clear|drop N]   Inspect or prune queued follow-up prompts",
		"    /export [PATH]               Save transcript to markdown (default .dfmc/exports/transcript-*.md)",
		"    /quit                        Exit DFMC",
		"    /coach                       Mute or unmute background coach notes",
		"    /hints                       Show or hide between-round trajectory hints",
		"    /select                      Toggle chat-only selection mode (hide stats, disable mouse capture)",
		"    /tools                       Show tool surface",
		"    /tool show NAME              Print the spec for NAME (args, risk, examples)",
		"    /diff                        Show staged patch diff",
		"    /patch                       Open the patch panel",
		"    /apply [--check]             Apply (or dry-run) the staged patch",
		"    /undo                        Undo the last assistant message",
		"    /ls [PATH] [-r] [--max N]    List files",
		"    /read PATH [START] [END]     Read a file range",
		"    /grep PATTERN                Search the project",
		"    /run COMMAND [ARGS...]       Run a shell command",
		"    /continue                    Resume a parked agent loop",
		"    /split TASK                  Decompose a broad task into subtasks",
		"    /btw NOTE                    Queue a note for the next tool-loop step",
		"",
		"Mentions: @file.go picks a file · @file.go:10-50 or @file.go#L10-L50 attaches a range.",
		"Panels: F1 Chat · F2 Providers · F3 Files · F4 Patch · F5 Workflow · F6 Tools · F7 Activity · F8 Memory · F9 CodeMap · F10 Conversations · F11 Prompts · F12 Security · Alt+I Status · Alt+Y Plans · Alt+W Context · Alt+O Providers · Ctrl+P palette",
		"Run /help <command> for details on a specific command.",
	}, "\n")
	return catalog + tail
}

// renderTUICommandHelp prints the detail view for a single registry command,
// or a short error + catalog pointer when unknown.
func renderTUICommandHelp(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "workflow":
		return strings.Join([]string{
			"/workflow",
			"",
			"Shows the current autonomous-workflow snapshot from inside Chat:",
			"  - shared todo list counts and the active step",
			"  - active subagent fan-out",
			"  - drive progress",
			"  - the latest split/autonomy plan summary",
			"",
			"Related:",
			"  /todos      show the detailed checklist",
			"  /subagents  show recent delegation activity",
			"  ctrl+y      jump to Plans",
			"  ctrl+g      jump to Activity",
		}, "\n")
	case "todos", "todo":
		return strings.Join([]string{
			"/todos",
			"",
			"Prints the shared todo_write list the agent is currently using.",
			"Useful when you want to see whether the agent decomposed the task",
			"and which step is currently marked in progress.",
		}, "\n")
	case "subagents", "workers":
		return strings.Join([]string{
			"/subagents",
			"",
			"Shows current subagent fan-out and the most recent subagent events",
			"mirrored into the Activity stream.",
			"",
			"Tip: ctrl+g jumps straight to Activity for the full event firehose.",
		}, "\n")
	case "queue":
		return strings.Join([]string{
			"/queue",
			"",
			"Shows the pending prompt queue used while a turn is still streaming.",
			"Safe local slash commands now run immediately; only real follow-up work should queue here.",
			"",
			"Subcommands:",
			"  /queue           show the current queue",
			"  /queue clear     remove every queued item",
			"  /queue drop N    remove one queued item by its 1-based index",
		}, "\n")
	case "select":
		return strings.Join([]string{
			"/select",
			"",
			"Toggles chat-only selection mode.",
			"When ON, the right stats panel is hidden and Bubble Tea mouse capture is disabled",
			"so terminal drag-to-select focuses on the chat column instead of the whole split layout.",
			"",
			"Shortcuts:",
			"  alt+x      toggle selection mode",
			"  ctrl+s     manually show/hide stats panel",
			"  /mouse     manually toggle mouse capture",
			"",
			"Note: drag-scroll while selecting depends on your terminal emulator.",
		}, "\n")
	}
	reg := commands.DefaultRegistry()
	if detail := reg.RenderCommandHelp(name); detail != "" {
		return detail
	}
	return fmt.Sprintf("Unknown command: %s. Try /help for the catalog.", name)
}

// renderSplitPlan formats a planning.Plan as a chat transcript block. Each
// subtask gets a numbered bullet with its hint tag ("numbered-list",
// "stage", "conjunction") so the user can see *why* the splitter chose to
// break it. When the query doesn't decompose, the block says so — no silent
// no-op that leaves the user wondering if the command ran.
func renderSplitPlan(plan planning.Plan) string {
	if len(plan.Subtasks) <= 1 {
		return "/split — this task looks atomic (the splitter couldn't find parallel units). Ask it more narrowly or dispatch it as-is."
	}
	mode := "sequential"
	if plan.Parallel {
		mode = "parallel"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/split — %d subtasks (%s, confidence %.2f):\n", len(plan.Subtasks), mode, plan.Confidence)
	for i, s := range plan.Subtasks {
		fmt.Fprintf(&b, "  %d. [%s] %s\n", i+1, s.Hint, strings.TrimSpace(s.Title))
		if desc := strings.TrimSpace(s.Description); desc != "" && desc != strings.TrimSpace(s.Title) {
			fmt.Fprintf(&b, "     %s\n", truncateSingleLine(desc, 160))
		}
	}
	if plan.Parallel {
		b.WriteString("\nDispatch each with a focused /ask or /continue — the model can fan them out in parallel.")
	} else {
		b.WriteString("\nRun them one at a time — the splitter detected ordering markers (first/then).")
	}
	return b.String()
}
