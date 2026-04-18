package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// newCoverageModel sets up a Model on a bare engine — enough surface
// for every slash to at least enter its handler, return handled=true,
// and append a transcript message. EventBus is wired so slashes that
// publish events (e.g. /btw, /continue) don't nil-panic.
func newCoverageModel(t *testing.T) Model {
	t.Helper()
	cfg := config.DefaultConfig()
	eng := &engine.Engine{
		Config:      cfg,
		ProjectRoot: t.TempDir(),
		EventBus:    engine.NewEventBus(),
	}
	return NewModel(context.Background(), eng)
}

// slashesThatDispatchEngineWork covers verbs whose handler path kicks
// off a real provider or tool call via tea.Cmd after returning. We can
// still drive them through executeChatCommand (which returns handled=
// true and appends to the transcript before the cmd fires), but the
// cmd itself will nil-panic on our bare engine. The coverage test
// skips them — their happy paths are covered by dedicated unit tests
// (submit_test.go, retry/edit tests, runTemplateSlash tests, etc.).
var slashesThatDispatchEngineWork = map[string]struct{}{
	"ask":      {},
	"review":   {},
	"explain":  {},
	"refactor": {},
	"test":     {},
	"doc":      {},
	"retry":    {},
}

// TestSlashCoverage_AllAdvertisedCommandsHandled is a spec-level test:
// walk every slash the TUI advertises (via the slashCommandCatalog or
// the registry under SurfaceTUI) and make sure typing it returns
// handled=true — i.e. no command silently falls through to the
// "Unknown command" default branch.
//
// A slash may answer with "use the CLI" — that still counts as handled.
// We only flag entries that dump the generic "Unknown chat command"
// fallback.
func TestSlashCoverage_AllAdvertisedCommandsHandled(t *testing.T) {
	m := newCoverageModel(t)

	advertised := collectAdvertisedCommands(m)
	if len(advertised) < 30 {
		t.Fatalf("advertised command catalog looks suspiciously small (%d) — catalog loader broken?", len(advertised))
	}

	unhandled := []string{}
	for _, entry := range advertised {
		if _, skip := slashesThatDispatchEngineWork[entry]; skip {
			continue
		}
		// executeChatCommand takes the raw "/cmd" form.
		input := "/" + entry
		next, _, handled := m.executeChatCommand(input)
		if !handled {
			unhandled = append(unhandled, entry+" (handled=false)")
			continue
		}
		nm, ok := next.(Model)
		if !ok {
			unhandled = append(unhandled, entry+" (model cast failed)")
			continue
		}
		if len(nm.chat.transcript) == 0 {
			// Handler returned without appending anything — that's fine
			// for picker-opening commands (tool/read/grep/run/provider/model)
			// which switch to a command-picker state.
			continue
		}
		last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
		if strings.Contains(last, "Unknown chat command") {
			unhandled = append(unhandled, entry+" → fell into 'Unknown chat command' fallback")
		}
	}

	if len(unhandled) > 0 {
		t.Fatalf("%d advertised slash command(s) do not route to a handler:\n  %s",
			len(unhandled), strings.Join(unhandled, "\n  "))
	}
}

// collectAdvertisedCommands walks the exact same sources the picker
// does — the extras catalog and the registry's TUI-surface commands.
// Subcommands are skipped (tested separately via conversationSlash etc.)
// so we don't try to type "/memory list" as a bare /cmd here.
func collectAdvertisedCommands(m Model) []string {
	seen := map[string]struct{}{}
	reg := commands.DefaultRegistry()

	// Extras list — the hand-curated TUI-only verbs in slashCommandCatalog.
	// We probe via m.slashCommandCatalog() which merges both sources.
	for _, item := range m.slashCommandCatalog() {
		name := strings.TrimSpace(strings.ToLower(item.Command))
		// Skip "foo bar" multi-token entries (subcommand keys) — those
		// are tested by the sub-handler unit tests.
		if name == "" || strings.Contains(name, " ") {
			continue
		}
		seen[name] = struct{}{}
	}

	// Registry commands for the TUI surface — redundant with the catalog
	// above but included for belt-and-braces: if someone adds a new
	// SurfaceAll command in internal/commands/defaults.go without
	// updating the extras list, we still want the coverage check to
	// catch it.
	for _, cmd := range reg.ForSurface(commands.SurfaceTUI) {
		name := strings.TrimSpace(strings.ToLower(cmd.Name))
		if name != "" {
			seen[name] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// TestSlashCoverage_ClIRedirectsMentionCLI — every command redirected to
// CLI (init, serve, mcp, etc.) should tell the user how to run it.
func TestSlashCoverage_CLIRedirectsMentionCLI(t *testing.T) {
	redirects := []string{
		"/init", "/completion", "/man", "/serve", "/remote", "/plugin", "/config",
		"/debug", "/generate", "/onboard", "/audit", "/mcp", "/update", "/tui",
	}
	for _, r := range redirects {
		m := newCoverageModel(t)
		next, _, handled := m.executeChatCommand(r)
		if !handled {
			t.Errorf("%s should be handled (got fallthrough)", r)
			continue
		}
		nm := next.(Model)
		if len(nm.chat.transcript) == 0 {
			t.Errorf("%s should append a system message", r)
			continue
		}
		last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
		if !strings.Contains(strings.ToLower(last), "dfmc ") {
			t.Errorf("%s redirect should mention the `dfmc` CLI; got:\n%s", r, last)
		}
	}
}

func TestSlashPrompt_ListsTemplates(t *testing.T) {
	m := newCoverageModel(t)
	next, _, handled := m.executeChatCommand("/prompt")
	if !handled {
		t.Fatalf("/prompt must be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "Prompt templates") {
		t.Fatalf("/prompt list should show template header, got:\n%s", last)
	}
}

func TestSlashPrompt_ShowUnknownIDExplains(t *testing.T) {
	m := newCoverageModel(t)
	next, _, _ := m.executeChatCommand("/prompt show does_not_exist")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "No prompt template") {
		t.Fatalf("unknown id should get a friendly message, got:\n%s", last)
	}
}

func TestSlashPrompt_UnknownSubcommand(t *testing.T) {
	m := newCoverageModel(t)
	next, _, _ := m.executeChatCommand("/prompt wat")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "unknown subcommand") {
		t.Fatalf("unknown subcommand should tell the user; got:\n%s", last)
	}
}

func TestSlashSkill_ListsBuiltinsAtLeast(t *testing.T) {
	m := newCoverageModel(t)
	next, _, handled := m.executeChatCommand("/skill")
	if !handled {
		t.Fatalf("/skill must be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	for _, want := range []string{"review", "explain", "refactor", "test", "doc"} {
		if !strings.Contains(last, want) {
			t.Fatalf("/skill list should include builtin %q, got:\n%s", want, last)
		}
	}
}

func TestSlashSkill_ShowBuiltinRedirectsToSlash(t *testing.T) {
	m := newCoverageModel(t)
	next, _, _ := m.executeChatCommand("/skill show review")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "/review") {
		t.Fatalf("/skill show review should point at /review, got:\n%s", last)
	}
}

func TestSlashSkill_RunBuiltinNudgesToTemplateSlash(t *testing.T) {
	m := newCoverageModel(t)
	next, _, _ := m.executeChatCommand("/skill run refactor readme.md")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "/refactor") {
		t.Fatalf("/skill run <builtin> should redirect to the dedicated slash, got:\n%s", last)
	}
}

func TestSlashContext_BriefReadsMagicDoc(t *testing.T) {
	m := newCoverageModel(t)
	next, _, _ := m.executeChatCommand("/context brief")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	// With no magic doc present the handler should explain, not crash.
	if strings.TrimSpace(last) == "" {
		t.Fatalf("/context brief should produce some output")
	}
}
