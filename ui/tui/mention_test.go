package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMentionRanker_PrefersExactBasenameAndBoostsRecency(t *testing.T) {
	files := []string{
		"internal/util/pkg.go",
		"ui/cli/utilities.go",
		"pkg/types/util_test.go",
		"util.go",
	}
	recent := []string{"pkg/types/util_test.go"}

	ranker := newMentionRanker(files, recent)
	got := ranker.rank("util", 5)
	if len(got) == 0 {
		t.Fatalf("expected matches, got none")
	}
	// util.go is the exact basename match so it should always be #1.
	if got[0].path != "util.go" {
		t.Fatalf("expected util.go first (exact basename), got %q", got[0].path)
	}
	// util_test.go has recency bonus; it should beat internal/util/pkg.go
	// (which has a higher raw score but no recency).
	rankOf := func(path string) int {
		for i, c := range got {
			if c.path == path {
				return i
			}
		}
		return -1
	}
	if rankOf("pkg/types/util_test.go") < 0 {
		t.Fatalf("recent file missing from ranking: %v", got)
	}
	if rankOf("pkg/types/util_test.go") > rankOf("ui/cli/utilities.go") {
		t.Fatalf("recency bonus did not lift util_test.go above utilities.go: %v", got)
	}
}

func TestMentionRanker_SubsequenceMatching(t *testing.T) {
	files := []string{
		"ui/tui/tui.go",
		"internal/engine/engine.go",
		"pkg/types/types.go",
	}
	got := newMentionRanker(files, nil).rank("eeng", 5)
	if len(got) == 0 || got[0].path != "internal/engine/engine.go" {
		t.Fatalf("expected subsequence match to hit engine.go, got %+v", got)
	}
}

func TestMentionRanker_EmptyQueryReturnsRecentFirst(t *testing.T) {
	files := []string{
		"a.go",
		"b.go",
		"c.go",
	}
	recent := []string{"c.go", "b.go"}
	got := newMentionRanker(files, recent).rank("", 3)
	if len(got) != 3 {
		t.Fatalf("expected all three files back, got %d", len(got))
	}
	if got[0].path != "c.go" {
		t.Fatalf("expected c.go first (most recent), got %q", got[0].path)
	}
	if got[1].path != "b.go" {
		t.Fatalf("expected b.go second (next recent), got %q", got[1].path)
	}
}

func TestResolveMentionQuery_ConfidenceFloor(t *testing.T) {
	files := []string{"apps/web/frontend/src/components/Navbar.tsx"}

	// Tight substring — should resolve.
	if path, ok := resolveMentionQuery(files, nil, "Navbar"); !ok || !strings.HasSuffix(path, "Navbar.tsx") {
		t.Fatalf("expected Navbar to resolve, got %q, %v", path, ok)
	}

	// Gibberish that happens to share a character — subsequence match scores
	// below the 400 floor, so we should leave the literal @xyz alone.
	if _, ok := resolveMentionQuery(files, nil, "zq"); ok {
		t.Fatalf("expected low-confidence match to be rejected")
	}
}

func TestExpandAtFileMentionsWithRecent_PicksBestOnAmbiguity(t *testing.T) {
	files := []string{
		"internal/memory/store.go",
		"internal/storage/store.go",
	}
	// Recency should pick storage over memory even though they tie on score.
	out := expandAtFileMentionsWithRecent("please read @store", files, []string{"internal/storage/store.go"})
	if !strings.Contains(out, "internal/storage/store.go") {
		t.Fatalf("expected storage/store.go (recent) to win, got %q", out)
	}
	// Without recency, ties break on alphabetical — memory wins.
	out2 := expandAtFileMentionsWithRecent("please read @store", files, nil)
	if !strings.Contains(out2, "internal/memory/store.go") {
		t.Fatalf("expected memory/store.go (alphabetical tiebreak), got %q", out2)
	}
}

func TestSplitMentionToken_RangeForms(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantPath, want string
	}{
		{"bare", "auth.go", "auth.go", ""},
		{"colon range", "auth.go:10-50", "auth.go", "#L10-L50"},
		{"colon single", "auth.go:42", "auth.go", "#L42"},
		{"hash-L range", "auth.go#L10-L50", "auth.go", "#L10-L50"},
		{"hash-L single", "auth.go#L42", "auth.go", "#L42"},
		{"nested path", "internal/auth/token.go:120-180", "internal/auth/token.go", "#L120-L180"},
		{"malformed keeps bare", "auth.go:foo-bar", "auth.go:foo-bar", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotRange := splitMentionToken(tc.in)
			if gotPath != tc.wantPath || gotRange != tc.want {
				t.Fatalf("splitMentionToken(%q) = (%q,%q), want (%q,%q)",
					tc.in, gotPath, gotRange, tc.wantPath, tc.want)
			}
		})
	}
}

func TestExpandAtFileMentionsWithRecent_PreservesRangeSuffix(t *testing.T) {
	files := []string{"internal/auth/token.go"}
	out := expandAtFileMentionsWithRecent("look at @token.go:120-180 please", files, nil)
	want := "[[file:internal/auth/token.go#L120-L180]]"
	if !strings.Contains(out, want) {
		t.Fatalf("expected %q in %q", want, out)
	}
}

func TestMentionRanker_HidesBinaryByDefault(t *testing.T) {
	files := []string{
		"assets/logo.png",
		"internal/auth/logo.go",
	}
	got := newMentionRanker(files, nil).rank("logo", 5)
	// With a generic query, the .png should be filtered out.
	for _, c := range got {
		if strings.HasSuffix(c.path, ".png") {
			t.Fatalf("expected .png to be filtered, got %+v", got)
		}
	}
	if len(got) != 1 || got[0].path != "internal/auth/logo.go" {
		t.Fatalf("expected only logo.go, got %+v", got)
	}
}

func TestMentionRanker_ShowsBinaryWhenExtensionTyped(t *testing.T) {
	files := []string{
		"assets/logo.png",
		"internal/auth/logo.go",
	}
	// Explicitly typing the .png extension relaxes the filter.
	got := newMentionRanker(files, nil).rank("logo.png", 5)
	found := false
	for _, c := range got {
		if c.path == "assets/logo.png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected .png file to be shown when extension is typed, got %+v", got)
	}
}

func TestMentionRanker_FlagsRecent(t *testing.T) {
	files := []string{"a.go", "b.go"}
	recent := []string{"b.go"}
	got := newMentionRanker(files, recent).rank("", 5)
	byPath := map[string]bool{}
	for _, c := range got {
		byPath[c.path] = c.recent
	}
	if !byPath["b.go"] {
		t.Fatalf("expected b.go flagged recent, got %+v", got)
	}
	if byPath["a.go"] {
		t.Fatalf("expected a.go NOT flagged recent, got %+v", got)
	}
}

func TestActiveMentionQuery_ReturnsRangeSuffix(t *testing.T) {
	query, suffix, ok := activeMentionQuery("please read @auth.go:10-50")
	if !ok {
		t.Fatalf("expected active mention, got none")
	}
	if query != "auth.go" {
		t.Fatalf("expected query=auth.go, got %q", query)
	}
	if suffix != "#L10-L50" {
		t.Fatalf("expected suffix=#L10-L50, got %q", suffix)
	}
}

// Chat suggestion UX: once the user types `@`, the picker must show
// *something* on every frame — a loading hint, an empty-state, or match
// rows. Silent picker was the source of the "@ doesn't work" complaint.

func TestBuildChatSuggestionState_MentionActiveEvenWithoutFiles(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "please read @"
	m.files = nil
	state := m.buildChatSuggestionState()
	if !state.mentionActive {
		t.Fatalf("mentionActive should be true when input has trailing @, regardless of file index")
	}
	if len(state.quickActions) != 0 {
		t.Fatalf("mentionActive should suppress quick actions, got %d", len(state.quickActions))
	}
}

func TestRenderChatView_MentionPicker_ShowsIndexingHintWhenFilesEmpty(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "please read @auth"
	m.files = nil
	view := m.renderChatView(120)
	if !strings.Contains(view, "File Picker") {
		t.Fatalf("expected File Picker modal header when @ active, got:\n%s", view)
	}
	if !strings.Contains(view, "Indexing project files") {
		t.Fatalf("empty file index should surface an indexing hint, got:\n%s", view)
	}
}

func TestRenderChatView_MentionPicker_ShowsNoMatchCopyWhenQueryHasNoHit(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "skim @nothingmatchesthis"
	m.files = []string{"internal/auth/token.go", "ui/cli/cli.go"}
	view := m.renderChatView(120)
	if !strings.Contains(view, "File Picker") {
		t.Fatalf("expected File Picker modal header, got:\n%s", view)
	}
	if !strings.Contains(view, "No files matched 'nothingmatchesthis'") {
		t.Fatalf("non-matching query should surface a no-match hint, got:\n%s", view)
	}
}

// TestRenderChatView_MentionPicker_IsBorderedModal — pin the visual
// promotion of the @ picker from an inline suggestion strip to a bordered
// modal. Users couldn't tell the old one was a real picker they should
// commit to; the box sells "this is the file picker, drive it or esc out".
func TestRenderChatView_MentionPicker_IsBorderedModal(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "review @"
	m.files = []string{"internal/auth/token.go", "ui/cli/cli.go"}
	view := m.renderChatView(160)
	// Bordered box — rounded unicode corners are the cheapest proof.
	if !strings.ContainsAny(view, "╭╮╰╯") {
		t.Fatalf("expected a bordered modal box, got:\n%s", view)
	}
	if !strings.Contains(view, "◆ File Picker") {
		t.Fatalf("expected the 'File Picker' modal title, got:\n%s", view)
	}
	// Footer must keep the keybindings visible on every frame.
	if !strings.Contains(view, "tab/enter insert") || !strings.Contains(view, "esc cancel") {
		t.Fatalf("footer should surface the commit/cancel keys, got:\n%s", view)
	}
}

func TestRenderChatView_MentionPicker_ListsMatches(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "please read @token"
	m.files = []string{"internal/auth/token.go", "ui/cli/cli.go"}
	view := m.renderChatView(160)
	if !strings.Contains(view, "token.go") {
		t.Fatalf("mention picker should list the match, got:\n%s", view)
	}
	// The hint should carry the "N/M files" progress counter.
	if !strings.Contains(view, "/2 files") {
		t.Fatalf("match header should include file count, got:\n%s", view)
	}
}

// TestRenderChatView_MentionPickerOwnsTailRealEstate — when the @ picker is
// active it must be the dominant element under the composer. The context
// strip, Slash Assist hints, and Quick actions all got pushed *above* the
// picker in earlier builds, and in short terminals that meant the modal
// rendered below the fold — so from the user's perspective "@ doesn't work".
// This test pins the new contract: while the mention picker is up, those
// competing decorations are suppressed, and the picker sits immediately
// under the Input box.
func TestRenderChatView_MentionPickerOwnsTailRealEstate(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.pinnedFile = "internal/auth/token.go" // would normally surface in context strip
	m.input = "review @"
	m.files = []string{"internal/auth/token.go", "ui/cli/cli.go"}
	view := m.renderChatView(160)

	if !strings.Contains(view, "◆ File Picker") {
		t.Fatalf("mention picker should render, got:\n%s", view)
	}
	// The context strip carries a distinctive "📎 context" prefix and must
	// be suppressed while the picker is active so it doesn't compete for
	// space under the input box. (The chat header also surfaces pinned
	// info — that's fine, it's not in the tail.)
	if strings.Contains(view, "📎 context") {
		t.Fatalf("context strip should be suppressed under active mention picker:\n%s", view)
	}
	// Slash Assist hints and Quick actions are the other common tail fillers
	// that used to push the modal off-screen.
	if strings.Contains(view, "Slash Assist") {
		t.Fatalf("Slash Assist hints should be suppressed under active mention picker:\n%s", view)
	}
	if strings.Contains(view, "Quick actions") {
		t.Fatalf("Quick actions should be suppressed under active mention picker:\n%s", view)
	}
}

// TestRenderChatView_MentionPickerSitsUnderInputBox — pin ordering so the
// Input box is followed by the modal, not by any other tail block. If a
// future contributor adds a new tail decoration that sneaks in between them,
// the picker will no longer read as "the next thing after input" and this
// test fails on purpose.
func TestRenderChatView_MentionPickerSitsUnderInputBox(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "hi @"
	m.files = []string{"internal/auth/token.go"}
	view := m.renderChatView(160)

	inputIdx := strings.Index(view, "› Input")
	pickerIdx := strings.Index(view, "◆ File Picker")
	if inputIdx < 0 || pickerIdx < 0 {
		t.Fatalf("missing Input header or picker title in view:\n%s", view)
	}
	if pickerIdx < inputIdx {
		t.Fatalf("picker should render below the Input box, got picker at %d before input at %d", pickerIdx, inputIdx)
	}
	// Between the Input header and the picker title nothing other than
	// whitespace and the input box chrome should appear. A cheap proxy:
	// the slice between them must not contain known competing titles.
	between := view[inputIdx:pickerIdx]
	for _, forbidden := range []string{"Slash Assist", "Quick actions", "Command args", "📎 context"} {
		if strings.Contains(between, forbidden) {
			t.Fatalf("found %q between Input and File Picker (should be nothing but input chrome):\n%s", forbidden, between)
		}
	}
}

// TestRenderContextStrip_HiddenWhenComposerEmpty — the context strip must
// not paint a dead bar when there's nothing attached. Empty composer with
// no pinned file and no markers should yield "".
func TestRenderContextStrip_HiddenWhenComposerEmpty(t *testing.T) {
	m := NewModel(context.Background(), nil)
	got := m.renderContextStrip(120)
	if got != "" {
		t.Fatalf("empty composer should produce empty strip, got %q", got)
	}
}

// TestRenderContextStrip_ShowsMarkersAndFences — users need to see what the
// context manager will actually pick up from their message before sending.
// Inline [[file:...]] markers, fenced blocks, and @refs should each show up.
func TestRenderContextStrip_ShowsMarkersAndFences(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "Review [[file:a.go]] and [[file:b.go#L10-L50]] plus @c.go\n" +
		"```go\nfunc main(){}\n```"
	got := m.renderContextStrip(160)
	if !strings.Contains(got, "markers:") {
		t.Fatalf("expected markers count, got %q", got)
	}
	if !strings.Contains(got, "@refs:") {
		t.Fatalf("expected @refs count, got %q", got)
	}
	if !strings.Contains(got, "fenced:") {
		t.Fatalf("expected fenced count, got %q", got)
	}
	if !strings.Contains(got, "chars:") {
		t.Fatalf("expected chars count, got %q", got)
	}
}

// TestCtrlTOpensFilePicker — the AltGr-@ fallback. Turkish (Q) keyboards
// on MinTTY/Git Bash can swallow the '@' rune silently; Ctrl+T is the
// guaranteed-deliverable alternative that puts '@' at the cursor and
// kicks the existing mention picker. End-to-end check: one keypress →
// picker modal appears in the render.
func TestCtrlTOpensFilePicker(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{"internal/auth/token.go", "ui/tui/tui.go"}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	mm := out.(Model)
	if !strings.Contains(mm.input, "@") {
		t.Fatalf("ctrl+t must put '@' at the cursor; got input=%q", mm.input)
	}
	if mm.chatCursor != len([]rune(mm.input)) {
		t.Fatalf("cursor must sit after the inserted '@', got %d (input len=%d)", mm.chatCursor, len([]rune(mm.input)))
	}
	view := mm.renderChatView(120)
	if !strings.Contains(view, "◆ File Picker") {
		t.Fatalf("ctrl+t must surface the file picker modal:\n%s", view)
	}
}

// TestCtrlTPrependsSpaceMidWord — if the cursor is glued to a word when
// Ctrl+T fires, we need a space before the '@' so the mention token is
// just '@' (not "wordhello@"), which is what activeMentionQuery scans.
func TestCtrlTPrependsSpaceMidWord(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("hello")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	mm := out.(Model)
	if mm.input != "hello @" {
		t.Fatalf("ctrl+t after a word should prepend a space, got %q", mm.input)
	}
}

// TestSlashFileOpensPicker — /file is the slash-command alias for @
// and Ctrl+T. Useful if neither key is available on the user's terminal.
func TestSlashFileOpensPicker(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.files = []string{"internal/auth/token.go"}
	next, _, handled := m.executeChatCommand("/file")
	if !handled {
		t.Fatalf("/file must be handled=true")
	}
	mm := next.(Model)
	if mm.input != "@" {
		t.Fatalf("/file should leave composer primed with '@', got %q", mm.input)
	}
	view := mm.renderChatView(120)
	if !strings.Contains(view, "◆ File Picker") {
		t.Fatalf("/file must surface the file picker modal:\n%s", view)
	}
}

// TestAtKeyWithNonRunesKeyType_StillInsertsAt — defensive regression for
// the "@ yemiyor" bug on Turkish keyboards / Git Bash / MinTTY. When
// bubbletea delivers a KeyMsg whose Type ISN'T KeyRunes but whose Runes
// slice still contains '@' (AltGr+Q on Windows with Unix-TTY emulation
// can arrive as KeyCtrlQ{Alt:true, Runes:['@']}), the composer must still
// insert the '@' so the mention picker can fire.
func TestAtKeyWithNonRunesKeyType_StillInsertsAt(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	// Worst case — Type is KeyCtrlQ (what AltGr+Q can look like), Alt is
	// set, but Runes carries the actual '@' the user intended. The old
	// switch had no case for this and the key was eaten silently.
	out, _ := m.Update(tea.KeyMsg{
		Type:  tea.KeyCtrlQ,
		Runes: []rune("@"),
		Alt:   true,
	})
	mm := out.(Model)
	if !strings.Contains(mm.input, "@") {
		t.Fatalf("@ rune must reach the input buffer even when Type is not KeyRunes, got %q", mm.input)
	}
	// And the full render must show the file picker now that the @ is in.
	mm.files = []string{"ui/tui/tui.go"}
	view := mm.renderChatView(120)
	if !strings.Contains(view, "◆ File Picker") {
		t.Fatalf("mention picker must render after @ arrives via fallback, got:\n%s", view)
	}
}

// TestControlKeyWithNoRunesIsIgnored — the fallback must NOT insert
// anything for bare control sequences (e.g. Ctrl+C with Runes=nil).
// Otherwise we'd double-fire on every control key.
func TestControlKeyWithNoRunesIsIgnored(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	before := m.input
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	mm := out.(Model)
	if mm.input != before {
		t.Fatalf("control key with empty Runes must not mutate input, got %q", mm.input)
	}
}

// TestRenderContextStrip_ShowsTokenEstimate — chars alone don't answer the
// question that matters: "will this fit in the provider's context window?".
// The strip must carry a token count so the composer has real budget
// feedback, not just char count.
func TestRenderContextStrip_ShowsTokenEstimate(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "Explain the auth flow in this project and propose improvements."
	got := m.renderContextStrip(160)
	if !strings.Contains(got, "tokens:") {
		t.Fatalf("expected tokens count in strip, got %q", got)
	}
	// Heuristic counter always gives at least 1 token for non-empty input.
	if !strings.Contains(got, "~") {
		t.Fatalf("token count should lead with '~' (heuristic marker), got %q", got)
	}
}

// TestRenderContextStrip_TokenBudgetPercentWhenProviderKnown — when the
// provider profile reports a MaxContext, the strip must translate the raw
// token count into "% of budget" so users know how close they are to the
// limit at a glance.
func TestRenderContextStrip_TokenBudgetPercentWhenProviderKnown(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "short question"
	m.status.ProviderProfile.MaxContext = 200000
	got := m.renderContextStrip(200)
	if !strings.Contains(got, "% of 200000") {
		t.Fatalf("expected budget percent suffix, got %q", got)
	}
}

// TestRenderContextStrip_ShowsPinned — the pinned file is the most stable
// piece of context and must surface even when nothing else is attached.
func TestRenderContextStrip_ShowsPinned(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.pinnedFile = "internal/auth/token.go"
	got := m.renderContextStrip(120)
	if !strings.Contains(got, "pinned:") || !strings.Contains(got, "internal/auth/token.go") {
		t.Fatalf("expected pinned file in strip, got %q", got)
	}
}

// TestCountHelpers — the small counting functions drive the strip; pin
// their edge cases so future callers can rely on them.
func TestCountHelpers(t *testing.T) {
	if n := countFileMarkers("[[file:a.go]] and [[file:b.go]]"); n != 2 {
		t.Errorf("countFileMarkers: want 2, got %d", n)
	}
	if n := countFileMarkers("no marker here"); n != 0 {
		t.Errorf("countFileMarkers: want 0, got %d", n)
	}
	if n := countFencedBlocks("```\nfoo\n```"); n != 1 {
		t.Errorf("countFencedBlocks: complete pair = 1, got %d", n)
	}
	if n := countFencedBlocks("```\nfoo\n"); n != 0 {
		t.Errorf("countFencedBlocks: odd fence = 0, got %d", n)
	}
	if n := countAtMentions("hi @foo and @bar but email@x.com"); n != 2 {
		t.Errorf("countAtMentions: want 2 (email@ not mid-token), got %d", n)
	}
	if n := countAtMentions("@only"); n != 1 {
		t.Errorf("countAtMentions: leading @ counts, got %d", n)
	}
}

// TestMentionFlow_EndToEnd — full user flow: type @, filter, tab-commit.
// Verifies the marker lands in the input and the picker closes cleanly.
// This is the regression anchor for the user-facing complaint that "@ file
// selection doesn't work" — the whole flow runs here, no mocks.
func TestMentionFlow_EndToEnd(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{
		"internal/auth/token.go",
		"internal/auth/session.go",
		"ui/cli/cli.go",
		"ui/tui/tui.go",
	}

	// Step 1: type @ — mention picker should activate.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = out.(Model)
	state := m.buildChatSuggestionState()
	if !state.mentionActive {
		t.Fatalf("typing @ should activate mention picker, state=%+v", state)
	}
	if len(state.mentionSuggestions) == 0 {
		t.Fatalf("empty @ query should list recent files, got 0 suggestions")
	}

	// Step 2: type 'token' — picker should filter to auth/token.go.
	for _, r := range "token" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	state = m.buildChatSuggestionState()
	if state.mentionQuery != "token" {
		t.Fatalf("expected query=token, got %q", state.mentionQuery)
	}
	foundToken := false
	for _, row := range state.mentionSuggestions {
		if strings.HasSuffix(row.Path, "token.go") {
			foundToken = true
			break
		}
	}
	if !foundToken {
		t.Fatalf("filtered list should include token.go, got %+v", state.mentionSuggestions)
	}

	// Step 3: press tab — marker must replace @token in the input.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if !strings.Contains(m.input, "[[file:internal/auth/token.go]]") {
		t.Fatalf("tab should rewrite @token to a file marker, got input=%q", m.input)
	}
	if strings.Contains(m.input, "@token") {
		t.Fatalf("after commit, the raw @token should be gone, got input=%q", m.input)
	}

	// Step 4: picker should no longer be active.
	state = m.buildChatSuggestionState()
	if state.mentionActive {
		t.Fatalf("after commit, mention picker should be inactive, got active")
	}
}

// TestMentionFlow_EnterCommitsSelection — same as above but using Enter
// instead of Tab. Enter is the most discoverable commit key and users
// might not realise Tab also works.
func TestMentionFlow_EnterCommitsSelection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{"internal/auth/token.go", "ui/cli/cli.go"}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = out.(Model)
	for _, r := range "tok" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if !strings.Contains(m.input, "[[file:internal/auth/token.go]]") {
		t.Fatalf("enter should also commit the selection, got input=%q", m.input)
	}
	// Enter must NOT have sent the message (turn is still unsent).
	if m.sending {
		t.Fatalf("enter during mention-active state should insert, not submit; got m.sending=true")
	}
}

// TestMentionFlow_PickerNavigation — arrow-down selects the next match and
// a subsequent commit picks the highlighted row, not always row 0.
func TestMentionFlow_PickerNavigation(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{
		"internal/auth/token.go",
		"internal/auth/session.go",
		"internal/auth/middleware.go",
	}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = out.(Model)
	for _, r := range "auth" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	state := m.buildChatSuggestionState()
	if len(state.mentionSuggestions) < 2 {
		t.Fatalf("need ≥2 auth matches for navigation test, got %d", len(state.mentionSuggestions))
	}
	firstPath := state.mentionSuggestions[0].Path

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(Model)
	if m.mentionIndex != 1 {
		t.Fatalf("KeyDown should advance mentionIndex to 1, got %d", m.mentionIndex)
	}

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	state = m.buildChatSuggestionState()
	// The inserted marker should be the SECOND result, not the first.
	secondPath := firstPath // placeholder; rebuild suggestions below
	_ = secondPath
	// After commit, input contains [[file:<picked>]]. Verify the picked path
	// isn't firstPath (proving navigation took effect).
	if strings.Contains(m.input, "[[file:"+firstPath+"]]") {
		t.Fatalf("after KeyDown+Tab, expected picker to commit the SECOND row, but got first row %q in input %q", firstPath, m.input)
	}
}

func TestHandleChatKey_AtTriggersFileReloadWhenIndexEmpty(t *testing.T) {
	// Can't construct a real engine in a unit test (no store); this exercises
	// only the early-return guard: when eng is nil we must NOT dispatch a
	// reload cmd (which would panic on nil engine). The inverse path — eng
	// non-nil — is covered by the integration suite.
	m := NewModel(context.Background(), nil)
	m.activeTab = 0 // Chat tab
	m.files = nil
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if cmd != nil {
		// Only expected when eng != nil, which we didn't provide.
		t.Fatalf("nil engine should not produce a reload cmd on @, got %T", cmd)
	}
}
