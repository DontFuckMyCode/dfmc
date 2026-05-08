package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTurnActionsTestModel() Model {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleUser, Content: "explain the auth flow", Timestamp: time.Now()},
		{Role: chatRoleAssistant, Content: "the client sends a bearer token; the server verifies it.", Timestamp: time.Now()},
		{Role: chatRoleUser, Content: "what about refresh?", Timestamp: time.Now()},
		{Role: chatRoleAssistant, Content: "refresh tokens are exchanged at /auth/refresh.", Timestamp: time.Now()},
	}
	return m
}

func TestParseAssistantTurnArg(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		ok   bool
		desc string
	}{
		{"3", 3, true, "positive integer"},
		{" 12 ", 12, true, "trims whitespace"},
		{"", 0, false, "empty"},
		{"0", 0, false, "zero rejected — turns are 1-based"},
		{"-1", 0, false, "negative rejected"},
		{"abc", 0, false, "non-numeric"},
		{"3.5", 0, false, "fractional"},
	}
	for _, c := range cases {
		got, ok := parseAssistantTurnArg(c.in)
		if got != c.n || ok != c.ok {
			t.Errorf("%s: parseAssistantTurnArg(%q) = (%d,%v); want (%d,%v)", c.desc, c.in, got, ok, c.n, c.ok)
		}
	}
}

func TestFindAssistantTurnWalksTranscript(t *testing.T) {
	m := newTurnActionsTestModel()
	if got := findAssistantTurn(m.chat.transcript, 1); got != 1 {
		t.Errorf("turn 1 should be at transcript index 1, got %d", got)
	}
	if got := findAssistantTurn(m.chat.transcript, 2); got != 3 {
		t.Errorf("turn 2 should be at transcript index 3, got %d", got)
	}
	if got := findAssistantTurn(m.chat.transcript, 3); got != -1 {
		t.Errorf("missing turn should return -1, got %d", got)
	}
	if got := findAssistantTurn(m.chat.transcript, 0); got != -1 {
		t.Errorf("turn 0 should return -1 (1-based), got %d", got)
	}
}

// TestRenderAssistantTurnChipsShowsAffordances — under each finished
// assistant bubble, the chip line advertises /pin /fork /save with the
// turn number so the user can act on the answer without selecting it
// first. When pinned, the chip flips to ★ and offers /unpin.
func TestRenderAssistantTurnChipsShowsAffordances(t *testing.T) {
	m := newTurnActionsTestModel()

	chip := m.renderAssistantTurnChips(2, 120)
	for _, want := range []string{"/pin 2", "/fork 2", "/save 2"} {
		if !strings.Contains(chip, want) {
			t.Errorf("chip line should advertise %q, got: %q", want, chip)
		}
	}
	if strings.Contains(chip, "★") {
		t.Errorf("unpinned turn should not carry ★ marker, got: %q", chip)
	}

	m.chat.pinnedAssistantTurns = map[int]bool{2: true}
	pinnedChip := m.renderAssistantTurnChips(2, 120)
	if !strings.Contains(pinnedChip, "★ pinned") {
		t.Errorf("pinned turn should carry ★ pinned marker, got: %q", pinnedChip)
	}
	if !strings.Contains(pinnedChip, "/unpin 2") {
		t.Errorf("pinned chip should offer /unpin, got: %q", pinnedChip)
	}
	if strings.Contains(pinnedChip, "/pin 2") {
		t.Errorf("pinned chip should not offer /pin alongside /unpin, got: %q", pinnedChip)
	}
}

func TestHandlePinTurnSlashTogglesAnchor(t *testing.T) {
	m := newTurnActionsTestModel()

	out, _, ok := m.handlePinTurnSlash(2, true)
	if !ok {
		t.Fatal("/pin should always return handled=true")
	}
	pinned := out.(Model).chat.pinnedAssistantTurns
	if !pinned[2] {
		t.Fatalf("expected turn 2 pinned, got %#v", pinned)
	}

	out, _, _ = out.(Model).handlePinTurnSlash(2, false)
	pinned = out.(Model).chat.pinnedAssistantTurns
	if pinned[2] {
		t.Fatalf("expected turn 2 unpinned, got %#v", pinned)
	}
}

func TestHandlePinTurnSlashRejectsMissingTurn(t *testing.T) {
	m := newTurnActionsTestModel()
	out, _, _ := m.handlePinTurnSlash(99, true)
	gm := out.(Model)
	if !strings.Contains(gm.notice, "No assistant turn #99") {
		t.Fatalf("expected notice about missing turn, got %q", gm.notice)
	}
	if len(gm.chat.pinnedAssistantTurns) != 0 {
		t.Fatalf("missing turn should not pin anything, got %#v", gm.chat.pinnedAssistantTurns)
	}
}

// TestHandleSaveTurnSlashWritesSingleTurnExport — /save <n> drops a
// markdown file under .dfmc/exports/turn-<n>-<stamp>.md containing the
// matched user prompt + the assistant body. Round-trips through the
// real file system in a t.TempDir() so we exercise the path joiner /
// MkdirAll / write code paths.
func TestHandleSaveTurnSlashWritesSingleTurnExport(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp: %v", err)
	}

	m := newTurnActionsTestModel()
	out, _, _ := m.handleSaveTurnSlash(2)
	gm := out.(Model)
	if !strings.Contains(gm.notice, "Saved turn #2") {
		t.Fatalf("expected save notice, got %q", gm.notice)
	}

	// Locate the file (stamp is dynamic).
	matches, _ := filepath.Glob(filepath.Join(dir, ".dfmc", "exports", "turn-2-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected one turn-2 export, found: %#v", matches)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	bodyStr := string(body)
	for _, want := range []string{
		"# DFMC turn #2",
		"## user",
		"what about refresh?",
		"## assistant (turn #2)",
		"refresh tokens are exchanged at /auth/refresh.",
	} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("export missing %q, got:\n%s", want, bodyStr)
		}
	}
}

func TestHandleSaveTurnSlashRejectsMissingTurn(t *testing.T) {
	m := newTurnActionsTestModel()
	out, _, _ := m.handleSaveTurnSlash(99)
	gm := out.(Model)
	if !strings.Contains(gm.notice, "No assistant turn #99") {
		t.Fatalf("expected missing-turn notice, got %q", gm.notice)
	}
}

// TestHandleForkTurnSlashRejectsMissingTurn pins the surface that the
// dispatcher relies on: an out-of-range /fork hops straight to the
// helpful notice rather than pretending to call the engine. The engine
// path itself is exercised by the conversation_test.go suite.
func TestHandleForkTurnSlashRejectsMissingTurn(t *testing.T) {
	m := newTurnActionsTestModel()
	out, _, _ := m.handleForkTurnSlash(99, "")
	gm := out.(Model)
	if !strings.Contains(gm.notice, "No assistant turn #99") {
		t.Fatalf("expected missing-turn notice, got %q", gm.notice)
	}
}
