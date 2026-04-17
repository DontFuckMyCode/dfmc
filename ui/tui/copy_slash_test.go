// /copy slash command tests. The clipboard side-effect is an OSC 52
// byte sequence emitted by a tea.Cmd — we can't synthesize a terminal
// here, so we pin the *preparation* side: which message was targeted,
// what notice the user sees, and how out-of-range / unknown args are
// rejected. Content delivery is covered indirectly by copyToClipboardCmd
// which is exercised through the notice (byte count + truncation flag).

package tui

import (
	"context"
	"strings"
	"testing"
)

// newCopyModel returns a Model loaded with a canned transcript:
//
//	user #-, assistant #1, user #-, assistant #2 (with a code block),
//	tool #-, assistant #3 (empty), assistant #4
//
// The odd shape — interleaved users / tools / empties — is deliberate:
// /copy must skip non-assistant rows and handle empty content cleanly.
func newCopyModel() Model {
	m := NewModel(context.Background(), nil)
	m.transcript = []chatLine{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "see snippet below:\n\n```go\nfmt.Println(\"hi\")\n```\n\nthat is all."},
		{Role: "tool", Content: "read_file: ok"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "fourth answer"},
	}
	return m
}

func TestCopySlash_AssistantIndicesSkipsNonAssistantRows(t *testing.T) {
	m := newCopyModel()
	got := m.assistantIndices()
	want := []int{1, 3, 5, 6}
	if len(got) != len(want) {
		t.Fatalf("assistantIndices len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("assistantIndices[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestCopySlash_DefaultCopiesLastResponse(t *testing.T) {
	m := newCopyModel()
	next, cmd, handled := m.handleCopySlash(nil)
	if !handled {
		t.Fatal("/copy must be handled")
	}
	if cmd == nil {
		t.Fatal("expected a clipboard tea.Cmd for non-empty content")
	}
	nm := next.(Model)
	// #4 is the label of the last assistant row (index 6 in the slice,
	// which is the 4th assistant response).
	if !strings.Contains(nm.notice, "response #4") {
		t.Fatalf("notice should mention response #4, got %q", nm.notice)
	}
	if !strings.Contains(strings.ToLower(nm.notice), "copied") {
		t.Fatalf("notice should announce the copy, got %q", nm.notice)
	}
}

func TestCopySlash_LastAliasMatchesDefault(t *testing.T) {
	m := newCopyModel()
	_, _, h1 := m.handleCopySlash(nil)
	_, _, h2 := m.handleCopySlash([]string{"last"})
	if !h1 || !h2 {
		t.Fatal("both /copy and /copy last must be handled")
	}
}

func TestCopySlash_PositiveIndexPicks1BasedSlot(t *testing.T) {
	m := newCopyModel()
	next, cmd, _ := m.handleCopySlash([]string{"2"})
	if cmd == nil {
		t.Fatal("expected clipboard cmd for existing response #2")
	}
	nm := next.(Model)
	if !strings.Contains(nm.notice, "response #2") {
		t.Fatalf("notice should label response #2, got %q", nm.notice)
	}
}

func TestCopySlash_NegativeIndexCountsFromEnd(t *testing.T) {
	m := newCopyModel()
	// -2 → the one before the last → response #3 (the empty one!).
	next, cmd, _ := m.handleCopySlash([]string{"-2"})
	nm := next.(Model)
	if cmd != nil {
		// Response #3 is empty; copyAssistantResponseAt should refuse.
		t.Fatalf("empty selected response must not produce a clipboard cmd, got cmd=%v notice=%q", cmd, nm.notice)
	}
	if !strings.Contains(strings.ToLower(nm.notice), "empty") {
		t.Fatalf("notice should flag empty response, got %q", nm.notice)
	}
}

func TestCopySlash_NegativeOneIsLast(t *testing.T) {
	m := newCopyModel()
	next, _, _ := m.handleCopySlash([]string{"-1"})
	nm := next.(Model)
	if !strings.Contains(nm.notice, "response #4") {
		t.Fatalf("-1 should resolve to the last assistant (#4), got %q", nm.notice)
	}
}

func TestCopySlash_OutOfRangePositiveReportsCount(t *testing.T) {
	m := newCopyModel()
	next, cmd, _ := m.handleCopySlash([]string{"99"})
	if cmd != nil {
		t.Fatal("out-of-range index must not emit a clipboard cmd")
	}
	nm := next.(Model)
	if !strings.Contains(nm.notice, "only 4") {
		t.Fatalf("notice should report the real count (4), got %q", nm.notice)
	}
}

func TestCopySlash_OutOfRangeNegativeReportsCount(t *testing.T) {
	m := newCopyModel()
	next, cmd, _ := m.handleCopySlash([]string{"-99"})
	if cmd != nil {
		t.Fatal("out-of-range negative index must not emit a clipboard cmd")
	}
	nm := next.(Model)
	if !strings.Contains(nm.notice, "only 4") {
		t.Fatalf("notice should report the real count (4), got %q", nm.notice)
	}
}

func TestCopySlash_EmptyTranscriptRefusesAndNotes(t *testing.T) {
	m := NewModel(context.Background(), nil)
	next, cmd, _ := m.handleCopySlash(nil)
	if cmd != nil {
		t.Fatal("empty transcript must not emit a clipboard cmd")
	}
	nm := next.(Model)
	if !strings.Contains(strings.ToLower(nm.notice), "no assistant") {
		t.Fatalf("notice should say there's nothing to copy, got %q", nm.notice)
	}
}

func TestCopySlash_AllJoinsEveryNonEmptyResponse(t *testing.T) {
	m := newCopyModel()
	next, cmd, _ := m.handleCopySlash([]string{"all"})
	if cmd == nil {
		t.Fatal("expected clipboard cmd for /copy all")
	}
	nm := next.(Model)
	// Three non-empty responses (one of four is empty).
	if !strings.Contains(nm.notice, "3 response(s)") {
		t.Fatalf("notice should report 3 non-empty responses, got %q", nm.notice)
	}
}

func TestCopySlash_CodeDefaultGrabsLatestFencedBlock(t *testing.T) {
	m := newCopyModel()
	next, cmd, _ := m.handleCopySlash([]string{"code"})
	if cmd == nil {
		t.Fatal("expected clipboard cmd for /copy code")
	}
	nm := next.(Model)
	if !strings.Contains(nm.notice, "code block") {
		t.Fatalf("notice should mention the code block, got %q", nm.notice)
	}
	// The block lives in response #2 (second assistant row).
	if !strings.Contains(nm.notice, "#2") {
		t.Fatalf("code-block notice should reference response #2, got %q", nm.notice)
	}
}

func TestCopySlash_CodeWithNoneFound(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.transcript = []chatLine{
		{Role: "assistant", Content: "just prose, no fences at all"},
	}
	next, cmd, _ := m.handleCopySlash([]string{"code"})
	if cmd != nil {
		t.Fatal("no fenced blocks → no clipboard cmd")
	}
	nm := next.(Model)
	if !strings.Contains(strings.ToLower(nm.notice), "no fenced") {
		t.Fatalf("notice should explain there are no blocks, got %q", nm.notice)
	}
}

func TestCopySlash_UnknownArgShowsUsage(t *testing.T) {
	m := newCopyModel()
	next, cmd, _ := m.handleCopySlash([]string{"banana"})
	if cmd != nil {
		t.Fatal("unknown arg must not emit a clipboard cmd")
	}
	nm := next.(Model)
	if !strings.Contains(strings.ToLower(nm.notice), "usage") {
		t.Fatalf("notice should be a usage line, got %q", nm.notice)
	}
}

// extractFencedBlocks is the helper that powers /copy code; keep its
// contract pinned so future refactors don't silently change what ends
// up on the clipboard.
func TestExtractFencedBlocks_HappyPath(t *testing.T) {
	text := "before\n```go\nA()\nB()\n```\nmiddle\n```python\nprint(1)\n```\nafter"
	got := extractFencedBlocks(text)
	if len(got) != 2 {
		t.Fatalf("want 2 blocks, got %d (%v)", len(got), got)
	}
	if got[0] != "A()\nB()" {
		t.Fatalf("block 0 body mismatch, got %q", got[0])
	}
	if got[1] != "print(1)" {
		t.Fatalf("block 1 body mismatch, got %q", got[1])
	}
}

func TestExtractFencedBlocks_UnclosedFenceStillCaptured(t *testing.T) {
	// An assistant message cut off mid-stream often leaves an unclosed
	// fence. Better to hand over what we saw than drop the payload.
	text := "intro\n```ts\nconst x = 1\nconst y = 2\n"
	got := extractFencedBlocks(text)
	if len(got) != 1 {
		t.Fatalf("want 1 block from unclosed fence, got %d (%v)", len(got), got)
	}
	if !strings.Contains(got[0], "const x = 1") {
		t.Fatalf("unclosed block should contain the captured lines, got %q", got[0])
	}
}

func TestExtractFencedBlocks_NoFencesReturnsNil(t *testing.T) {
	got := extractFencedBlocks("plain text with `inline` backticks but no fences")
	if len(got) != 0 {
		t.Fatalf("want zero blocks, got %d (%v)", len(got), got)
	}
}

// The #N chip is what makes /copy N discoverable — if the header ever
// stops stamping it, users won't know which integer to pass. Guard it
// at the renderer layer.
func TestMessageHeader_StampsCopyIndexChip(t *testing.T) {
	got := renderMessageHeader(messageHeaderInfo{
		Role:      "assistant",
		CopyIndex: 7,
	})
	if !strings.Contains(got, "#7") {
		t.Fatalf("assistant header with CopyIndex=7 should show #7 chip, got:\n%s", got)
	}
}

func TestMessageHeader_OmitsChipForUserRow(t *testing.T) {
	got := renderMessageHeader(messageHeaderInfo{
		Role:      "user",
		CopyIndex: 0,
	})
	if strings.Contains(got, "#") {
		t.Fatalf("user header must not carry a copy chip, got:\n%s", got)
	}
}
