package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultRegistry_BootsWithoutPanic(t *testing.T) {
	r := DefaultRegistry()
	if len(r.All()) == 0 {
		t.Fatalf("default registry must expose commands")
	}
}

// TestDefaultRegistry_NoDupes pins that the shipped catalog contains no
// duplicate names or alias collisions. Runtime now log-and-skips on conflicts
// (so dfmc doctor keeps working even if the catalog is broken), so this CI
// gate is what catches a programmer bug introducing a dupe.
func TestDefaultRegistry_NoDupes(t *testing.T) {
	r := NewRegistry()
	for _, cmd := range defaultCommands() {
		if err := r.Register(cmd); err != nil {
			t.Fatalf("default catalog has a registration error on %q: %v", cmd.Name, err)
		}
	}
	if len(r.All()) != len(defaultCommands()) {
		t.Fatalf("registered count (%d) != catalog count (%d) — a dupe was silently dropped", len(r.All()), len(defaultCommands()))
	}
}

func TestRegister_RejectsDuplicateName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Command{Name: "ask", Summary: "first", Surfaces: SurfaceCLI}); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	err := r.Register(Command{Name: "ASK", Summary: "duplicate", Surfaces: SurfaceCLI})
	if err == nil {
		t.Fatalf("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegister_RejectsAliasCollisions(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Command{Name: "conversation", Aliases: []string{"conv"}, Summary: "first", Surfaces: SurfaceCLI, Category: CategoryMemory})
	// Alias "conv" collides with existing alias — Register should error.
	if err := r.Register(Command{Name: "chat", Aliases: []string{"conv"}, Summary: "second", Surfaces: SurfaceCLI, Category: CategoryQuery}); err == nil {
		t.Fatalf("expected alias collision error")
	}
	// Alias collides with an existing canonical name.
	if err := r.Register(Command{Name: "history", Aliases: []string{"conversation"}, Summary: "third", Surfaces: SurfaceCLI, Category: CategoryMemory}); err == nil {
		t.Fatalf("alias that shadows a canonical name must error")
	}
	// Canonical name collides with an existing alias.
	if err := r.Register(Command{Name: "conv", Summary: "fourth", Surfaces: SurfaceCLI, Category: CategoryMemory}); err == nil {
		t.Fatalf("canonical name colliding with alias must error")
	}
}

func TestRegister_RejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Command{Name: "   ", Summary: "blank", Surfaces: SurfaceCLI}); err == nil {
		t.Fatalf("empty name must error")
	}
}

func TestLookup_CaseInsensitiveAndAliasAware(t *testing.T) {
	r := DefaultRegistry()
	cmd, ok := r.Lookup("CONV")
	if !ok {
		t.Fatalf("alias lookup failed")
	}
	if cmd.Name != "conversation" {
		t.Fatalf("alias should resolve to canonical name, got %q", cmd.Name)
	}
	if _, ok := r.Lookup("  ASK  "); !ok {
		t.Fatalf("lookup should trim+lowercase input")
	}
	if _, ok := r.Lookup("definitely-not-a-command"); ok {
		t.Fatalf("unknown command should miss")
	}
}

func TestForSurface_FiltersAndGroups(t *testing.T) {
	r := DefaultRegistry()
	cli := r.ForSurface(SurfaceCLI)
	tui := r.ForSurface(SurfaceTUI)
	web := r.ForSurface(SurfaceWeb)
	if len(cli) == 0 || len(tui) == 0 || len(web) == 0 {
		t.Fatalf("every surface should expose at least one command; cli=%d tui=%d web=%d", len(cli), len(tui), len(web))
	}
	for _, cmd := range cli {
		if !cmd.Surfaces.Has(SurfaceCLI) {
			t.Fatalf("CLI filter leaked non-CLI command: %q surfaces=%s", cmd.Name, cmd.Surfaces)
		}
	}
	// The `serve` command is CLI-only — it must NOT appear in the TUI list.
	for _, cmd := range tui {
		if cmd.Name == "serve" {
			t.Fatalf("serve should not be exposed on TUI")
		}
	}
}

func TestListByCategory_OmitsEmptyGroups(t *testing.T) {
	r := NewRegistry()
	if _, err := r.MustRegister(Command{Name: "ask", Summary: "x", Surfaces: SurfaceCLI, Category: CategoryQuery}); err != nil {
		t.Fatalf("mustregister: %v", err)
	}
	groups := r.ListByCategory(SurfaceCLI)
	if len(groups) != 1 {
		t.Fatalf("expected 1 category group, got %d", len(groups))
	}
	if groups[0].Category != CategoryQuery {
		t.Fatalf("wrong category surfaced: %+v", groups[0])
	}
	if groups[0].Label == "" {
		t.Fatalf("label must be populated from CategoryLabels")
	}
}

func TestSurface_String(t *testing.T) {
	if got := (SurfaceCLI | SurfaceWeb).String(); got != "cli,web" {
		t.Fatalf("expected cli,web got %q", got)
	}
	if got := SurfaceAll.String(); got != "cli,tui,web" {
		t.Fatalf("expected cli,tui,web got %q", got)
	}
	if got := Surface(0).String(); got != "none" {
		t.Fatalf("zero surface should render as 'none', got %q", got)
	}
}

func TestRenderHelp_IncludesAllVisibleCategories(t *testing.T) {
	r := DefaultRegistry()
	out := r.RenderHelp(SurfaceCLI, "DFMC — test")
	if !strings.Contains(out, "DFMC — test") {
		t.Fatalf("header missing from help output: %q", out)
	}
	for _, cat := range []Category{CategoryQuery, CategoryAnalyze, CategorySystem} {
		if !strings.Contains(out, CategoryLabels[cat]) {
			t.Fatalf("help output missing category label %q", CategoryLabels[cat])
		}
	}
	if !strings.Contains(out, "ask") {
		t.Fatalf("ask command should appear in CLI help")
	}
	// Surface-only commands must not leak across surfaces.
	tuiHelp := r.RenderHelp(SurfaceTUI, "")
	if strings.Contains(tuiHelp, "serve") {
		t.Fatalf("TUI help must not mention CLI-only `serve`")
	}
}

func TestRenderCommandHelp_Details(t *testing.T) {
	r := DefaultRegistry()
	out := r.RenderCommandHelp("conv") // alias -> conversation
	if !strings.Contains(out, "conversation —") {
		t.Fatalf("help must start with canonical name, got %q", out)
	}
	if !strings.Contains(out, "Subcommands:") {
		t.Fatalf("conversation help must list subcommands, got %q", out)
	}
	if !strings.Contains(out, "Aliases: conv") {
		t.Fatalf("help must list aliases, got %q", out)
	}
	if !strings.Contains(out, "Available on:") {
		t.Fatalf("help must list surfaces, got %q", out)
	}
	if r.RenderCommandHelp("no-such-thing") != "" {
		t.Fatalf("unknown command should return empty string")
	}
}

func TestNames_Sorted(t *testing.T) {
	names := DefaultRegistry().Names(SurfaceCLI)
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Fatalf("Names() output not sorted: %v", names)
		}
	}
}

func TestCommand_SerializesToJSON(t *testing.T) {
	// The web endpoint will JSON-encode the whole registry. Ensure the shape
	// is stable and includes surfaces as a human-friendly list.
	r := DefaultRegistry()
	ask, _ := r.Lookup("ask")
	raw, err := json.Marshal(ask)
	if err != nil {
		t.Fatalf("marshal ask: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"surfaces":["cli"`) {
		t.Fatalf("surfaces array missing from JSON: %s", s)
	}
	if !strings.Contains(s, `"category":"query"`) {
		t.Fatalf("category missing from JSON: %s", s)
	}
}
