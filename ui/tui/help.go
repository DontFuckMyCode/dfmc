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

	// Grouped keyboard shortcuts — organized by context, not alphabetically.
	// Separators make sections visually distinct in the terminal.
	sections := []string{

		"",
		"─────────────────────────────────────────",
		"CHAT COMPOSER",
		"─────────────────────────────────────────",
		"  Ctrl+X                 send composer / queue while streaming",
		"  Enter / Ctrl+J         literal newline (Alt+Enter also works)",
		"  Ctrl+W                 kill word before cursor",
		"  Ctrl+K                 kill to end of line",
		"  Ctrl+U                 clear entire input line",
		"  Ctrl+A / Home         cursor to line start",
		"  Ctrl+E / End          cursor to line end / jump to latest transcript",
		"  Ctrl+← / →            word left/right",
		"  ↑ / ↓                 history nav / suggestion nav (no picker)",
		"  PgUp / PgDown         scroll transcript 8 lines",
		"  Shift+PgUp/PgDown     scroll transcript 3 lines",
		"  Shift+↑ / ↓           scroll transcript 3 lines",
		"  Ctrl+H / Backspace    delete char before cursor",
		"  Delete                delete char at cursor",
		"  @ / Ctrl+T            open file mention picker",
		"  /                     open slash command picker",
		"  Tab                    autocomplete (mention/slash arg/quick action)",
		"  Ctrl+P                open slash command menu",
		"  Ctrl+G                jump to Activity tab",
		"  Esc                    dismiss resume prompt (streaming cancel: ctrl+c)",

		"",
		"─────────────────────────────────────────",
		"PANEL TABS  (F-key order = user mental model: F1=first tab)",
		"─────────────────────────────────────────",
		"  F1 Chat",
		"  F1  Chat       F2  Status     F3  Files",
		"  F4  Patch      F5  Workflow  F6  Tools",
		"  F7  Activity   F8  Memory     F9  CodeMap",
		"  F10 Conversations  F11 Prompts  F12 Security",
		"  Ctrl+I   Context panel    Ctrl+O   Providers",
		"  Tab / Shift+Tab   next / prev tab",

		"",
		"─────────────────────────────────────────",
		"STATS PANEL  (alt+key on Chat tab)",
		"─────────────────────────────────────────",
		"  alt+a   overview mode",
		"  alt+s   todos mode",
		"  alt+d   tasks mode",
		"  alt+f   subagents mode",
		"  alt+p   providers mode",
		"  alt+x   toggle selection mode (drag-select transcript)",
		"  ctrl+s  show/hide stats panel",

		"",
		"─────────────────────────────────────────",
		"FILES PANEL",
		"─────────────────────────────────────────",
		"  j / ↓        next file",
		"  k / ↑        previous file",
		"  Enter        load file preview",
		"  r             reload directory",
		"  p             toggle pin",
		"  i             insert [[file:path]] into chat",
		"  e             prepare Explain prompt",
		"  v             prepare Review prompt",

		"",
		"─────────────────────────────────────────",
		"ACTIVITY PANEL",
		"─────────────────────────────────────────",
		"  j / k / ↑ / ↓   scroll",
		"  PgUp / PgDown   page scroll",
		"  g / Home        jump to start",
		"  G / End         jump to latest (follow mode)",
		"  1-6             filter by category",
		"  /               search",
		"  c               clear log",
		"  p               toggle follow (auto-scroll to latest)",

		"",
		"─────────────────────────────────────────",
		"MENTIONS",
		"─────────────────────────────────────────",
		"  @file.go               pick a file",
		"  @file.go:10-50         attach a line range",
		"  @file.go#L10           attach a single line",
		"  Ctrl+T                 open picker without typing @",

		"",
		"─────────────────────────────────────────",
		"KEYBOARD NOTES",
		"─────────────────────────────────────────",
		"  Layouts where '@' lives behind AltGr / Alt+Q (international",
		"  keyboards on MinTTY / Windows Terminal): use Ctrl+T to open",
		"  the file picker; prefer letter shortcuts (j/k/g/G) when Alt",
		"  combinations are unreliable.",
	}

	slashSection := strings.Join([]string{
		"",
		"─────────────────────────────────────────",
		"TUI-only shortcuts:",
		"-----------------------------------------",
		"SLASH COMMANDS",
		"─────────────────────────────────────────",
		"  /reload          Reload engine config",
		"  /clear           Clear transcript (memory untouched)",
		"  /compact [N]     Collapse older transcript into a summary",
		"  /approve         Show tool-approval gate state",
		"  /hooks           List lifecycle hooks",
		"  /doctor          In-chat health snapshot",
		"  /stats           Session metrics (alias /tokens, /cost)",
		"  /workflow        Show todos, subagents, drive progress",
		"  /shortcuts       Open the Shortcuts cheat sheet (alt+h tab)",
		"  /todos [clear]   Print or wipe the shared todo list",
		"  /tasks [list|tree|show|roots|clear]  Task store panel + ops",
		"  /subagents       Show subagent fan-out",
		"  /cancel          Cancel the active agent turn (alias /abort, /stop)",
		"  /providers       Open Providers panel",
		"  /queue [show|clear|drop N]   Inspect or prune queued prompts",
		"  /export [PATH]   Save transcript to markdown",
		"  /quit            Exit DFMC",
		"  /coach           Mute/unmute coach notes",
		"  /hints           Show/hide trajectory hints",
		"  /select          Toggle selection mode",
		"  /tools           Show tool surface / toggle chip strip",
		"  /tool show NAME  Print spec for NAME",
		"  /diff            Show staged patch diff",
		"  /patch           Open patch panel",
		"  /apply [--check] Apply (or dry-run) the staged patch",
		"  /undo            Undo last assistant message",
		"  /ls [PATH] [-r] [--max N]   List files",
		"  /read PATH [START] [END]    Read a file range",
		"  /grep PATTERN     Search the project",
		"  /run COMMAND [ARGS...]      Run a shell command",
		"  /continue        Resume parked agent loop",
		"  /split TASK      Decompose a broad task into subtasks",
		"  /agents [show NAME]  List sub-agent roles + provider profiles",
		"  /btw NOTE        Inject a note at the next agent step",
		"  /analyze [--flag] [path]     Full analysis",
		"  /scan [--flag] [path]        Security-only scan",
		"",
		"  Run /help <command> for details on a specific command.",
	}, "\n")

	return catalog + strings.Join(sections, "\n") + slashSection
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
