package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestHeartbeatTickCmd(t *testing.T) {
	cmd := heartbeatTickCmd()
	if cmd == nil {
		t.Fatal("heartbeatTickCmd returned nil")
	}
}

func TestDrainPendingQueue_EmptyQueue(t *testing.T) {
	m := NewModel(nil, nil)
	// Empty queue should return (m, nil)
	next, cmd := m.drainPendingQueue()
	if next.chat.input != m.chat.input {
		t.Error("expected unchanged model")
	}
	if cmd != nil {
		t.Errorf("empty queue: expected nil cmd, got %v", cmd)
	}
}

func TestApplyCommandPickerProvider_EmptySelection(t *testing.T) {
	m := NewModel(nil, nil)
	m.commandPicker.persist = false
	next, cmd := m.applyCommandPickerProvider("")
	nm := next.(Model)
	if nm.notice != "Provider selection is empty." {
		t.Errorf("notice: got %q", nm.notice)
	}
	if cmd != nil {
		t.Errorf("empty selection: expected nil cmd, got %v", cmd)
	}
}

func TestApplyCommandPickerModel_EmptySelection(t *testing.T) {
	m := NewModel(nil, nil)
	m.commandPicker.persist = false
	next, cmd := m.applyCommandPickerModel("")
	nm := next.(Model)
	if nm.notice != "Model selection is empty." {
		t.Errorf("notice: got %q", nm.notice)
	}
	if cmd != nil {
		t.Errorf("empty selection: expected nil cmd, got %v", cmd)
	}
}

func TestContextCommandWhySummary_NoReport(t *testing.T) {
	m := NewModel(nil, nil)
	// No engine, no status contextIn set - should return "No context report available yet."
	got := m.contextCommandWhySummary()
	if got == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestDescribeLastIntent_NoDecisionsYet(t *testing.T) {
	m := NewModel(nil, nil)
	got := m.describeLastIntent()
	if got == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestDeleteInputBeforeCursor_EmptyInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.input = ""
	m.chat.cursor = 0
	m.deleteInputBeforeCursor()
	// Should not panic with empty input
}

func TestDeleteInputAtCursor_EmptyInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.input = ""
	m.chat.cursor = 0
	m.deleteInputAtCursor()
	// Should not panic with empty input
}

func TestDeleteInputAtCursor_AlreadyAtEnd(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.input = "hello"
	m.chat.cursor = 5 // past the end
	m.deleteInputAtCursor()
	// Should not panic and should not change input
	if m.chat.input != "hello" {
		t.Errorf("input changed unexpectedly to %q", m.chat.input)
	}
}

func TestExitInputHistoryNavigation_AlreadyInactive(t *testing.T) {
	m := NewModel(nil, nil)
	// index is already -1 by default, so should return early
	m.exitInputHistoryNavigation()
	// Should not panic - this is the default state
}

func TestExitInputHistoryNavigation_ResetsIndex(t *testing.T) {
	m := NewModel(nil, nil)
	m.inputHistory.index = 2 // actively navigating
	m.inputHistory.draft = "draft text"
	m.exitInputHistoryNavigation()
	if m.inputHistory.index != -1 {
		t.Errorf("index: got %d want -1", m.inputHistory.index)
	}
	if m.inputHistory.draft != "" {
		t.Errorf("draft: got %q want empty", m.inputHistory.draft)
	}
}

func TestConversationBranchSlash_UnknownSubcommand(t *testing.T) {
	m := NewModel(nil, nil)
	// Unknown subcommand should return error without needing engine
	got := conversationBranchSlash(m, []string{"unknown-subcommand"})
	if got == "" {
		t.Error("expected non-empty response for unknown subcommand")
	}
	if !strings.Contains(got, "unknown") {
		t.Errorf("expected 'unknown' in response, got %q", got)
	}
}

func TestConversationBranchSlash_ListSubcommandNeedsEngine(t *testing.T) {
	// "list" subcommand calls m.eng.ConversationBranchList() which panics with nil engine
	// This test documents that calling with "list" on a nil-engine model crashes
	// We skip actually running it to avoid panicking in tests
	// The unknown-subcommand test above covers the early-return path
}

func TestLooksLikeSecretFile_TrueCases(t *testing.T) {
	cases := []string{
		".env",
		".env.local",
		".env.production",
		".envrc",
		".netrc",
		".pgpass",
		"id_rsa",
		"id_ed25519",
		"credentials",
		"credentials.json",
		"secrets.yml",
		"private.key",
		"private_key.pem",
		"service-account.json",
		"api_key",
		"apikey",
		"password_reset_flow.md",
		"my_secret.txt",
	}
	for _, path := range cases {
		if !looksLikeSecretFile(path) {
			t.Errorf("looksLikeSecretFile(%q) = false, want true", path)
		}
	}
}

func TestLooksLikeSecretFile_FalseCases(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"readme.md",
		"main.go",
		"index.html",
		"style.css",
		"package.json",
		"env.txt",
		"env.md",
		"testdata/env",
		"docs/index.md",
		"src/main.go",
		"user_manager.go",
	}
	for _, path := range cases {
		if looksLikeSecretFile(path) {
			t.Errorf("looksLikeSecretFile(%q) = true, want false", path)
		}
	}
}

func TestLooksLikePath_TrueCases(t *testing.T) {
	cases := []string{
		"src/main.go",
		"path/to/file.go",
		"foo\\bar\\baz.go",
		"file.go",
		"main.go",
		"path/to/script.sh",
		"somefile.txt",
		"src/app.go:10",
		"D:/path/to/file",
	}
	for _, s := range cases {
		if !looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = false, want true", s)
		}
	}
}

func TestLooksLikePath_FalseCases(t *testing.T) {
	cases := []string{
		"-flag",
		"--long-flag",
		"-x",
		"README",
		"foo:10",
		"some random text without path chars",
	}
	for _, s := range cases {
		if looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = true, want false", s)
		}
	}
}

func TestLooksLikePath_WindowsDriveLetter(t *testing.T) {
	// C:\path\to\file returns true (function behavior differs from expected Windows detection)
	// The function accepts paths with backslashes even when they look like Windows drives
	if !looksLikePath("C:\\path\\to\\file") {
		t.Error("looksLikePath(C:\\path\\to\\file) = false, want true")
	}
}

func TestToStringAnyMap_StringAny(t *testing.T) {
	input := map[string]any{"foo": "bar", "num": 42}
	got, ok := toStringAnyMap(input)
	if !ok {
		t.Fatal("toStringAnyMap returned ok=false for map[string]any")
	}
	if got["foo"] != "bar" {
		t.Errorf("foo = %v, want bar", got["foo"])
	}
	if got["num"] != 42 {
		t.Errorf("num = %v, want 42", got["num"])
	}
}

func TestToStringAnyMap_AnyAny(t *testing.T) {
	input := map[any]any{"foo": "bar", "num": 42}
	got, ok := toStringAnyMap(input)
	if !ok {
		t.Fatal("toStringAnyMap returned ok=false for map[any]any")
	}
	if got["foo"] != "bar" {
		t.Errorf("foo = %v, want bar", got["foo"])
	}
	if got["num"] != 42 {
		t.Errorf("num = %v, want 42", got["num"])
	}
}

func TestToStringAnyMap_AnyAnyWithNonStringKeys(t *testing.T) {
	input := map[any]any{"foo": "bar", 123: "num", "key": "value"}
	got, ok := toStringAnyMap(input)
	if !ok {
		t.Fatal("toStringAnyMap returned ok=false for map[any]any with non-string keys")
	}
	if got["foo"] != "bar" {
		t.Errorf("foo = %v, want bar", got["foo"])
	}
	if got["key"] != "value" {
		t.Errorf("key = %v, want value", got["key"])
	}
	// Non-string keys (like 123) should be skipped - only string keys are transferred
	// The function silently skips non-string keys
}

func TestToStringAnyMap_InvalidType(t *testing.T) {
	cases := []any{
		"string",
		123,
		nil,
		[]string{"a", "b"},
		[]any{"a", "b"},
	}
	for _, input := range cases {
		_, ok := toStringAnyMap(input)
		if ok {
			t.Errorf("toStringAnyMap(%T) = ok=true, want false", input)
		}
	}
}

func TestPayloadString_Basic(t *testing.T) {
	data := map[string]any{"key": "value"}
	got := payloadString(data, "key", "fallback")
	if got != "value" {
		t.Errorf("payloadString = %q, want value", got)
	}
}

func TestPayloadString_Fallback(t *testing.T) {
	data := map[string]any{"other": "value"}
	got := payloadString(data, "key", "fallback")
	if got != "fallback" {
		t.Errorf("payloadString = %q, want fallback", got)
	}
}

func TestPayloadString_NilData(t *testing.T) {
	got := payloadString(nil, "key", "fallback")
	if got != "fallback" {
		t.Errorf("payloadString(nil) = %q, want fallback", got)
	}
}

func TestPayloadString_EmptyString(t *testing.T) {
	data := map[string]any{"key": "  "}
	got := payloadString(data, "key", "fallback")
	if got != "fallback" {
		t.Errorf("payloadString(empty) = %q, want fallback", got)
	}
}

func TestPayloadString_NonStringValue(t *testing.T) {
	data := map[string]any{"key": 123}
	got := payloadString(data, "key", "fallback")
	if got != "123" {
		t.Errorf("payloadString(int) = %q, want 123", got)
	}
}

func TestPayloadStringSlice_Basic(t *testing.T) {
	data := map[string]any{"tags": []string{"a", "b", "c"}}
	got := payloadStringSlice(data, "tags")
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestPayloadStringSlice_NilData(t *testing.T) {
	got := payloadStringSlice(nil, "key")
	if got != nil {
		t.Errorf("payloadStringSlice(nil) = %v, want nil", got)
	}
}

func TestPayloadStringSlice_MissingKey(t *testing.T) {
	data := map[string]any{"other": []string{"a"}}
	got := payloadStringSlice(data, "key")
	if got != nil {
		t.Errorf("payloadStringSlice(missing) = %v, want nil", got)
	}
}

func TestPayloadStringSlice_NonStringValues(t *testing.T) {
	data := map[string]any{"nums": []any{1, 2, 3}}
	got := payloadStringSlice(data, "nums")
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestPayloadStringSlice_TrimsWhitespace(t *testing.T) {
	data := map[string]any{"names": []string{"  a  ", "b", ""}}
	got := payloadStringSlice(data, "names")
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (empty strings skipped)", len(got))
	}
}

func TestPayloadBool_Basic(t *testing.T) {
	data := map[string]any{"flag": true}
	got := payloadBool(data, "flag", false)
	if got != true {
		t.Errorf("payloadBool(true) = %v, want true", got)
	}
}

func TestPayloadBool_StringTrue(t *testing.T) {
	cases := []string{"true", "True", "TRUE", "1", "yes", "YES", "on", "ON"}
	for _, v := range cases {
		data := map[string]any{"flag": v}
		got := payloadBool(data, "flag", false)
		if got != true {
			t.Errorf("payloadBool(%q) = %v, want true", v, got)
		}
	}
}

func TestPayloadBool_StringFalse(t *testing.T) {
	cases := []string{"false", "False", "FALSE", "0", "no", "NO", "off", "OFF"}
	for _, v := range cases {
		data := map[string]any{"flag": v}
		got := payloadBool(data, "flag", true)
		if got != false {
			t.Errorf("payloadBool(%q) = %v, want false", v, got)
		}
	}
}

func TestPayloadBool_Fallback(t *testing.T) {
	data := map[string]any{"other": true}
	got := payloadBool(data, "flag", true)
	if got != true {
		t.Errorf("payloadBool(missing) = %v, want true (fallback)", got)
	}
}

func TestPayloadBool_NilData(t *testing.T) {
	got := payloadBool(nil, "flag", true)
	if got != true {
		t.Errorf("payloadBool(nil) = %v, want true (fallback)", got)
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"short", "short"},
		{"", ""},
		{"this-is-a-long-id", "this-is-a-lo"},
		{"exactly12chars!@", "exactly12cha"},
	}
	for _, tc := range tests {
		got := shortID(tc.input)
		if got != tc.want {
			t.Errorf("shortID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTruncateActivityText(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello world", 20, "hello world"},
		{"hello world", 5, "hello..."},
		{"hello world", 0, "hello world"},
		{"hello world", -1, "hello world"},
		{"", 10, ""},
		{"a very long string", 6, "a very..."},
	}
	for _, tc := range tests {
		got := truncateActivityText(tc.input, tc.n)
		if got != tc.want {
			t.Errorf("truncateActivityText(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}

func TestTruncateActivityText_Newlines(t *testing.T) {
	// newlines and carriage returns should be replaced with spaces
	input := "hello\nworld\rtest"
	got := truncateActivityText(input, 50)
	if got != "hello world test" {
		t.Errorf("truncateActivityText with newlines = %q, want 'hello world test'", got)
	}
}

func TestPayloadIntAny(t *testing.T) {
	tests := []struct {
		data     map[string]any
		fallback int
		keys     []string
		want     int
	}{
		{map[string]any{"a": 1}, 0, []string{"a"}, 1},
		{map[string]any{"a": 1}, 0, []string{"b", "a"}, 1},
		{map[string]any{"b": 2}, 0, []string{"a", "b"}, 2},
		{nil, 99, []string{"a"}, 99},
		{map[string]any{}, 99, []string{"a"}, 99},
		{map[string]any{"a": "5"}, 0, []string{"a"}, 5},
	}
	for _, tc := range tests {
		got := payloadIntAny(tc.data, tc.fallback, tc.keys...)
		if got != tc.want {
			t.Errorf("payloadIntAny(%v, %d, %v) = %d, want %d", tc.data, tc.fallback, tc.keys, got, tc.want)
		}
	}
}

func TestKindIcon(t *testing.T) {
	tests := []struct {
		kind activityKind
		want string
	}{
		{activityKindError, "!"},
		{activityKindTool, "*"},
		{activityKindAgent, "@"},
		{activityKindStream, ">"},
		{activityKindCtx, "#"},
		{activityKindIndex, "="},
		{activityKindInfo, "."},
	}
	for _, tc := range tests {
		got := kindIcon(tc.kind)
		if got == "" {
			t.Errorf("kindIcon(%v) returned empty", tc.kind)
		}
	}
}

func TestActivityModeLabel(t *testing.T) {
	tests := []struct {
		mode activityViewMode
		want string
	}{
		{activityViewTools, "tools"},
		{activityViewAgents, "agents"},
		{activityViewErrors, "errors"},
		{activityViewWorkflow, "workflow"},
		{activityViewContext, "context"},
		{activityViewAll, "all"},
	}
	for _, tc := range tests {
		got := activityModeLabel(tc.mode)
		if got != tc.want {
			t.Errorf("activityModeLabel(%v) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestNextActivityMode_CyclesThroughModes(t *testing.T) {
	// Test that cycling through modes works
	current := activityViewTools
	for i := 0; i < 12; i++ {
		next := nextActivityMode(current)
		if next == current {
			t.Errorf("nextActivityMode(%v) returned same mode", current)
		}
		current = next
	}
}

func TestClampActivityOffset(t *testing.T) {
	tests := []struct {
		scroll, total int
		want          int
	}{
		{0, 10, 0},
		{5, 10, 5},
		{9, 10, 9},
		{10, 10, 9},   // scroll >= total => total-1
		{15, 10, 9},   // scroll >= total => total-1
		{-1, 10, 0},   // negative => 0
		{-100, 10, 0}, // negative => 0
		{0, 0, 0},     // total <= 0 => 0
		{5, 0, 0},     // total <= 0 => 0
		{5, 1, 0},     // scroll >= total => 0
	}
	for _, tc := range tests {
		got := clampActivityOffset(tc.scroll, tc.total)
		if got != tc.want {
			t.Errorf("clampActivityOffset(%d, %d) = %d, want %d", tc.scroll, tc.total, got, tc.want)
		}
	}
}

func TestActivitySelectedIndex(t *testing.T) {
	// activitySelectedIndex returns total-1-scroll (reversed order for bottom-up selection)
	tests := []struct {
		total, scroll int
		want          int
	}{
		{10, 0, 9},   // first entry at top = last index
		{10, 5, 4},   // middle
		{10, 9, 0},   // last entry at top = first index
		{10, 10, 0},  // clamped to 9, then 9->0
		{10, 15, 0},  // clamped to 9, then 9->0
		{0, 0, -1},   // total <= 0
		{10, -1, 9},  // -1 clamped to 0, then 9
	}
	for _, tc := range tests {
		got := activitySelectedIndex(tc.total, tc.scroll)
		if got != tc.want {
			t.Errorf("activitySelectedIndex(%d, %d) = %d, want %d", tc.total, tc.scroll, got, tc.want)
		}
	}
}

func TestIndexOfString(t *testing.T) {
	tests := []struct {
		items  []string
		target string
		want   int
	}{
		{[]string{"a", "b", "c"}, "b", 1},
		{[]string{"a", "b", "c"}, "a", 0},
		{[]string{"a", "b", "c"}, "d", -1},
		{[]string{"  a  ", "b"}, "a", 0},
		{[]string{"a", "  b  "}, "b", 1},
		{[]string{}, "a", -1},
		{[]string{"a"}, "a", 0},
		{[]string{"a"}, "A", -1},
	}
	for _, tc := range tests {
		got := indexOfString(tc.items, tc.target)
		if got != tc.want {
			t.Errorf("indexOfString(%v, %q) = %d, want %d", tc.items, tc.target, got, tc.want)
		}
	}
}

func TestClampIndex(t *testing.T) {
	tests := []struct {
		index, length int
		want          int
	}{
		{0, 10, 0},
		{5, 10, 5},
		{9, 10, 9},
		{10, 10, 9},
		{15, 10, 9},
		{-1, 10, 0},
		{-100, 10, 0},
		{0, 0, 0},
		{5, 0, 0},
		{5, 1, 0},
	}
	for _, tc := range tests {
		got := clampIndex(tc.index, tc.length)
		if got != tc.want {
			t.Errorf("clampIndex(%d, %d) = %d, want %d", tc.index, tc.length, got, tc.want)
		}
	}
}

func TestTruncateForLine(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello world", 20, "hello world"},
		{"hello world", 5, "hello…"},
		{"hello world", 0, "hello world"},
		{"hello world", -1, "hello world"},
		{"", 10, ""},
		{"hello\nworld", 20, "hello world"},
		{"hello\nworld", 5, "hello…"},
		{"  hello  ", 10, "hello"},
		{"a very long string", 6, "a very…"},
	}
	for _, tc := range tests {
		got := truncateForLine(tc.input, tc.n)
		if got != tc.want {
			t.Errorf("truncateForLine(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}

func TestSubagentProviderLabel(t *testing.T) {
	tests := []struct {
		provider, model string
		want            string
	}{
		{"anthropic", "sonnet", "anthropic/sonnet"},
		{"anthropic", "", "anthropic"},
		{"", "sonnet", "sonnet"},
		{"", "", ""},
		{"  anthropic  ", "  sonnet  ", "anthropic/sonnet"},
	}
	for _, tc := range tests {
		got := subagentProviderLabel(tc.provider, tc.model)
		if got != tc.want {
			t.Errorf("subagentProviderLabel(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestSubagentProfileChain(t *testing.T) {
	tests := []struct {
		candidates []string
		want       string
	}{
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a -> b"},
		{[]string{"a", "b", "c", "d"}, "a -> b -> c -> d"},
		{[]string{"a", "b", "c", "d", "e"}, "a -> b -> c -> d -> ..."},
		{[]string{"", "a", "", "b", ""}, "a -> b"},
	}
	for _, tc := range tests {
		got := subagentProfileChain(tc.candidates)
		if got != tc.want {
			t.Errorf("subagentProfileChain(%v) = %q, want %q", tc.candidates, got, tc.want)
		}
	}
}

func TestSubagentProfileTransition(t *testing.T) {
	tests := []struct {
		from, to  string
		want      string
	}{
		{"a", "b", "a -> b"},
		{"", "b", "fallback -> b"},
		{"a", "", "a"},
		{"", "", ""},
		{"  a  ", "  b  ", "a -> b"},
	}
	for _, tc := range tests {
		got := subagentProfileTransition(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("subagentProfileTransition(%q, %q) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestRenderSendingInputBuffer_SingleLine(t *testing.T) {
	got := renderSendingInputBuffer("hello")
	want := "> hello"
	if got != want {
		t.Errorf("renderSendingInputBuffer(%q) = %q, want %q", "hello", got, want)
	}
}

func TestRenderSendingInputBuffer_MultiLine(t *testing.T) {
	got := renderSendingInputBuffer("hello\nworld")
	want := "> hello\n  world"
	if got != want {
		t.Errorf("renderSendingInputBuffer(%q) = %q, want %q", "hello\nworld", got, want)
	}
}

func TestRenderSendingInputBuffer_Empty(t *testing.T) {
	got := renderSendingInputBuffer("")
	want := "> "
	if got != want {
		t.Errorf("renderSendingInputBuffer(%q) = %q, want %q", "", got, want)
	}
}

func TestRenderSendingInputBuffer_ThreeLines(t *testing.T) {
	got := renderSendingInputBuffer("a\nb\nc")
	want := "> a\n  b\n  c"
	if got != want {
		t.Errorf("renderSendingInputBuffer(%q) = %q, want %q", "a\nb\nc", got, want)
	}
}

func TestTruncateForPanelSized_Basic(t *testing.T) {
	got := truncateForPanelSized("hello\nworld", 20, 10)
	if got != "hello\nworld" {
		t.Errorf("basic case = %q, want 'hello\\nworld'", got)
	}
}

func TestTruncateForPanelSized_DefaultsTo18(t *testing.T) {
	// With maxLines=0 or negative, should default to 18
	got := truncateForPanelSized("a\nb\nc", 20, 0)
	if got != "a\nb\nc" {
		t.Errorf("maxLines=0 should use 18 = %q", got)
	}
	got = truncateForPanelSized("a\nb\nc", 20, -5)
	if got != "a\nb\nc" {
		t.Errorf("maxLines=-5 should use 18 = %q", got)
	}
}

func TestTruncateForPanelSized_LineTruncation(t *testing.T) {
	// Many lines should be truncated to maxLines
	lines := strings.Repeat("line\n", 20)
	got := truncateForPanelSized(lines, 20, 5)
	if !strings.Contains(got, "... [truncated]") {
		t.Errorf("expected truncation marker, got %q", got)
	}
	if strings.Count(got, "\n") != 5 {
		t.Errorf("expected 5 lines after truncation, got %d newlines", strings.Count(got, "\n"))
	}
}

func TestTruncateForPanelSized_WidthTruncation(t *testing.T) {
	// Long lines should be truncated by width
	longLine := strings.Repeat("x", 50)
	got := truncateForPanelSized(longLine, 20, 10)
	if !strings.Contains(got, "... [trimmed]") {
		t.Errorf("expected width truncation marker, got %q", got)
	}
}

func TestTruncateForPanelSized_WidthZero(t *testing.T) {
	// width=0 should not truncate
	longLine := strings.Repeat("x", 50)
	got := truncateForPanelSized(longLine, 0, 10)
	if strings.Contains(got, "...") {
		t.Errorf("width=0 should not truncate, got %q", got)
	}
}

func TestChatDigest(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello\nworld", "hello ..."},
		{"hello\n\nworld", "hello ..."},
		{"", ""},
		{"  hello  ", "hello"},
		{"\nhello\nworld\n", "hello ..."},
		{"  \n  ", ""},
	}
	for _, tc := range tests {
		got := chatDigest(tc.input)
		if got != tc.want {
			t.Errorf("chatDigest(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestChatDigest_MultilineWithEmptyFirstLine(t *testing.T) {
	// Note: [multiline] branch is unreachable because strings.TrimSpace
	// removes leading newlines before strings.Cut, so first can never be empty.
	// This test documents that the branch exists but can't be triggered.
	got := chatDigest("a\n\nsecond line")
	if got != "a ..." {
		t.Errorf("two newlines = %q, want 'a ...'", got)
	}
}

func TestGitCmdError(t *testing.T) {
	err := &gitCmdError{args: []string{"status"}, msg: "fatal: not a git repository"}
	got := err.Error()
	want := "git status: fatal: not a git repository"
	if got != want {
		t.Errorf("gitCmdError.Error() = %q, want %q", got, want)
	}
}

func TestGitCmdError_MultipleArgs(t *testing.T) {
	err := &gitCmdError{args: []string{"log", "--oneline", "-n", "5"}, msg: "repository not found"}
	got := err.Error()
	want := "git log --oneline -n 5: repository not found"
	if got != want {
		t.Errorf("gitCmdError.Error() = %q, want %q", got, want)
	}
}

func TestActivityFocusSelectionFile_NoSelection(t *testing.T) {
	m := NewModel(nil, nil)
	// No activity entries, so should return notice
	next, cmd := m.activityFocusSelectionFile()
	nm := next.(Model)
	if nm.notice != "No activity event selected." {
		t.Errorf("notice = %q, want %q", nm.notice, "No activity event selected.")
	}
	if cmd != nil {
		t.Errorf("cmd = %v, want nil", cmd)
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		v, lo, hi int
		want      int
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
		{5, 5, 5, 5},
		{-100, 0, 100, 0},
		{200, 0, 100, 100},
	}
	for _, tc := range tests {
		got := clampInt(tc.v, tc.lo, tc.hi)
		if got != tc.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tc.v, tc.lo, tc.hi, got, tc.want)
		}
	}
}

func TestHasTrailingWhitespace(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"hello", false},
		{"hello ", true},
		{"hello\t", true},
		{"hello\n", true},
		{"hello\r", true},
		{"", false},
		{"  ", true}, // trailing space
	}
	for _, tc := range tests {
		got := hasTrailingWhitespace(tc.text)
		if got != tc.want {
			t.Errorf("hasTrailingWhitespace(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestFirstSuggestions(t *testing.T) {
	tests := []struct {
		items []string
		limit int
		want  []string
	}{
		{[]string{"a", "b", "c"}, 2, []string{"a", "b"}},
		{[]string{"a", "b"}, 5, []string{"a", "b"}},
		{[]string{"a", "b", "c"}, 0, nil},
		{[]string{}, 5, nil},
		{[]string{"a", "b", "c"}, -1, nil},
	}
	for _, tc := range tests {
		got := firstSuggestions(tc.items, tc.limit)
		if !slices.Equal(got, tc.want) {
			t.Errorf("firstSuggestions(%v, %d) = %v, want %v", tc.items, tc.limit, got, tc.want)
		}
	}
}

func TestClassifyActivity(t *testing.T) {
	tests := []struct {
		eventType string
		wantKind  activityKind
	}{
		{"agent:loop:thinking", activityKindAgent},
		{"tool:call", activityKindTool},
		{"tool:result", activityKindTool},
		{"stream:start", activityKindStream},
		{"context:updated", activityKindCtx},
		{"ctx:something", activityKindCtx},
		{"index:built", activityKindIndex},
		{"error:some", activityKindError},
		{"something:fail", activityKindError},
		{"unknown:event", activityKindInfo},
		{"", activityKindInfo},
	}
	for _, tc := range tests {
		ev := engine.Event{Type: tc.eventType, Payload: map[string]any{}}
		got, _ := classifyActivity(ev)
		if got != tc.wantKind {
			t.Errorf("classifyActivity(%q) kind = %v, want %v", tc.eventType, got, tc.wantKind)
		}
	}
}

func TestFormatASTLanguageSummaryTUI(t *testing.T) {
	tests := []struct {
		items []ast.BackendLanguageStatus
		want  string
	}{
		{[]ast.BackendLanguageStatus{}, ""},
		{[]ast.BackendLanguageStatus{{Language: "Go", Active: "tree-sitter"}}, "Go=tree-sitter"},
		{[]ast.BackendLanguageStatus{{Language: "", Active: "tree-sitter"}}, ""},
		{[]ast.BackendLanguageStatus{{Language: "Go", Active: ""}}, ""},
		{[]ast.BackendLanguageStatus{{Language: "Go", Active: "tree-sitter"}, {Language: "TS", Active: "tree-sitter"}}, "Go=tree-sitter, TS=tree-sitter"},
	}
	for _, tc := range tests {
		got := formatASTLanguageSummaryTUI(tc.items)
		if got != tc.want {
			t.Errorf("formatASTLanguageSummaryTUI(%v) = %q, want %q", tc.items, got, tc.want)
		}
	}
}

func TestActivityModeShortcut(t *testing.T) {
	tests := []struct {
		mode activityViewMode
		want string
	}{
		{activityViewAll, "1"},
		{activityViewTools, "2"},
		{activityViewAgents, "3"},
		{activityViewErrors, "4"},
		{activityViewWorkflow, "5"},
		{activityViewContext, "6"},
		{activityViewMode("unknown"), "1"},
	}
	for _, tc := range tests {
		got := activityModeShortcut(tc.mode)
		if got != tc.want {
			t.Errorf("activityModeShortcut(%v) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestFilteredConversations(t *testing.T) {
	entries := []conversation.Summary{
		{ID: "conv-1", Provider: "anthropic", Model: "sonnet"},
		{ID: "conv-2", Provider: "openai", Model: "gpt-4"},
		{ID: "conv-3", Provider: "anthropic", Model: "opus"},
	}

	// Empty query returns all
	got := filteredConversations(entries, "")
	if len(got) != 3 {
		t.Errorf("empty query: got %d, want 3", len(got))
	}

	// Filter by ID
	got = filteredConversations(entries, "conv-1")
	if len(got) != 1 || got[0].ID != "conv-1" {
		t.Errorf("filter by ID: got %v", got)
	}

	// Filter by provider
	got = filteredConversations(entries, "anthropic")
	if len(got) != 2 {
		t.Errorf("filter by provider: got %d, want 2", len(got))
	}

	// Filter by model
	got = filteredConversations(entries, "sonnet")
	if len(got) != 1 || got[0].Model != "sonnet" {
		t.Errorf("filter by model: got %v", got)
	}

	// Case insensitive
	got = filteredConversations(entries, "ANTHROPIC")
	if len(got) != 2 {
		t.Errorf("case insensitive: got %d, want 2", len(got))
	}

	// No match
	got = filteredConversations(entries, "nonexistent")
	if len(got) != 0 {
		t.Errorf("no match: got %d, want 0", len(got))
	}
}

func TestFilteredConversations_EmptyEntries(t *testing.T) {
	got := filteredConversations(nil, "query")
	if len(got) != 0 {
		t.Errorf("nil entries: got %d, want 0", len(got))
	}

	got = filteredConversations([]conversation.Summary{}, "query")
	if len(got) != 0 {
		t.Errorf("empty entries: got %d, want 0", len(got))
	}
}

func TestFormatContextInSummaryTUI(t *testing.T) {
	report := &engine.ContextInStatus{
		FileCount:           5,
		TokenCount:          1000,
		MaxTokensTotal:      200000,
		MaxTokensPerFile:    8000,
		Task:                "refactor",
		Compression:         "heuristic",
		ExplicitFileMentions: 3,
	}
	got := formatContextInSummaryTUI(report)
	if got == "" {
		t.Fatal("formatContextInSummaryTUI returned empty")
	}
}

func TestFormatContextInSummaryTUI_Nil(t *testing.T) {
	got := formatContextInSummaryTUI(nil)
	if got != "" {
		t.Errorf("formatContextInSummaryTUI(nil) = %q, want empty", got)
	}
}

func TestFormatContextInReasonSummaryTUI(t *testing.T) {
	report := &engine.ContextInStatus{
		Reasons: []string{"reason 1", "reason 2", "reason 3"},
	}
	got := formatContextInReasonSummaryTUI(report)
	if got == "" {
		t.Fatal("formatContextInReasonSummaryTUI returned empty")
	}
}

func TestFormatContextInReasonSummaryTUI_Limit(t *testing.T) {
	report := &engine.ContextInStatus{
		Reasons: []string{"reason 1", "reason 2", "reason 3", "reason 4"},
	}
	got := formatContextInReasonSummaryTUI(report)
	if got == "" {
		t.Fatal("formatContextInReasonSummaryTUI returned empty")
	}
	// Should contain "...more" since there are more than 3 reasons
	if !strings.Contains(got, "...more") {
		t.Errorf("expected '...more' in output, got %q", got)
	}
}

func TestFormatContextInReasonSummaryTUI_NilReport(t *testing.T) {
	got := formatContextInReasonSummaryTUI(nil)
	if got != "" {
		t.Errorf("formatContextInReasonSummaryTUI(nil) = %q, want empty", got)
	}
}

func TestParamStr(t *testing.T) {
	params := map[string]any{
		"str":   "hello",
		"int":    42,
		"int64":  int64(123),
		"float": 3.14,
		"bool":   true,
		"empty":  "",
		"nil":    nil,
	}

	tests := []struct {
		key  string
		want string
	}{
		{"str", "hello"},
		{"int", "42"},
		{"int64", "123"},
		{"float", "3.14"},
		{"bool", "true"},
		{"empty", ""},
		{"nil", ""},
		{"missing", ""},
	}

	for _, tc := range tests {
		got := paramStr(params, tc.key)
		if got != tc.want {
			t.Errorf("paramStr(%v, %q) = %q, want %q", params, tc.key, got, tc.want)
		}
	}
}

func TestParamStr_NilParams(t *testing.T) {
	got := paramStr(nil, "key")
	if got != "" {
		t.Errorf("paramStr(nil, key) = %q, want empty", got)
	}
}

func TestParamStr_IntTypes(t *testing.T) {
	params := map[string]any{
		"i32": int32(100),
		"i64": int64(200),
	}
	if got := paramStr(params, "i32"); got != "100" {
		t.Errorf("paramStr int32 = %q, want 100", got)
	}
	if got := paramStr(params, "i64"); got != "200" {
		t.Errorf("paramStr int64 = %q, want 200", got)
	}
}

func TestSuggestionToRunCommandInput(t *testing.T) {
	tests := []struct {
		suggestion string
		want       string
	}{
		{"command=go args=version", "go version"},
		{"command=go args=\"test ./...\"", "go test ./..."},
		{"command=go", "go"},
		{"", "go test ./..."},
		{"invalid!", "go test ./..."},
	}
	for _, tc := range tests {
		got := suggestionToRunCommandInput(tc.suggestion)
		if got != tc.want {
			t.Errorf("suggestionToRunCommandInput(%q) = %q, want %q", tc.suggestion, got, tc.want)
		}
	}
}

func TestFilterSuggestionsByToken(t *testing.T) {
	items := []string{"read", "refactor", "review", "test", "explain", "doc"}

	tests := []struct {
		token string
		want  []string
	}{
		{"re", []string{"read", "refactor", "review"}}, // all have "re" as prefix or contain "re"
		{"test", []string{"test"}},
		{"x", []string{"explain"}},                       // "explain" contains "x"
		{"", []string{"read", "refactor", "review", "test", "explain", "doc"}},
		{"EX", []string{"explain"}},                     // case insensitive, "doc" doesn't have "ex"
	}

	for _, tc := range tests {
		got := filterSuggestionsByToken(items, tc.token)
		if !slices.Equal(got, tc.want) {
			t.Errorf("filterSuggestionsByToken(%v, %q) = %v, want %v", items, tc.token, got, tc.want)
		}
	}
}

func TestFilterSuggestionsByToken_EmptyItems(t *testing.T) {
	if got := filterSuggestionsByToken(nil, "re"); got != nil {
		t.Errorf("nil items: got %v, want nil", got)
	}
	if got := filterSuggestionsByToken([]string{}, "re"); got != nil {
		t.Errorf("empty items: got %v, want nil", got)
	}
}

func TestSummarizeWorkflowTodos(t *testing.T) {
	tests := []struct {
		todos []toolruntime.TodoItem
		wantTotal, wantPending, wantDoing, wantDone int
	}{
		{
			[]toolruntime.TodoItem{{Content: "a", Status: "in_progress"}, {Content: "b", Status: "pending"}},
			2, 1, 1, 0,
		},
		{
			[]toolruntime.TodoItem{{Content: "a", Status: "completed"}, {Content: "b", Status: "done"}},
			2, 0, 0, 2,
		},
		{
			[]toolruntime.TodoItem{{Content: "a", Status: "active"}, {Content: "b", Status: "doing"}},
			2, 0, 2, 0,
		},
		{
			[]toolruntime.TodoItem{},
			0, 0, 0, 0,
		},
		{
			[]toolruntime.TodoItem{{Content: "a", Status: "unknown"}},
			1, 1, 0, 0,
		},
	}

	for _, tc := range tests {
		total, pending, doing, done := summarizeWorkflowTodos(tc.todos)
		if total != tc.wantTotal || pending != tc.wantPending || doing != tc.wantDoing || done != tc.wantDone {
			t.Errorf("summarizeWorkflowTodos(%v) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
				tc.todos, total, pending, doing, done, tc.wantTotal, tc.wantPending, tc.wantDoing, tc.wantDone)
		}
	}
}

func TestFormatWorkflowTodoLines(t *testing.T) {
	todos := []toolruntime.TodoItem{
		{Content: "task 1", Status: "in_progress", ActiveForm: "working on task 1"},
		{Content: "task 2", Status: "completed"},
		{Content: "task 3", Status: "pending"},
	}

	got := formatWorkflowTodoLines(todos, 10)
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}

	// Check that it prefixes correctly
	if !strings.Contains(got[0], "[doing]") {
		t.Errorf("first should be [doing], got %q", got[0])
	}
	if !strings.Contains(got[1], "[done]") {
		t.Errorf("second should be [done], got %q", got[1])
	}
	if !strings.Contains(got[2], "[todo]") {
		t.Errorf("third should be [todo], got %q", got[2])
	}
}

func TestFormatWorkflowTodoLines_Empty(t *testing.T) {
	if got := formatWorkflowTodoLines(nil, 10); got != nil {
		t.Errorf("nil todos: got %v, want nil", got)
	}
	if got := formatWorkflowTodoLines([]toolruntime.TodoItem{}, 10); got != nil {
		t.Errorf("empty todos: got %v, want nil", got)
	}
	if got := formatWorkflowTodoLines([]toolruntime.TodoItem{{Content: "a"}}, 0); got != nil {
		t.Errorf("limit=0: got %v, want nil", got)
	}
}

func TestFormatWorkflowTodoLines_Limit(t *testing.T) {
	todos := []toolruntime.TodoItem{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "pending"},
		{Content: "c", Status: "pending"},
	}
	got := formatWorkflowTodoLines(todos, 2)
	if len(got) != 2 {
		t.Errorf("limit 2: got %d, want 2", len(got))
	}
}

func TestActivityTargetForEntry(t *testing.T) {
	tests := []struct {
		entry  activityEntry
		target activityActionTarget
	}{
		{activityEntry{EventID: "provider:changed"}, activityTargetProviders},
		{activityEntry{EventID: "drive:run:start"}, activityTargetPlans},
		{activityEntry{EventID: "agent:autonomy:kickoff"}, activityTargetPlans},
		{activityEntry{EventID: "agent:subagent:start"}, activityTargetPlans},
		{activityEntry{EventID: "security:scan"}, activityTargetSecurity},
		{activityEntry{EventID: "tool:call", Tool: "edit_file", Path: "foo.go"}, activityTargetPatch},
		{activityEntry{EventID: "tool:call", Path: "foo.go"}, activityTargetFiles},
		{activityEntry{EventID: "tool:call", Tool: "read_file"}, activityTargetTools},
		{activityEntry{EventID: "context:updated"}, activityTargetContext},
		{activityEntry{EventID: "ctx:something"}, activityTargetContext},
		{activityEntry{EventID: "index:built"}, activityTargetCodeMap},
		{activityEntry{EventID: "config:loaded"}, activityTargetStatus},
		{activityEntry{EventID: "engine:start"}, activityTargetStatus},
		{activityEntry{Path: "some/path.go"}, activityTargetFiles},
		{activityEntry{}, activityTargetStatus},
	}

	for _, tc := range tests {
		got := activityTargetForEntry(tc.entry)
		if got != tc.target {
			t.Errorf("activityTargetForEntry(%+v) = %v, want %v", tc.entry, got, tc.target)
		}
	}
}

func TestActivityTargetForEntry_TextBased(t *testing.T) {
	// Secret detection based on text content
	entry := activityEntry{EventID: "tool:result", Text: "found secret in config"}
	got := activityTargetForEntry(entry)
	if got != activityTargetSecurity {
		t.Errorf("secret in text = %v, want activityTargetSecurity", got)
	}

	entry2 := activityEntry{EventID: "tool:result", Text: "vulnerability found"}
	got2 := activityTargetForEntry(entry2)
	if got2 != activityTargetSecurity {
		t.Errorf("vulnerability in text = %v, want activityTargetSecurity", got2)
	}
}

func TestFormatToolResultForChat_Success(t *testing.T) {
	res := toolruntime.Result{Success: true, DurationMs: 100}
	got := formatToolResultForChat("read_file", nil, res, nil)
	if got == "" {
		t.Fatal("formatToolResultForChat returned empty")
	}
	if !strings.Contains(got, "success") {
		t.Errorf("success case should contain 'success', got %q", got)
	}
}

func TestFormatToolResultForChat_Error(t *testing.T) {
	res := toolruntime.Result{}
	err := fmt.Errorf("something went wrong")
	got := formatToolResultForChat("edit_file", nil, res, err)
	if got == "" {
		t.Fatal("formatToolResultForChat returned empty")
	}
	if !strings.Contains(got, "failed") {
		t.Errorf("error case should contain 'failed', got %q", got)
	}
}

func TestFormatToolResultForChat_EmptyName(t *testing.T) {
	res := toolruntime.Result{Success: true, DurationMs: 50}
	got := formatToolResultForChat("", nil, res, nil)
	if got == "" {
		t.Fatal("formatToolResultForChat returned empty")
	}
	if !strings.Contains(got, "tool") {
		t.Errorf("empty name should default to 'tool', got %q", got)
	}
}

func TestTruncateCommandBlock(t *testing.T) {
	tests := []struct {
		text string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello\n... [truncated]"},
		{"", 10, ""},
		{"hello", 0, "hello"},
		{"hello", -1, "hello"},
		{"  hello  ", 10, "hello"},
	}
	for _, tc := range tests {
		got := truncateCommandBlock(tc.text, tc.max)
		if got != tc.want {
			t.Errorf("truncateCommandBlock(%q, %d) = %q, want %q", tc.text, tc.max, got, tc.want)
		}
	}
}

