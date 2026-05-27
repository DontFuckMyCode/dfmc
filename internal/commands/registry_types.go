package commands

// registry_types.go — pure data carriers used by the commands
// registry: the Surface bitset (which UI layers expose a command)
// and the Subcommand / Command / CategoryGroup record shapes that
// the help renderers consume.
//
// Sibling of registry.go which keeps the Category constants +
// canonical category ordering, CategoryLabels, the Registry struct
// + NewRegistry / Register mutation surface, and the Lookup / All /
// ForSurface / ListByCategory / orderedCommandCategories
// query/iteration surface.

import "strings"

// Surface is a bitset of UI layers that expose a given command. Commands are
// typically available on all three, but a few (e.g. `tui`) only make sense
// on the CLI.
type Surface uint8

const (
	SurfaceCLI Surface = 1 << iota
	SurfaceTUI
	SurfaceWeb
)

// SurfaceAll is shorthand for the most common case: the command shows up on
// every layer.
const SurfaceAll = SurfaceCLI | SurfaceTUI | SurfaceWeb

// Has reports whether s includes the given surface bit.
func (s Surface) Has(other Surface) bool { return s&other != 0 }

// String returns a short comma-separated list of surface names — useful in
// `dfmc help` output and the web `/api/v1/commands` response.
func (s Surface) String() string {
	names := make([]string, 0, 3)
	if s.Has(SurfaceCLI) {
		names = append(names, "cli")
	}
	if s.Has(SurfaceTUI) {
		names = append(names, "tui")
	}
	if s.Has(SurfaceWeb) {
		names = append(names, "web")
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

// Subcommand describes one child verb of a multi-level command (e.g. the
// `list` under `memory`). Subcommands are flat — two levels max — because
// deeper nesting has never paid its cost in this codebase.
type Subcommand struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Summary string   `json:"summary"`
	Usage   string   `json:"usage,omitempty"`
}

// Command is the registry record for one verb. Handler wiring lives elsewhere
// — the registry only knows what exists, what it does in prose, and where.
type Command struct {
	Name        string       `json:"name"`
	Aliases     []string     `json:"aliases,omitempty"`
	Summary     string       `json:"summary"`
	Description string       `json:"description,omitempty"`
	Category    Category     `json:"category"`
	Surfaces    Surface      `json:"-"`
	SurfaceList []string     `json:"surfaces"`
	Subcommands []Subcommand `json:"subcommands,omitempty"`
	Examples    []string     `json:"examples,omitempty"`
	// Usage is a one-line argument signature (e.g. "ask QUESTION [--provider
	// NAME]") shown in help output.
	Usage string `json:"usage,omitempty"`
}

// CategoryGroup is the shape ListByCategory returns. Help renderers iterate
// this directly.
type CategoryGroup struct {
	Category Category   `json:"category"`
	Label    string     `json:"label"`
	Commands []*Command `json:"commands"`
}
