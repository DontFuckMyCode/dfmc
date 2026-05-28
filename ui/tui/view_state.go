package tui

// view_state.go — side-tab view state. Each top-level F-tab beyond
// chat (Files, Patch, Tools, Workflow) and the floating tasks overlay
// owns one struct here. Keeps the diagnostic-panel cluster
// (panel_states.go) and the runtime/agent cluster (runtime_state.go)
// free of unrelated bookkeeping.

import (
	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// tasksPanelState — floating tasks overlay on the chat tab.
// Rendered by render_task_tree.go.
type tasksPanelState struct {
	expanded      map[string]bool
	selectedIndex int
	scroll        int
}

// patchViewState — Patch tab state plus the workspace-diff snapshot that
// feeds it. `diff`/`changed` mirror `git diff` output (refreshed by the
// workspace loader); `latestPatch` is the most recent patch the assistant
// emitted; `set`/`files`/`index`/`hunk` are the parsed view we paginate
// through with [/]-keys.
type patchViewState struct {
	diff        string
	changed     []string
	latestPatch string
	set         []patchSection
	files       []string
	index       int
	hunk        int
}

// filesViewState — Files tab state. `entries` is the directory listing,
// `index` the cursor row, `pinned` a sticky selection that survives
// re-loads, and `path/preview/size` the currently shown file.
// `query`/`searchActive` implement the `/` filter mode, mirroring the
// pattern every other diagnostic panel uses: `/` opens a live input
// box, runes mutate the query, Enter commits, Esc stops; `c` clears
// the filter outside search mode.
type filesViewState struct {
	entries      []string
	index        int
	pinned       string
	preview      string
	path         string
	size         int
	query        string
	searchActive bool
}

// toolViewState — Tools tab cursor position, scroll offset for the
// registry list, current output for the selected tool, and the in-place
// editor (editing flag, draft buffer, per-key overrides) used to tweak
// parameters before re-running. `query`/`searchActive` implement the
// `/` filter mode for the tool registry — same shape as Files / Memory
// / CodeMap. `c` clears the filter outside search mode.
type toolViewState struct {
	index        int
	output       string
	editing      bool
	draft        string
	overrides    map[string]string
	query        string
	searchActive bool
}

// workflowPanelState — Drive TODO tree panel state for the Workflow tab.
// Tracks the list of drive runs, which run is selected, scroll position,
// and which TODO nodes are expanded to show their detail.
type workflowPanelState struct {
	runs           []*drive.Run // from drive store List(), refreshed on events
	selectedRunID  string       // empty = show run selector; set = show TODO tree
	scrollY        int          // vertical scroll offset in the TODO tree
	expandedTodo   map[string]bool
	selectedIndex  int    // index in run selector list when no run selected
	selectedTodoID string // ID of the TODO whose detail is shown
	// followLive: when true, the cursor and scroll auto-track the
	// currently-running TODO (or the most recent active run if no TODO
	// is running). Toggled with `space` on the Workflow tab. Off by
	// default so manual navigation isn't fought by auto-jumps.
	followLive bool
	// routingEditor controls the drive.Config.Routing editor overlay.
	showRoutingEditor  bool              // true = overlay open
	routingEditTag     string            // tag being edited (empty = new entry)
	routingEditProfile string            // profile name being edited
	routingEditIndex   int               // which row is selected in the routing list
	routingEditMode    bool              // true = currently editing the profile field
	routingDraft       map[string]string // routing entries in the editor (tag -> profile)
}

// activityPanelState — Activity tab state. `entries` is the timestamped
// firehose fed by every engine event; `follow` pins the view to the tail
// (any manual scroll unpins it).
type activityPanelState struct {
	entries      []activityEntry
	scroll       int
	follow       bool
	mode         activityViewMode
	query        string
	searchActive bool
}

// toolStatusPanelState -- Ctrl+Shift+T Tool Status overlay state.
//
// expanded toggles between the default compact layout (each tool call
// gets a head line + clipped reason/params/result/error first lines)
// and a detail layout that renders every multi-line body in full so
// the user can read the actual error message or full params payload.
// Enter / 'x' flips the mode; scroll grammar stays the same.
type toolStatusPanelState struct {
	scroll   int
	expanded bool
}
