// Package commands provides a shared metadata registry that all three DFMC
// surfaces (CLI, TUI, web) consult so command help text, categories, aliases,
// and surface availability stay in sync without being hand-copied across
// layers. The registry intentionally does NOT own dispatch — each surface
// keeps its own handler wiring — but it owns description of record.
//
// Usage sketch:
//
//	reg := commands.DefaultRegistry()
//	for _, c := range reg.ForSurface(commands.SurfaceCLI) {
//	    fmt.Println(c.Name, "—", c.Summary)
//	}
//
// New commands are added in defaults.go. Register() guards against duplicates
// and alias collisions so a bad addition fails at startup instead of at the
// first user's help request.
package commands

import (
	"fmt"
	"sort"
	"strings"
)

// Category groups commands into help sections. The order matters — help
// output renders categories in the declared order, matching the order a new
// user would naturally discover them (ask before memory before serve).
type Category string

const (
	// CategoryQuery: model-driven answers — ask, chat, review, explain,
	// refactor, test, doc.
	CategoryQuery Category = "query"
	// CategoryAnalyze: local-only code intelligence — analyze, map, scan.
	CategoryAnalyze Category = "analyze"
	// CategoryProject: per-project state — init, context, magicdoc, prompt.
	CategoryProject Category = "project"
	// CategoryMemory: persistence across sessions — memory, conversation.
	CategoryMemory Category = "memory"
	// CategoryTools: executable tool surface — tool, plugin, skill.
	CategoryTools Category = "tools"
	// CategoryConfig: runtime configuration — config, provider, model.
	CategoryConfig Category = "config"
	// CategoryServer: long-running processes — serve, remote, tui.
	CategoryServer Category = "server"
	// CategorySystem: meta-commands — status, doctor, version, help.
	CategorySystem Category = "system"
)

// categoryOrder is the canonical display order used by List/ForSurface when
// grouping by category.
var categoryOrder = []Category{
	CategoryQuery,
	CategoryAnalyze,
	CategoryProject,
	CategoryMemory,
	CategoryTools,
	CategoryConfig,
	CategoryServer,
	CategorySystem,
}

// CategoryLabels maps category keys to human-readable section headings.
var CategoryLabels = map[Category]string{
	CategoryQuery:   "Ask & chat",
	CategoryAnalyze: "Analyze & inspect",
	CategoryProject: "Project state",
	CategoryMemory:  "Memory & conversations",
	CategoryTools:   "Tools & skills",
	CategoryConfig:  "Configuration",
	CategoryServer:  "Servers & clients",
	CategorySystem:  "System & meta",
}

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

// Registry is the mutable container. Use DefaultRegistry() to build one
// pre-populated with the shipped command catalog; use NewRegistry() if you
// want to bootstrap an empty one for tests.
type Registry struct {
	commands map[string]*Command
	aliases  map[string]string
	order    []string
}

// NewRegistry returns an empty Registry. Most callers should prefer
// DefaultRegistry, which bundles the shipped command catalog.
func NewRegistry() *Registry {
	return &Registry{
		commands: map[string]*Command{},
		aliases:  map[string]string{},
		order:    nil,
	}
}

// Register inserts cmd, rejecting duplicates and alias collisions so a typo
// in defaults.go fails at boot rather than the user's first help invocation.
// The command is stored by normalized (lowercase) name; lookup is
// case-insensitive.
func (r *Registry) Register(cmd Command) error {
	name := strings.ToLower(strings.TrimSpace(cmd.Name))
	if name == "" {
		return fmt.Errorf("commands: empty name")
	}
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("commands: duplicate name %q", name)
	}
	if _, exists := r.aliases[name]; exists {
		return fmt.Errorf("commands: name %q collides with existing alias", name)
	}
	cmd.Name = name
	cmd.SurfaceList = strings.Split(cmd.Surfaces.String(), ",")
	// Normalize + verify aliases.
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(cmd.Aliases))
	for _, a := range cmd.Aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || a == name {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		if _, hit := r.commands[a]; hit {
			return fmt.Errorf("commands: alias %q for %q collides with existing command", a, name)
		}
		if prev, hit := r.aliases[a]; hit {
			return fmt.Errorf("commands: alias %q for %q already points to %q", a, name, prev)
		}
		seen[a] = struct{}{}
		clean = append(clean, a)
	}
	cmd.Aliases = clean

	r.commands[name] = &cmd
	for _, a := range clean {
		r.aliases[a] = name
	}
	r.order = append(r.order, name)
	return nil
}

// MustRegister is the panic-on-error variant — useful in package init or
// tests where a registration failure is a programmer bug.
func (r *Registry) MustRegister(cmd Command) {
	if err := r.Register(cmd); err != nil {
		panic(err)
	}
}

// Lookup resolves a name (or alias) to its canonical command record. The
// boolean is false when no match is found so callers can emit a "did you
// mean?" helper without a nil check.
func (r *Registry) Lookup(name string) (*Command, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil, false
	}
	if cmd, ok := r.commands[name]; ok {
		return cmd, true
	}
	if canon, ok := r.aliases[name]; ok {
		if cmd, ok := r.commands[canon]; ok {
			return cmd, true
		}
	}
	return nil, false
}

// All returns every registered command in the order they were registered.
// The slice is safe to iterate but callers must not mutate the elements.
func (r *Registry) All() []*Command {
	out := make([]*Command, 0, len(r.order))
	for _, n := range r.order {
		if cmd, ok := r.commands[n]; ok {
			out = append(out, cmd)
		}
	}
	return out
}

// ForSurface returns only commands exposed by the given surface. The result
// is grouped by Category in the canonical order, and alphabetized within
// each category so help output is stable across invocations.
func (r *Registry) ForSurface(s Surface) []*Command {
	byCategory := map[Category][]*Command{}
	for _, cmd := range r.All() {
		if !cmd.Surfaces.Has(s) {
			continue
		}
		byCategory[cmd.Category] = append(byCategory[cmd.Category], cmd)
	}
	out := make([]*Command, 0, len(r.order))
	for _, cat := range categoryOrder {
		group := byCategory[cat]
		sort.SliceStable(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		out = append(out, group...)
	}
	return out
}

// ListByCategory returns commands grouped by category — the shape that help
// renderers want directly. Categories with no commands for the given surface
// are omitted.
func (r *Registry) ListByCategory(s Surface) []CategoryGroup {
	byCategory := map[Category][]*Command{}
	for _, cmd := range r.ForSurface(s) {
		byCategory[cmd.Category] = append(byCategory[cmd.Category], cmd)
	}
	out := make([]CategoryGroup, 0, len(categoryOrder))
	for _, cat := range categoryOrder {
		group := byCategory[cat]
		if len(group) == 0 {
			continue
		}
		out = append(out, CategoryGroup{
			Category: cat,
			Label:    CategoryLabels[cat],
			Commands: group,
		})
	}
	return out
}

// CategoryGroup is the shape ListByCategory returns. Help renderers iterate
// this directly.
type CategoryGroup struct {
	Category Category   `json:"category"`
	Label    string     `json:"label"`
	Commands []*Command `json:"commands"`
}
