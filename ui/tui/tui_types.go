package tui

// tui_types.go — typed roles, severity tags, parameter coercion, and
// the small struct-only data types referenced across the bubbletea
// package. Pulled out of tui.go so the lifecycle file stays focused on
// Model/NewModel/View. Companion siblings:
//
//   - tui.go      Options + pendingQueueCap + Model + NewModel +
//                 ensureDiagnostics + Init + projectRoot + View +
//                 mouse/scroll constants
//   - tui_run.go  Run + runProgramSafely + runWithPanicGuard
//
// chatRole / coachSeverity are typed strings on purpose: pre-fix the
// dispatchers compared raw strings ("warn" vs "warning") and silently
// took the no-op branch on typos. Typing them makes every call site
// reference a constant the compiler validates.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// chatLineRole canonicalises the strings that go into chatLine.Role.
// The field stays a plain string for backwards compatibility with
// ~100 existing call sites and tests, but new code should reference
// these constants so typos like "asistant" surface at compile time
// (or via grep) instead of silently mis-routing a render branch.
//
// Mirrors pkg/types.MessageRole values exactly. "coach" is TUI-only
// (a system-style hint addressed to the user, separate from the
// LLM's "system" role). Typed (chatRole, not plain string) so the
// compiler catches misspellings at every call site that branches on
// role — the M1 review flagged this as a footgun because the comments
// were the only "type safety" before.
type chatRole string

const (
	chatRoleUser      chatRole = "user"
	chatRoleAssistant chatRole = "assistant"
	chatRoleSystem    chatRole = "system"
	chatRoleTool      chatRole = "tool"
	chatRoleCoach     chatRole = "coach"
)

// Eq compares two roles case-insensitively. Used everywhere the renderer
// branches on role since the wire format (LLM responses, JSONL replays)
// can deliver "Assistant", "ASSISTANT", etc. Replaces the dozen
// strings.EqualFold(item.Role, "literal") sites.
func (c chatRole) Eq(other chatRole) bool {
	return strings.EqualFold(string(c), string(other))
}

// coachSeverity tags a coach note's tone — drives the leading marker the
// renderer puts on the transcript line. Pre-fix the parameter was a bare
// `string` and the dispatcher did `strings.ToLower(...) == "warn"` — a
// caller typo ("warning" instead of "warn") silently fell through to the
// no-marker path. Typing it makes every call site name a constant the
// compiler validates.
type coachSeverity string

const (
	coachSeverityInfo      coachSeverity = "info"
	coachSeverityWarn      coachSeverity = "warn"
	coachSeverityCelebrate coachSeverity = "celebrate"
)

// coachSeverityFromWire normalises a severity string arriving over an
// engine event payload (where it's still typeless). Unknown values
// degrade to coachSeverityInfo rather than erroring — the wire is
// untrusted input and a future engine adding "fyi" shouldn't crash old
// TUIs.
func coachSeverityFromWire(s string) coachSeverity {
	switch coachSeverity(strings.ToLower(strings.TrimSpace(s))) {
	case coachSeverityWarn:
		return coachSeverityWarn
	case coachSeverityCelebrate:
		return coachSeverityCelebrate
	default:
		return coachSeverityInfo
	}
}

// paramStr extracts a tool-param value as a trimmed string, handling the
// JSON-decoded type fan-out (string / int / int64 / float64 / bool) that
// would otherwise force every caller into `fmt.Sprint(params[k])` plus a
// `strings.EqualFold(s, "<nil>")` workaround for the way fmt prints nil
// interfaces. Pre-fix that pattern was duplicated 16× across tui.go,
// command_picker.go, slash_picker.go and missed typed-nil edge cases —
// the H1 review item. Returns "" for missing keys, nil values, or
// whitespace-only strings.
func paramStr(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case int:
		return strconv.Itoa(t)
	case int32:
		return strconv.Itoa(int(t))
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

type chatLine struct {
	Role          chatRole
	Content       string
	Preview       string
	PatchFiles    []string
	PatchHunks    int
	IsLatestPatch bool
	ToolNames     []string
	ToolCalls     int
	ToolFailures  int
	ToolChips     []toolChip
	EventLines    []chatEventLine
	Timestamp     time.Time
	TokenCount    int
	DurationMs    int
	// Provider/Model are captured at the moment the line starts streaming
	// so the header badge stays correct even after the user switches
	// providers mid-session. Empty for non-assistant rows.
	Provider string
	Model    string
	// Cancelled marks an assistant line whose stream was aborted by the
	// user (Esc). The header renders a ⊘ marker so scrollback makes clear
	// the turn is partial, not silently truncated.
	Cancelled bool
}

type patchSection struct {
	Path      string
	Content   string
	HunkCount int
	Hunks     []patchHunk
}

type patchHunk struct {
	Header  string
	Content string
}

type commandPickerItem struct {
	Value       string
	Description string
	Meta        string
}

type chatSuggestionState struct {
	slashMenuActive     bool
	slashCommands       []slashCommandItem
	slashArgSuggestions []string
	// mentionActive is true when the trailing token begins with `@`, even
	// if no files match yet. The render path keys off this so the picker
	// always shows feedback (loading, empty-state, match list) instead of
	// going silent and leaving the user unsure whether @ is wired up.
	mentionActive      bool
	mentionQuery       string
	mentionRange       string
	mentionSuggestions []mentionRow
	quickActions       []quickActionSuggestion
}

type quickActionSuggestion struct {
	Tool          string
	Params        map[string]any
	Reason        string
	PreparedInput string
}

type viewCacheState struct {
	width     int
	height    int
	activeTab int
	value     string
}
