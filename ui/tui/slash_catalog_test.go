package tui

import (
	"context"
	"strings"
	"testing"
)

// cliOnlySlashCommands enumerates slash verbs that intentionally dispatch to
// the "run from CLI" stub — this mirrors the dispatch branch in
// executeChatCommand and serves as a test-owned contract: if a new CLI-only
// command shows up in the catalog it must also show up here, or the author
// must wire a real TUI-side handler.
var cliOnlySlashCommands = map[string]bool{
	"init":       true,
	"completion": true,
	"man":        true,
	"serve":      true,
	"remote":     true,
	"plugin":     true,
	"skill":      true,
	"prompt":     true,
	"config":     true,
}

// TestEveryCatalogCommandDispatches walks the full slashCommandCatalog and
// asserts every entry has a live dispatch branch — none falls through to the
// "Unknown command" branch. This is the regression guard for "some slash
// commands silently do nothing" — the common failure mode when a new command
// lands in the picker but the executeChatCommand switch isn't updated.
func TestEveryCatalogCommandDispatches(t *testing.T) {
	m := NewModel(context.Background(), nil)
	catalog := m.slashCommandCatalog()
	if len(catalog) == 0 {
		t.Fatal("slashCommandCatalog is empty; something is very wrong")
	}

	for _, item := range catalog {
		// Only drive the first token. Subcommand entries ("conversation list")
		// are fine as-is; the top-level "conversation" token reaches the
		// dispatch switch either way.
		firstToken := strings.SplitN(strings.TrimSpace(item.Command), " ", 2)[0]
		input := "/" + firstToken

		t.Run(firstToken, func(t *testing.T) {
			fresh := NewModel(context.Background(), nil)
			_, _, handled := fresh.executeChatCommand(input)
			if !handled {
				t.Fatalf("catalog entry %q did not produce handled=true", input)
			}
			// After dispatch, the last transcript line is the one executeChatCommand
			// wrote. We only flag the unknown-command fallthrough by checking the
			// *prefix* of the final message — substring matching would false-
			// positive on commands like /diff that can surface the phrase
			// "Unknown command" inside the diff body of the test file itself.
			next, _, _ := m.executeChatCommand(input)
			mm, ok := next.(Model)
			if !ok {
				t.Fatalf("expected Model, got %T", next)
			}
			if len(mm.transcript) == 0 {
				return
			}
			last := mm.transcript[len(mm.transcript)-1].Content
			if strings.HasPrefix(last, "Unknown command:") || strings.HasPrefix(last, "Unknown chat command:") {
				t.Fatalf("catalog entry %q fell through to unknown-command branch: %q", input, last)
			}
		})
	}
}

// TestCatalogCliOnlyCommandsEmitHelpfulHint verifies that the enumerated
// CLI-only commands (/init, /serve, etc.) don't fail silently but instead
// produce the "run from CLI" transcript line that tells users how to proceed.
func TestCatalogCliOnlyCommandsEmitHelpfulHint(t *testing.T) {
	for name := range cliOnlySlashCommands {
		t.Run(name, func(t *testing.T) {
			m := NewModel(context.Background(), nil)
			next, _, handled := m.executeChatCommand("/" + name)
			if !handled {
				t.Fatalf("/%s should be handled (even as a CLI-only stub)", name)
			}
			mm := next.(Model)
			if len(mm.transcript) == 0 {
				t.Fatalf("/%s should emit a transcript line explaining the CLI route", name)
			}
			last := mm.transcript[len(mm.transcript)-1].Content
			if !strings.Contains(last, "CLI command") || !strings.Contains(last, "dfmc "+name) {
				t.Fatalf("/%s should tell the user to run `dfmc %s`, got:\n%s", name, name, last)
			}
		})
	}
}

// TestSuggestSlashCommand_SuggestsClosestOnTypo — the unknown-command branch
// should suggest a close match so the user recovers in one keystroke instead
// of opening /help.
func TestSuggestSlashCommand_SuggestsClosestOnTypo(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, _, handled := m.executeChatCommand("/revieww")
	if !handled {
		t.Fatalf("unknown commands still return handled=true")
	}
	mm := next.(Model)
	if len(mm.transcript) == 0 {
		t.Fatalf("unknown command should emit a transcript hint")
	}
	last := mm.transcript[len(mm.transcript)-1].Content
	if !strings.Contains(last, "review") {
		t.Fatalf("typo /revieww should suggest /review, got:\n%s", last)
	}
}

// TestStarterPromptsAllDispatch — every command offered on the welcome
// screen (digits 1..N) must route to a real handler, not the 'Unknown
// command' fallthrough. This guard catches drift between the starter list
// and the dispatch switch — e.g. a starter pointing at '/codemap' when the
// actual verb is '/map'.
func TestStarterPromptsAllDispatch(t *testing.T) {
	starters := defaultStarterPrompts()
	if len(starters) == 0 {
		t.Fatal("defaultStarterPrompts returned nothing; welcome screen would be empty")
	}
	for _, s := range starters {
		t.Run(s.Key+"-"+s.Title, func(t *testing.T) {
			// Strip trailing '@' (starter 2 primes the mention picker) and
			// any extra whitespace so we exercise the bare command.
			raw := strings.TrimSpace(strings.TrimSuffix(s.Cmd, "@"))
			if raw == "" {
				t.Fatalf("starter %q has empty Cmd", s.Key)
			}
			m := NewModel(context.Background(), nil)
			next, _, handled := m.executeChatCommand(raw)
			if !handled {
				t.Fatalf("starter %q (Cmd=%q) did not dispatch", s.Key, s.Cmd)
			}
			mm := next.(Model)
			if len(mm.transcript) == 0 {
				return
			}
			last := mm.transcript[len(mm.transcript)-1].Content
			if strings.HasPrefix(last, "Unknown command:") || strings.HasPrefix(last, "Unknown chat command:") {
				t.Fatalf("starter %q (Cmd=%q) fell through to unknown-command branch: %q", s.Key, s.Cmd, last)
			}
		})
	}
}

// TestUnknownSlashCommandEmitsHelpPointer — when no suggestion is close
// enough, the user still deserves a pointer to /help rather than silent
// failure.
func TestUnknownSlashCommandEmitsHelpPointer(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, _, handled := m.executeChatCommand("/zzzqqqxxx")
	if !handled {
		t.Fatalf("unknown commands are still handled (by definition)")
	}
	mm := next.(Model)
	if len(mm.transcript) == 0 {
		t.Fatalf("unknown slash should emit a transcript line")
	}
	last := mm.transcript[len(mm.transcript)-1].Content
	if !strings.Contains(last, "/help") {
		t.Fatalf("unknown slash with no suggestion should point at /help, got:\n%s", last)
	}
}
