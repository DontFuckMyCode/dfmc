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
