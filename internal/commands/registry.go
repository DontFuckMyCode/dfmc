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
// New commands are added in defaults_catalog.go. Register() guards against duplicates
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

var categoryOrderIndex = func() map[Category]int {
	out := make(map[Category]int, len(categoryOrder))
	for i, cat := range categoryOrder {
		out[cat] = i
	}
	return out
}()

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

// Surface bitset, Subcommand / Command / CategoryGroup record shapes,
// and the RegistrationError typed wrapper live in registry_types.go.

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
// case-insensitive. A zero Surfaces bitset is also rejected: a command not
// exposed on any surface is invisible everywhere yet would otherwise
// register cleanly with SurfaceList=["none"], making the catalog lie about
// what actually ships. Catching this at registration is what stops a
// missing-Surfaces field in defaults.go from silently dropping the command.
func (r *Registry) Register(cmd Command) error {
	name := strings.ToLower(strings.TrimSpace(cmd.Name))
	if name == "" {
		return fmt.Errorf("commands: empty name")
	}
	if cmd.Surfaces == 0 {
		return fmt.Errorf("commands: %q has no Surfaces — set Surfaces: SurfaceAll or a specific bit", name)
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

// MustRegister is the error-wrapping variant of Register: it returns the
// canonical name on success and a *RegistrationError (with the offending
// command name attached) on failure. Despite the "Must" prefix it does NOT
// panic — the name is historical and the (string, error) signature is what
// callers actually rely on. Use it in package init or tests where you want
// the typed wrapper for diagnostics; otherwise plain Register is fine.
func (r *Registry) MustRegister(cmd Command) (string, error) {
	if err := r.Register(cmd); err != nil {
		return "", &RegistrationError{
			CommandName: strings.ToLower(strings.TrimSpace(cmd.Name)),
			Err:         err,
		}
	}
	return strings.ToLower(strings.TrimSpace(cmd.Name)), nil
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
	for _, cat := range orderedCommandCategories(byCategory) {
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
	for _, cat := range orderedCommandCategories(byCategory) {
		group := byCategory[cat]
		if len(group) == 0 {
			continue
		}
		label := CategoryLabels[cat]
		if strings.TrimSpace(label) == "" {
			label = string(cat)
		}
		out = append(out, CategoryGroup{
			Category: cat,
			Label:    label,
			Commands: group,
		})
	}
	return out
}

func orderedCommandCategories(byCategory map[Category][]*Command) []Category {
	out := make([]Category, 0, len(byCategory))
	seen := map[Category]struct{}{}
	for _, cat := range categoryOrder {
		if len(byCategory[cat]) == 0 {
			continue
		}
		out = append(out, cat)
		seen[cat] = struct{}{}
	}

	extras := make([]Category, 0)
	for cat, group := range byCategory {
		if len(group) == 0 {
			continue
		}
		if _, known := categoryOrderIndex[cat]; known {
			continue
		}
		if _, ok := seen[cat]; ok {
			continue
		}
		extras = append(extras, cat)
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i] < extras[j] })
	return append(out, extras...)
}
