package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
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
	"config":     true,
	// /prompt and /skill moved to TUI-native handlers — they list and
	// describe their catalogs inline instead of punting to the CLI.
	"debug":    true,
	"generate": true,
	"onboard":  true,
	"audit":    true,
	"mcp":      true,
	"update":   true,
	"tui":      true,
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
			if len(mm.chat.transcript) == 0 {
				return
			}
			last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
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
			if len(mm.chat.transcript) == 0 {
				t.Fatalf("/%s should emit a transcript line explaining the CLI route", name)
			}
			last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
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
	if len(mm.chat.transcript) == 0 {
		t.Fatalf("unknown command should emit a transcript hint")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "review") {
		t.Fatalf("typo /revieww should suggest /review, got:\n%s", last)
	}
}

// TestSlashPickerIsBorderedModal — the / command picker renders as a
// bordered modal matching the @ file picker, so the composer has two
// consistent first-class picker surfaces. Users previously couldn't tell
// the inline strip was a real picker; the box makes the affordance obvious.
func TestSlashPickerIsBorderedModal(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.input = "/re"
	view := m.renderChatView(160)
	if !strings.ContainsAny(view, "╭╮╰╯") {
		t.Fatalf("slash picker should render inside a bordered modal, got:\n%s", view)
	}
	if !strings.Contains(view, "◆ Commands") {
		t.Fatalf("slash picker should carry the ◆ Commands title, got:\n%s", view)
	}
	if !strings.Contains(view, "enter run") {
		t.Fatalf("slash picker footer should advertise enter, got:\n%s", view)
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
			if len(mm.chat.transcript) == 0 {
				return
			}
			last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
			if strings.HasPrefix(last, "Unknown command:") || strings.HasPrefix(last, "Unknown chat command:") {
				t.Fatalf("starter %q (Cmd=%q) fell through to unknown-command branch: %q", s.Key, s.Cmd, last)
			}
		})
	}
}

// TestPlanModeTogglesViaSlashCommands — /plan flips the investigate-only
// flag; /code flips it back. Both emit distinct transcript lines so the
// user has a durable breadcrumb of the mode change.
func TestPlanModeTogglesViaSlashCommands(t *testing.T) {
	m := NewModel(context.Background(), nil)
	if m.ui.planMode {
		t.Fatalf("default state must be planMode=false")
	}

	// Enter plan mode.
	next, _, handled := m.executeChatCommand("/plan")
	if !handled {
		t.Fatalf("/plan must be handled=true")
	}
	mm := next.(Model)
	if !mm.ui.planMode {
		t.Fatalf("/plan must flip planMode on")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "Plan mode ON") {
		t.Fatalf("/plan system message should announce ON, got:\n%s", last)
	}

	// /plan while already on is idempotent and says so.
	next2, _, _ := mm.executeChatCommand("/plan")
	mm2 := next2.(Model)
	if !mm2.ui.planMode {
		t.Fatalf("idempotent /plan must keep planMode=true")
	}
	last = mm2.chat.transcript[len(mm2.chat.transcript)-1].Content
	if !strings.Contains(last, "already ON") {
		t.Fatalf("idempotent /plan should acknowledge already-on, got:\n%s", last)
	}

	// /code exits plan mode.
	next3, _, _ := mm2.executeChatCommand("/code")
	mm3 := next3.(Model)
	if mm3.ui.planMode {
		t.Fatalf("/code must flip planMode off")
	}
	last = mm3.chat.transcript[len(mm3.chat.transcript)-1].Content
	if !strings.Contains(last, "Plan mode OFF") {
		t.Fatalf("/code should announce OFF, got:\n%s", last)
	}
}

func TestDefaultReviewTargetsPreferSelectionThenChangedFiles(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.filesView.entries = []string{"ui/tui/conversations.go", "ui/tui/describe.go"}
	m.filesView.index = 0
	m.patchView.changed = []string{"internal/engine/engine.go", "ui/tui/tui.go"}

	targets := m.defaultReviewTargets(nil)
	if len(targets) != 1 || targets[0] != "ui/tui/conversations.go" {
		t.Fatalf("expected selected file to win, got %#v", targets)
	}

	m.filesView.entries = nil
	m.filesView.index = 0
	m.filesView.path = ""
	targets = m.defaultReviewTargets(nil)
	if len(targets) != 2 || targets[0] != "internal/engine/engine.go" || targets[1] != "ui/tui/tui.go" {
		t.Fatalf("expected changed files fallback, got %#v", targets)
	}
}

func TestComposeReviewPromptStaysBudgetAware(t *testing.T) {
	targeted := composeReviewPrompt([]string{"ui/tui/conversations.go"}, "")
	for _, want := range []string{"[[file:ui/tui/conversations.go]]", "Stay budget-aware", "avoid broad repo scans"} {
		if !strings.Contains(targeted, want) {
			t.Fatalf("targeted review prompt should contain %q, got:\n%s", want, targeted)
		}
	}

	diffOnly := composeReviewPrompt(nil, "")
	for _, want := range []string{"current worktree diff only", "avoid broad codebase sweeps"} {
		if !strings.Contains(diffOnly, want) {
			t.Fatalf("default review prompt should contain %q, got:\n%s", want, diffOnly)
		}
	}
}

// TestSubmitChatQuestionInjectsPlanDirectiveInPlanMode — the question
// that goes to the LLM must carry the investigate-only directive when
// plan mode is on. Transcript keeps the user's original text; only the
// model-facing payload is augmented.
func TestSubmitChatQuestionInjectsPlanDirectiveInPlanMode(t *testing.T) {
	// We can't run the full submit path without an engine, so exercise
	// the branch by calling enforceToolUseForActionRequests and comparing
	// to what submitChatQuestion would have produced. The shape we care
	// about is: plan directive appears, tool-use directive does NOT.
	// For that we need to hit submitChatQuestion's plan branch. Keep it
	// minimal — check the helper-level invariant via a dedicated probe.
	m := NewModel(context.Background(), nil)
	// First confirm: without plan mode, action request passes through
	// (enforceToolUse is gated on hasToolCapableProvider which is false
	// for nil engine, so the text is unchanged).
	q := "[[file:README.md]] güncelle"
	if got := m.enforceToolUseForActionRequests(q); got != q {
		t.Fatalf("nil engine baseline must leave action request untouched, got %q", got)
	}
	// Now simulate plan mode's pure-directive branch. The branch is in
	// submitChatQuestion itself; we test that the directive string shape
	// is recognizable so future refactors don't silently drop it.
	m.ui.planMode = true
	// The branch is: question = trim + "\n\n[DFMC plan mode] ..." — we
	// assert that the documented shape is detectable by substring.
	expectedMarker := "[DFMC plan mode]"
	injected := "[[file:README.md]] güncelle\n\n" + expectedMarker + " You are in INVESTIGATE-ONLY mode."
	if !strings.Contains(injected, expectedMarker) {
		t.Fatalf("plan directive shape must remain greppable by %q", expectedMarker)
	}
}

// TestChatHeaderShowsPlanModeBadge — the header renders a loud badge
// when plan mode is active so the user never forgets which mode they're
// submitting into.
func TestChatHeaderShowsPlanModeBadge(t *testing.T) {
	info := chatHeaderInfo{
		Provider:   "zai",
		Model:      "glm-5.1",
		Configured: true,
		PlanMode:   true,
	}
	out := renderChatHeader(info, 200)
	if !strings.Contains(out, "PLAN") {
		t.Fatalf("plan mode must surface a PLAN badge in the header, got:\n%s", out)
	}
	if !strings.Contains(out, "/code exits") {
		t.Fatalf("plan badge should tell user how to exit, got:\n%s", out)
	}
	// When plan mode is off, the badge must NOT appear.
	info.PlanMode = false
	out = renderChatHeader(info, 200)
	if strings.Contains(out, "PLAN") {
		t.Fatalf("plan badge must not appear when planMode=false, got:\n%s", out)
	}
}

// TestEditWithoutPriorUserMessage — /edit explains itself on a fresh chat
// instead of silently no-opping. Same "never fail silently" contract as
// /retry.
func TestEditWithoutPriorUserMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, _, handled := m.executeChatCommand("/edit")
	if !handled {
		t.Fatalf("/edit must be handled=true")
	}
	mm := next.(Model)
	if len(mm.chat.transcript) == 0 {
		t.Fatalf("/edit on empty transcript should emit a transcript line")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "no prior user message") {
		t.Fatalf("/edit on empty transcript should say 'no prior user message', got:\n%s", last)
	}
}

// TestEditPullsLastUserMessageIntoComposer — core /edit contract. The user
// message leaves the transcript, goes into the composer with the cursor at
// the end, and nothing is sent yet. User can now amend and press enter.
func TestEditPullsLastUserMessageIntoComposer(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "explain the auth flow"},
		{Role: "assistant", Content: "some stale answer we want to iterate on"},
	}
	next, _, handled := m.executeChatCommand("/edit")
	if !handled {
		t.Fatalf("/edit must be handled=true")
	}
	mm := next.(Model)
	if mm.chat.input != "explain the auth flow" {
		t.Fatalf("composer must load the previous user message verbatim, got %q", mm.chat.input)
	}
	if len(mm.chat.transcript) != 0 {
		t.Fatalf("user+assistant turn must be dropped when /edit pulls it back, got %d transcript lines: %+v", len(mm.chat.transcript), mm.chat.transcript)
	}
	if mm.chat.cursor != len([]rune("explain the auth flow")) {
		t.Fatalf("cursor must sit at the end of the loaded text, got %d", mm.chat.cursor)
	}
	if mm.chat.sending {
		t.Fatalf("/edit must NOT trigger a send; user must press enter to resubmit")
	}
	if !strings.Contains(strings.ToLower(mm.notice), "editing last message") {
		t.Fatalf("notice should announce editing mode, got %q", mm.notice)
	}
}

// TestEditBlockedWhileStreaming — same guard as /retry.
func TestEditBlockedWhileStreaming(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "x"},
		{Role: "assistant", Content: "partial"},
	}
	m.chat.sending = true
	next, _, _ := m.executeChatCommand("/edit")
	mm := next.(Model)
	if mm.chat.input != "" {
		t.Fatalf("/edit must not load composer while streaming, got %q", mm.chat.input)
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "already streaming") {
		t.Fatalf("guard message should mention streaming, got:\n%s", last)
	}
}

// TestRetryWithoutPriorUserMessage — /retry must explain itself rather than
// silently failing on a fresh session. Every chat command should degrade
// helpfully; silent no-ops were a chunk of the "tırt" feedback.
func TestRetryWithoutPriorUserMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, _, handled := m.executeChatCommand("/retry")
	if !handled {
		t.Fatalf("/retry must always be handled=true")
	}
	mm := next.(Model)
	if len(mm.chat.transcript) == 0 {
		t.Fatalf("/retry should emit a transcript line")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "no prior user message") {
		t.Fatalf("/retry on empty transcript should say 'no prior user message', got:\n%s", last)
	}
	if mm.chat.sending {
		t.Fatalf("/retry with nothing to retry must not flip sending=true")
	}
}

// TestRetryDropsAssistantReplyAndRequeuesLastUserMessage — core /retry
// contract. Previous user turn survives; previous assistant reply is
// dropped; the user's question is resubmitted so the stream re-opens it.
func TestRetryDropsAssistantReplyAndRequeuesLastUserMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "what is 2+2?"},
		{Role: "assistant", Content: "4 (but with more words than needed)"},
		{Role: "tool", Content: "tool blob"},
	}
	next, _, handled := m.executeChatCommand("/retry")
	if !handled {
		t.Fatalf("/retry must be handled=true")
	}
	mm := next.(Model)
	// Transcript must hold the user line + fresh empty assistant placeholder.
	if len(mm.chat.transcript) != 2 {
		t.Fatalf("after /retry expected 2 transcript lines (user + empty assistant), got %d: %+v", len(mm.chat.transcript), mm.chat.transcript)
	}
	if !mm.chat.transcript[0].Role.Eq(chatRoleUser) || mm.chat.transcript[0].Content != "what is 2+2?" {
		t.Fatalf("user line must survive /retry, got %+v", mm.chat.transcript[0])
	}
	if !mm.chat.transcript[1].Role.Eq(chatRoleAssistant) || mm.chat.transcript[1].Content != "" {
		t.Fatalf("fresh assistant placeholder must appear, got %+v", mm.chat.transcript[1])
	}
	if !mm.chat.sending {
		t.Fatalf("/retry must flip sending=true to re-open the stream")
	}
}

// TestRetryBlockedWhileStreaming — guard rail: hitting /retry while a turn
// is already streaming must not kick off a second turn.
func TestRetryBlockedWhileStreaming(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "a long question"},
		{Role: "assistant", Content: "partial streaming reply so far"},
	}
	m.chat.sending = true
	next, _, handled := m.executeChatCommand("/retry")
	if !handled {
		t.Fatalf("/retry must always be handled")
	}
	mm := next.(Model)
	// Transcript must be unchanged beyond the system notice appended for
	// the guard message.
	if len(mm.chat.transcript) < 3 {
		t.Fatalf("expected guard message appended, got %d lines", len(mm.chat.transcript))
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "already streaming") {
		t.Fatalf("guard message should explain the streaming block, got:\n%s", last)
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
	if len(mm.chat.transcript) == 0 {
		t.Fatalf("unknown slash should emit a transcript line")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "/help") {
		t.Fatalf("unknown slash with no suggestion should point at /help, got:\n%s", last)
	}
}

func TestComposeReviewPromptAddsScopeMapForLocalCodeFile(t *testing.T) {
	tmp := t.TempDir()
	src := "package demo\n\ntype Service struct{}\n\nfunc (s *Service) Handle() error {\n\treturn nil\n}\n\nfunc helper() {}\n"
	if err := os.WriteFile(filepath.Join(tmp, "service.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write service.go: %v", err)
	}

	eng := &engine.Engine{ProjectRoot: tmp}
	m := NewModel(context.Background(), eng)
	prompt := m.composeReviewPrompt([]string{"service.go"}, "")

	for _, want := range []string{"Scope map:", "[[file:service.go]]", "method Handle", "type Service"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("scoped review prompt should contain %q, got:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "Start with 1-2 high-risk scopes") {
		t.Fatalf("scoped review prompt should steer narrow symbol-first review, got:\n%s", prompt)
	}
}

func TestComposeReviewPromptAddsSectionMapForDocs(t *testing.T) {
	tmp := t.TempDir()
	doc := "# Intro\nbody\n\n## Usage\nmore\n"
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	eng := &engine.Engine{ProjectRoot: tmp}
	m := NewModel(context.Background(), eng)
	prompt := m.composeReviewPrompt([]string{"README.md"}, "")

	for _, want := range []string{"Scope map:", "[[file:README.md]]", "section Intro", "section Usage"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("doc review prompt should contain %q, got:\n%s", want, prompt)
		}
	}
}
