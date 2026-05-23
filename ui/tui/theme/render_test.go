package theme

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestRoleBadge(t *testing.T) {
	for _, role := range []string{"user", "assistant", "system", "tool"} {
		out := RoleBadge(role)
		if out == "" {
			t.Fatalf("RoleBadge(%q) returned empty string", role)
		}
	}
}

func TestRoleLineStyle(t *testing.T) {
	for _, role := range []string{"user", "assistant", "system", "tool", "coach"} {
		style := RoleLineStyle(role)
		out := style.Render("x")
		if out == "" {
			t.Fatalf("RoleLineStyle(%q) rendered empty string", role)
		}
	}
}

func TestSectionHeader(t *testing.T) {
	out := SectionHeader("▶", "Test")
	if out == "" {
		t.Fatal("SectionHeader returned empty")
	}
	if !contains(out, "Test") {
		t.Fatalf("expected section header to contain label, got %q", out)
	}
}

func TestRenderMarkdownLite_NonEmpty(t *testing.T) {
	out := RenderMarkdownLite("hello world")
	if out == "" {
		t.Fatal("RenderMarkdownLite returned empty for plain text")
	}
}

func TestIsTableHeader(t *testing.T) {
	if !IsTableHeader("| colA | colB |") {
		t.Fatal("expected true for table header row")
	}
	if IsTableHeader("plain text") {
		t.Fatal("expected false for plain text")
	}
}

func TestIsTableSeparator(t *testing.T) {
	if !IsTableSeparator("|---|---|") {
		t.Fatal("expected true for table separator")
	}
	if IsTableSeparator("| colA |") {
		t.Fatal("expected false for non-separator")
	}
}

func TestMax0(t *testing.T) {
	if Max0(5) != 5 {
		t.Fatalf("Max0(5) = %d, want 5", Max0(5))
	}
	if Max0(-3) != 0 {
		t.Fatalf("Max0(-3) = %d, want 0", Max0(-3))
	}
}

func TestBulletLine(t *testing.T) {
	bullet, rest, ok := BulletLine("- item")
	if !ok {
		t.Fatal("expected ok for bullet line")
	}
	if bullet == "" {
		t.Fatal("expected non-empty bullet")
	}
	_ = rest // rest may be empty for whitespace-only lines
}

func TestSplitTableRow(t *testing.T) {
	cells := SplitTableRow("| a | b | c |", '|')
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d: %v", len(cells), cells)
	}
}

func TestToolChipRender(t *testing.T) {
	chip := ToolChip{
		Name:         "read_file",
		Status:       "ok",
		DurationMs:   120,
		Preview:      "42 lines",
		Step:         1,
		OutputTokens: 300,
	}
	out := RenderToolChip(chip, 80)
	if out == "" {
		t.Fatal("RenderToolChip returned empty")
	}
}

func TestFormatToolTokenCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0 tok"},
		{500, "~1 tok"},
		{1500, "~2 tok"},
	}
	for _, tc := range tests {
		got := FormatToolTokenCount(tc.n)
		if got == "" {
			t.Fatalf("FormatToolTokenCount(%d) empty", tc.n)
		}
	}
}

func TestRenderTodoStrip_NonEmpty(t *testing.T) {
	items := []TodoStripItem{
		{Content: "write tests", Status: "in_progress", ActiveForm: "writing tests"},
		{Content: "fix bug", Status: "pending"},
	}
	out := RenderTodoStrip(items, 80)
	if out == "" {
		t.Fatal("RenderTodoStrip returned empty")
	}
}

func TestRenderRuntimeCard_NonEmpty(t *testing.T) {
	rs := RuntimeSummary{Active: true, ToolRounds: 2}
	out := RenderRuntimeCard(rs, 80)
	if out == "" {
		t.Fatal("RenderRuntimeCard returned empty")
	}
}

func TestFormatDurationChip(t *testing.T) {
	out := FormatDurationChip(1234)
	if out == "" {
		t.Fatal("FormatDurationChip returned empty")
	}
}

func TestSpinnerFrame(t *testing.T) {
	out := SpinnerFrame(0)
	if out == "" {
		t.Fatal("SpinnerFrame returned empty")
	}
}

func TestCompactTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "~1k"},
		{1500, "~2k"},
		{999999, "~1M"},
	}
	for _, tc := range tests {
		got := CompactTokens(tc.n)
		if got == "" {
			t.Fatalf("CompactTokens(%d) empty", tc.n)
		}
	}
}

func TestFormatThousands(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1_500_000, "1,500,000"},
	}
	for _, tc := range tests {
		got := FormatThousands(tc.n)
		if got != tc.want {
			t.Errorf("FormatThousands(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestTruncateSingleLine(t *testing.T) {
	out := TruncateSingleLine("hello world", 5)
	if out == "" {
		t.Fatal("TruncateSingleLine returned empty")
	}
}

func TestStatsPanelMode_Values(t *testing.T) {
	modes := []StatsPanelMode{
		StatsPanelModeOverview,
		StatsPanelModeTodos,
		StatsPanelModeTasks,
		StatsPanelModeSubagents,
		StatsPanelModeProviders,
	}
	for _, m := range modes {
		if m == "" {
			t.Fatalf("StatsPanelMode value is empty string")
		}
	}
}

func TestToolChip_Fields(t *testing.T) {
	chip := ToolChip{
		Name:            "test",
		CompressedChars: 50,
		SavedChars:      25,
		CompressionPct:  33,
		InnerLines:      []string{"line1", "line2"},
		Reason:          "checking file",
	}
	if chip.Name != "test" {
		t.Fatalf("Name = %q, want test", chip.Name)
	}
	if chip.CompressionPct != 33 {
		t.Fatalf("CompressionPct = %d, want 33", chip.CompressionPct)
	}
}

func TestProviderPanelRow_Fields(t *testing.T) {
	row := ProviderPanelRow{
		Name:    "anthropic",
		Active:  true,
		Primary: true,
		Models:  []string{"claude-3-5-sonnet"},
	}
	if row.Name != "anthropic" {
		t.Fatalf("Name = %q, want anthropic", row.Name)
	}
	if !row.Active {
		t.Fatal("Expected Active=true")
	}
}

// contains is a helper not available in testing package.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- markdown.go tests ---

func TestRenderMarkdownBlocks_Empty(t *testing.T) {
	out := RenderMarkdownBlocks("", 80)
	if out != nil {
		t.Fatalf("RenderMarkdownBlocks(%q) = %v, want nil", "", out)
	}
}

func TestRenderMarkdownBlocks_PlainText(t *testing.T) {
	out := RenderMarkdownBlocks("hello world", 80)
	if len(out) == 0 {
		t.Fatal("RenderMarkdownBlocks returned empty for plain text")
	}
	if !contains(out[0], "hello world") {
		t.Fatalf("expected plain text in output, got %q", out)
	}
}

func TestRenderMarkdownBlocks_Headers(t *testing.T) {
	out := RenderMarkdownBlocks("# Hello\n## World\n### Test", 80)
	if len(out) < 3 {
		t.Fatalf("expected at least 3 lines for headers, got %d", len(out))
	}
}

func TestRenderMarkdownBlocks_Bullet(t *testing.T) {
	out := RenderMarkdownBlocks("- item1\n- item2", 80)
	if len(out) < 2 {
		t.Fatalf("expected at least 2 lines for bullets, got %d", len(out))
	}
}

func TestRenderMarkdownBlocks_CodeFence(t *testing.T) {
	out := RenderMarkdownBlocks("```go\nfunc main() {}\n```", 80)
	if len(out) == 0 {
		t.Fatal("RenderMarkdownBlocks returned empty for code fence")
	}
}

func TestRenderMarkdownTable_DrawsBoxAndWrapsWithinWidth(t *testing.T) {
	md := strings.Join([]string{
		"| Name | Description |",
		"| --- | --- |",
		"| Alpha | This is a very long description that should wrap inside the cell before the transcript scrollbar |",
	}, "\n")
	out := RenderMarkdownBlocks(md, 42)
	if len(out) < 5 {
		t.Fatalf("expected multi-line boxed table, got %v", out)
	}
	joined := strings.Join(out, "\n")
	for _, want := range []string{"\u250c", "\u252c", "\u251c", "\u253c", "\u2514"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered table missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "---") {
		t.Fatalf("markdown separator row should not render as table content:\n%s", joined)
	}
	for _, line := range out {
		if w := ansi.StringWidth(line); w > 42 {
			t.Fatalf("table line width %d > 42: %q\n%s", w, line, joined)
		}
	}
}

func TestContainsBoxSeparator_Empty(t *testing.T) {
	if ContainsBoxSeparator("") {
		t.Fatal("ContainsBoxSeparator(\"\") = true, want false")
	}
}

func TestContainsBoxSeparator_Valid(t *testing.T) {
	// ContainsBoxSeparator requires at least one dash (─ or -) and only valid box chars
	tests := []string{
		"───────",
		"├──────┤",
		"|-------|",
		"─ ─ ─ ─",
	}
	for _, s := range tests {
		if !ContainsBoxSeparator(s) {
			t.Errorf("ContainsBoxSeparator(%q) = false, want true", s)
		}
	}
}

func TestContainsBoxSeparator_Invalid(t *testing.T) {
	tests := []string{
		"hello",
		"abc123",
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
	}
	for _, s := range tests {
		if ContainsBoxSeparator(s) {
			t.Errorf("ContainsBoxSeparator(%q) = true, want false", s)
		}
	}
}

func TestHeaderLevel(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"# Title", 1},
		{"## Subtitle", 2},
		{"### Detail", 3},
		{"No header", 0},
		{"#### Too deep", 0},
		{"#no space", 0},
	}
	for _, tc := range tests {
		got := HeaderLevel(tc.input)
		if got != tc.want {
			t.Errorf("HeaderLevel(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// --- render.go tests ---

func TestRenderChatWorkflowFocusCard_Empty(t *testing.T) {
	info := StatsPanelInfo{Mode: StatsPanelModeOverview}
	out := RenderChatWorkflowFocusCard(info, 80)
	if out != "" {
		t.Fatalf("RenderChatWorkflowFocusCard with overview mode = %q, want empty", out)
	}
}

func TestRenderChatWorkflowFocusCard_TodosMode(t *testing.T) {
	info := StatsPanelInfo{
		Mode:      StatsPanelModeTodos,
		TodoLines: []string{"task 1", "task 2"},
	}
	out := RenderChatWorkflowFocusCard(info, 80)
	if out == "" {
		t.Fatal("RenderChatWorkflowFocusCard returned empty for todos mode")
	}
	if !contains(out, "Workflow Focus") {
		t.Fatalf("expected 'Workflow Focus' in output, got %q", out)
	}
}

func TestRenderChatWorkflowFocusCard_TasksMode(t *testing.T) {
	info := StatsPanelInfo{
		Mode:      StatsPanelModeTasks,
		TaskLines: []string{"task A"},
	}
	out := RenderChatWorkflowFocusCard(info, 80)
	if out == "" {
		t.Fatal("RenderChatWorkflowFocusCard returned empty for tasks mode")
	}
}

func TestRenderChatWorkflowFocusCard_WidthMin(t *testing.T) {
	info := StatsPanelInfo{Mode: StatsPanelModeTodos}
	out := RenderChatWorkflowFocusCard(info, 10)
	// width is bumped to 36 internally, so it should not be empty
	if out == "" {
		t.Fatal("RenderChatWorkflowFocusCard should not return empty even with narrow width")
	}
}

func TestRenderMessageHeader_Basic(t *testing.T) {
	info := MessageHeaderInfo{Role: "assistant"}
	out := RenderMessageHeader(info)
	if out == "" {
		t.Fatal("RenderMessageHeader returned empty")
	}
	if !contains(out, "ASSISTANT") {
		t.Fatalf("expected 'ASSISTANT' badge in output, got %q", out)
	}
}

func TestRenderMessageHeader_AllFields(t *testing.T) {
	info := MessageHeaderInfo{
		Role:         "user",
		TokenCount:   100,
		DurationMs:   500,
		ToolCalls:    3,
		ToolFailures: 1,
		CopyIndex:    2,
	}
	out := RenderMessageHeader(info)
	if out == "" {
		t.Fatal("RenderMessageHeader returned empty")
	}
}

func TestFormatRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ts   time.Time
		want string
	}{
		{now, "(just now)"},
		{now.Add(-10 * time.Second), "(just now)"},
		{now.Add(-45 * time.Second), "(45s ago)"},
		{now.Add(-2 * time.Minute), "(2m ago)"},
		{now.Add(-90 * time.Minute), "(1h30m ago)"},
		{now.Add(-3 * time.Hour), "(3h ago)"},
		{now.Add(-25 * time.Hour), "(1d ago)"},
		{time.Time{}, ""},
		{now.Add(time.Minute), ""},        // future → ignore
		{now.Add(-365 * 24 * time.Hour), ""}, // older than 30d → ignore
	}
	for _, tc := range cases {
		got := FormatRelativeTime(tc.ts, now)
		if got != tc.want {
			t.Errorf("FormatRelativeTime(%v) = %q, want %q", tc.ts, got, tc.want)
		}
	}
}

func TestFormatModelBadge(t *testing.T) {
	cases := []struct {
		provider, model, want string
	}{
		{"", "", ""},
		{"anthropic", "", "· anthropic"},
		{"", "gpt-4o", "· gpt-4o"},
		{"anthropic", "claude-opus-4-7", "· claude-opus-4-7"},
		{"openai", "gpt-4o", "· gpt-4o"},
	}
	for _, tc := range cases {
		if got := FormatModelBadge(tc.provider, tc.model); got != tc.want {
			t.Errorf("FormatModelBadge(%q,%q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestRenderMessageHeader_RelativeTimeAndModelBadge(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	info := MessageHeaderInfo{
		Role:      "assistant",
		Timestamp: now.Add(-3 * time.Minute),
		Now:       now,
		Provider:  "anthropic",
		Model:     "claude-opus-4-7",
	}
	out := RenderMessageHeader(info)
	if !contains(out, "(3m ago)") {
		t.Errorf("expected relative time chip, got %q", out)
	}
	if !contains(out, "claude-opus-4-7") {
		t.Errorf("expected model badge, got %q", out)
	}
}

func TestRenderMessageHeader_CancelledChip(t *testing.T) {
	info := MessageHeaderInfo{Role: "assistant", Cancelled: true}
	out := RenderMessageHeader(info)
	if !contains(out, "cancelled") {
		t.Errorf("expected cancelled chip, got %q", out)
	}
}

func TestRenderMessageHeader_DoneChipForCompletedAssistant(t *testing.T) {
	info := MessageHeaderInfo{Role: "assistant", Done: true}
	out := RenderMessageHeader(info)
	if !contains(out, "✓") {
		t.Errorf("expected done check chip, got %q", out)
	}
}

func TestRenderMessageHeader_DoneSuppressedWhenCancelled(t *testing.T) {
	// Cancelled wins: ⊘ shows, ✓ does not (the turn isn't actually done).
	info := MessageHeaderInfo{Role: "assistant", Done: true, Cancelled: true}
	out := RenderMessageHeader(info)
	if contains(out, "✓") {
		t.Errorf("done chip should be suppressed when cancelled, got %q", out)
	}
	if !contains(out, "cancelled") {
		t.Errorf("expected cancelled chip, got %q", out)
	}
}

func TestRenderMessageHeader_DoneSuppressedWhileStreaming(t *testing.T) {
	// Streaming spinner shows; the renderer should not also paint ✓.
	info := MessageHeaderInfo{Role: "assistant", Done: true, Streaming: true}
	out := RenderMessageHeader(info)
	if contains(out, "✓") {
		t.Errorf("done chip should be suppressed while streaming, got %q", out)
	}
}

func TestRenderMessageBubble_Empty(t *testing.T) {
	out := RenderMessageBubble("assistant", "", "header", 80)
	if out == "" {
		t.Fatal("RenderMessageBubble returned empty")
	}
}

func TestRenderMessageBubble_WithContent(t *testing.T) {
	out := RenderMessageBubble("user", "hello world", "header", 80)
	if out == "" {
		t.Fatal("RenderMessageBubble returned empty")
	}
}

func TestRenderMessageBubble_TableFitsContentWidth(t *testing.T) {
	md := strings.Join([]string{
		"| Name | Description |",
		"| --- | --- |",
		"| Alpha | This is a long description that should wrap inside the table cell |",
	}, "\n")
	out := RenderMessageBubble("assistant", md, "header", 44)
	for _, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 44 {
			t.Fatalf("bubble line width %d > 44: %q\n%s", w, line, out)
		}
	}
	if !strings.Contains(out, "\u250c") || !strings.Contains(out, "\u2514") {
		t.Fatalf("message bubble should contain box-drawing table:\n%s", out)
	}
}

func TestRenderMessageBubble_NarrowWidth(t *testing.T) {
	out := RenderMessageBubble("assistant", "text", "header", 2)
	if out == "" {
		t.Fatal("RenderMessageBubble returned empty for narrow width")
	}
}

func TestWrapBubbleLine_SingleLine(t *testing.T) {
	out := WrapBubbleLine("short", 80)
	if len(out) != 1 {
		t.Fatalf("WrapBubbleLine short line returned %d lines, want 1", len(out))
	}
}

func TestWrapBubbleLine_ZeroLimit(t *testing.T) {
	out := WrapBubbleLine("text", 0)
	if len(out) != 1 || out[0] != "text" {
		t.Fatalf("WrapBubbleLine with limit 0 = %v, want [text]", out)
	}
}

func TestWrapBubbleLine_WrapsLongLine(t *testing.T) {
	longLine := ""
	for i := 0; i < 200; i++ {
		longLine += "x"
	}
	out := WrapBubbleLine(longLine, 40)
	if len(out) <= 1 {
		t.Fatalf("WrapBubbleLine long line returned only %d lines, expected wrapping", len(out))
	}
}

func TestHardWrapByCells_Short(t *testing.T) {
	out := HardWrapByCells("short", 80)
	if len(out) != 1 || out[0] != "short" {
		t.Fatalf("HardWrapByCells short = %v, want [short]", out)
	}
}

func TestHardWrapByCells_Zero(t *testing.T) {
	out := HardWrapByCells("text", 0)
	if len(out) != 1 || out[0] != "text" {
		t.Fatalf("HardWrapByCells limit 0 = %v, want [text]", out)
	}
}

func TestHardWrapByCells_Wraps(t *testing.T) {
	out := HardWrapByCells("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", 20)
	if len(out) <= 1 {
		t.Fatalf("HardWrapByCells did not wrap, got %v", out)
	}
}

func TestRenderDivider_Zero(t *testing.T) {
	out := RenderDivider(0)
	if out != "" {
		t.Fatalf("RenderDivider(0) = %q, want empty", out)
	}
}

func TestRenderDivider_Normal(t *testing.T) {
	out := RenderDivider(40)
	if out == "" {
		t.Fatal("RenderDivider returned empty")
	}
	if len(out) < 40 {
		t.Fatalf("RenderDivider(40) length %d, want at least 40", len(out))
	}
}

func TestRenderDivider_Clamped(t *testing.T) {
	out := RenderDivider(500)
	if out == "" {
		t.Fatal("RenderDivider(500) returned empty")
	}
	// RenderDivider clamps width to 200, so repeated "─" should appear in output
	if !contains(out, "─") {
		t.Fatal("RenderDivider(500) should contain dashes")
	}
}

func TestRenderInputBox_Narrow(t *testing.T) {
	out := RenderInputBox("hello", 5)
	if out == "" {
		t.Fatal("RenderInputBox returned empty")
	}
}

func TestRenderInputBox_Normal(t *testing.T) {
	out := RenderInputBox("hello world", 80)
	if out == "" {
		t.Fatal("RenderInputBox returned empty")
	}
}

func TestFormatInputBoxContent_Empty(t *testing.T) {
	out := FormatInputBoxContent("", 80)
	if out != "" {
		t.Fatalf("FormatInputBoxContent empty = %q, want empty", out)
	}
}

func TestFormatInputBoxContent_ZeroLimit(t *testing.T) {
	out := FormatInputBoxContent("text", 0)
	if out != "text" {
		t.Fatalf("FormatInputBoxContent limit 0 = %q, want text", out)
	}
}

func TestFormatInputBoxContent_Newlines(t *testing.T) {
	out := FormatInputBoxContent("line1\nline2", 80)
	if out == "" {
		t.Fatal("FormatInputBoxContent with newlines returned empty")
	}
}

func TestRenderChatHeader_Slim(t *testing.T) {
	info := ChatHeaderInfo{Slim: true}
	out := RenderChatHeader(info, 80)
	if out == "" {
		t.Fatal("RenderChatHeader returned empty")
	}
}

func TestRenderChatHeader_Full(t *testing.T) {
	info := ChatHeaderInfo{
		Provider:      "anthropic",
		Model:         "claude-3-5-sonnet",
		Configured:    true,
		MaxContext:    200000,
		ContextTokens: 50000,
		ToolsEnabled:  true,
		AgentActive:   true,
		AgentPhase:    "thinking",
		AgentStep:     1,
		AgentMax:      10,
	}
	out := RenderChatHeader(info, 120)
	if out == "" {
		t.Fatal("RenderChatHeader returned empty for full info")
	}
}

func TestRenderChatHeader_PlanMode(t *testing.T) {
	info := ChatHeaderInfo{PlanMode: true}
	out := RenderChatHeader(info, 80)
	if out == "" {
		t.Fatal("RenderChatHeader returned empty for plan mode")
	}
}

func TestRenderChatHeader_Approval(t *testing.T) {
	info := ChatHeaderInfo{ApprovalPending: true}
	out := RenderChatHeader(info, 80)
	if out == "" {
		t.Fatal("RenderChatHeader returned empty for approval pending")
	}
}

func TestRenderChatHeader_Parked(t *testing.T) {
	info := ChatHeaderInfo{Parked: true}
	out := RenderChatHeader(info, 80)
	if out == "" {
		t.Fatal("RenderChatHeader returned empty for parked")
	}
}

func TestRenderChatHeader_Drive(t *testing.T) {
	info := ChatHeaderInfo{
		DriveRunID: "abc123",
		DriveDone:  2,
		DriveTotal: 5,
	}
	out := RenderChatHeader(info, 80)
	if out == "" {
		t.Fatal("RenderChatHeader returned empty for drive info")
	}
}

func TestRenderChatModeSegment_Ready(t *testing.T) {
	info := ChatHeaderInfo{}
	out := RenderChatModeSegment(info)
	if out == "" {
		t.Fatal("RenderChatModeSegment returned empty for ready state")
	}
}

func TestRenderChatModeSegment_Streaming(t *testing.T) {
	info := ChatHeaderInfo{Streaming: true}
	out := RenderChatModeSegment(info)
	if out == "" {
		t.Fatal("RenderChatModeSegment returned empty for streaming")
	}
}

func TestRenderChatModeSegment_AgentActive(t *testing.T) {
	info := ChatHeaderInfo{
		AgentActive: true,
		AgentPhase:  "executing",
		AgentStep:   5,
		AgentMax:    20,
	}
	out := RenderChatModeSegment(info)
	if out == "" {
		t.Fatal("RenderChatModeSegment returned empty for agent active")
	}
}

func TestDefaultStarterPrompts(t *testing.T) {
	prompts := DefaultStarterPrompts()
	if len(prompts) == 0 {
		t.Fatal("DefaultStarterPrompts returned empty")
	}
	if prompts[0].Key == "" || prompts[0].Title == "" || prompts[0].Cmd == "" {
		t.Fatal("DefaultStarterPrompts returned invalid prompt struct")
	}
}

func TestStarterTemplateForDigit_Valid(t *testing.T) {
	cmd, ok := StarterTemplateForDigit('1')
	if !ok {
		t.Fatal("StarterTemplateForDigit('1') returned ok=false")
	}
	if cmd == "" {
		t.Fatal("StarterTemplateForDigit('1') returned empty cmd")
	}
}

func TestStarterTemplateForDigit_Invalid(t *testing.T) {
	_, ok := StarterTemplateForDigit('0')
	if ok {
		t.Fatal("StarterTemplateForDigit('0') should return ok=false")
	}
	_, ok = StarterTemplateForDigit('9')
	if ok {
		t.Fatal("StarterTemplateForDigit('9') should return ok=false")
	}
}

func TestRenderStarterPrompts_Configured(t *testing.T) {
	out := RenderStarterPrompts(120, true)
	if len(out) == 0 {
		t.Fatal("RenderStarterPrompts returned empty for configured")
	}
}

func TestRenderStarterPrompts_Unconfigured(t *testing.T) {
	out := RenderStarterPrompts(120, false)
	if len(out) == 0 {
		t.Fatal("RenderStarterPrompts returned empty for unconfigured")
	}
}

func TestRenderStarterPrompts_ZeroWidth(t *testing.T) {
	out := RenderStarterPrompts(0, true)
	if len(out) == 0 {
		t.Fatal("RenderStarterPrompts returned empty for zero width")
	}
}

func TestRenderStreamingIndicator_EmptyPhase(t *testing.T) {
	out := RenderStreamingIndicator("", 0)
	if out == "" {
		t.Fatal("RenderStreamingIndicator returned empty")
	}
}

func TestRenderStreamingIndicator_WithPhase(t *testing.T) {
	out := RenderStreamingIndicator("thinking", 5)
	if out == "" {
		t.Fatal("RenderStreamingIndicator returned empty")
	}
}

func TestRenderResumeBanner_Basic(t *testing.T) {
	out := RenderResumeBanner(5, 10, 80)
	if out == "" {
		t.Fatal("RenderResumeBanner returned empty")
	}
}

func TestRenderResumeBanner_Narrow(t *testing.T) {
	out := RenderResumeBanner(3, 0, 80)
	if out == "" {
		t.Fatal("RenderResumeBanner returned empty")
	}
}

func TestRenderResumeBanner_ZeroWidth(t *testing.T) {
	out := RenderResumeBanner(1, 5, 0)
	if out == "" {
		t.Fatal("RenderResumeBanner returned empty for zero width")
	}
}

// --- render_bars.go tests ---

func TestRenderTokenMeter_ZeroUsed(t *testing.T) {
	out := RenderTokenMeter(0, 0)
	if out == "" {
		t.Fatal("RenderTokenMeter(0, 0) returned empty")
	}
}

func TestRenderTokenMeter_NoMax(t *testing.T) {
	out := RenderTokenMeter(1000, 0)
	if out == "" {
		t.Fatal("RenderTokenMeter(1000, 0) returned empty")
	}
}

func TestRenderTokenMeter_Normal(t *testing.T) {
	out := RenderTokenMeter(50000, 200000)
	if out == "" {
		t.Fatal("RenderTokenMeter(50000, 200000) returned empty")
	}
}

func TestRenderTokenMeter_HighUsage(t *testing.T) {
	out := RenderTokenMeter(190000, 200000)
	if out == "" {
		t.Fatal("RenderTokenMeter high usage returned empty")
	}
}

func TestRenderStepBar_ZeroMax(t *testing.T) {
	out := RenderStepBar(5, 0, 40, 0)
	if out == "" {
		t.Fatal("RenderStepBar(5, 0, 40, 0) returned empty")
	}
}

func TestRenderStepBar_Normal(t *testing.T) {
	out := RenderStepBar(5, 10, 40, 0)
	if out == "" {
		t.Fatal("RenderStepBar returned empty")
	}
}

func TestRenderStepBar_NarrowCells(t *testing.T) {
	out := RenderStepBar(3, 10, 2, 0)
	if out == "" {
		t.Fatal("RenderStepBar with narrow cells returned empty")
	}
}

func TestRenderStepBar_NegativeStep(t *testing.T) {
	out := RenderStepBar(-5, 10, 40, 0)
	if out == "" {
		t.Fatal("RenderStepBar with negative step returned empty")
	}
}

func TestRenderStepBar_StepOverflow(t *testing.T) {
	out := RenderStepBar(100, 10, 40, 0)
	if out == "" {
		t.Fatal("RenderStepBar with step > max returned empty")
	}
}

func TestRenderContextBar_ZeroMax(t *testing.T) {
	out := RenderContextBar(1000, 0, 40)
	if out == "" {
		t.Fatal("RenderContextBar(1000, 0, 40) returned empty")
	}
}

func TestRenderContextBar_Normal(t *testing.T) {
	out := RenderContextBar(50000, 200000, 40)
	if out == "" {
		t.Fatal("RenderContextBar returned empty")
	}
}

func TestRenderContextBarFrame_ZeroMax(t *testing.T) {
	out := RenderContextBarFrame(1000, 0, 40, 0)
	if out == "" {
		t.Fatal("RenderContextBarFrame returned empty for zero max")
	}
}

func TestRenderContextBarFrame_HighUsage(t *testing.T) {
	out := RenderContextBarFrame(190000, 200000, 40, 1)
	if out == "" {
		t.Fatal("RenderContextBarFrame high usage returned empty")
	}
}

// --- stats_panel.go tests ---

func TestRenderStatsPanel_Basic(t *testing.T) {
	info := StatsPanelInfo{}
	out := RenderStatsPanel(info, 30)
	if out == "" {
		t.Fatal("RenderStatsPanel returned empty")
	}
}

func TestRenderStatsPanelSized_Basic(t *testing.T) {
	info := StatsPanelInfo{}
	out := RenderStatsPanelSized(info, 30, 50)
	if out == "" {
		t.Fatal("RenderStatsPanelSized returned empty")
	}
}

func TestRenderStatsPanelSized_NarrowHeight(t *testing.T) {
	info := StatsPanelInfo{}
	out := RenderStatsPanelSized(info, 2, 50)
	if out == "" {
		t.Fatal("RenderStatsPanelSized returned empty for narrow height")
	}
}

func TestRenderStatsPanelSized_NarrowWidth(t *testing.T) {
	info := StatsPanelInfo{}
	out := RenderStatsPanelSized(info, 30, 20)
	if out == "" {
		t.Fatal("RenderStatsPanelSized returned empty for narrow width")
	}
}

// TestRenderStatsPanelOverviewProviderRowCompressed — Phase B dedup
// slice 5. The stats panel's Overview-mode PROVIDER section used to
// stack provider + model on two bold rows that duplicated the runtime-
// strip Top row (chat tab) and the dedicated ACTIVE section in
// Providers mode (alt+p). Compressed to a single subtle pointer line
// so signal-density survives without the 3-row block. Failure states
// (no provider, needs key) keep their loud multi-row form because
// those are actionable, not just labels.
func TestRenderStatsPanelOverviewProviderRowCompressed(t *testing.T) {
	configured := RenderStatsPanelSized(StatsPanelInfo{
		Provider: "anthropic", Model: "claude-opus-4-7",
		Configured: true, MaxContext: 200000,
	}, 30, 60)
	if !strings.Contains(configured, "anthropic / claude-opus-4-7") {
		t.Fatalf("Overview should still name the active provider/model on one line, got:\n%s", configured)
	}
	// Failure path stays multi-row + loud — the user must act here.
	missing := RenderStatsPanelSized(StatsPanelInfo{}, 30, 60)
	if !strings.Contains(missing, "no provider") {
		t.Fatalf("missing-provider state should still be loud, got:\n%s", missing)
	}
	if !strings.Contains(missing, "/provider") {
		t.Fatalf("missing-provider guidance should point at /provider, got:\n%s", missing)
	}

	needsKey := RenderStatsPanelSized(StatsPanelInfo{
		Provider: "anthropic", Model: "claude-opus-4-7",
		Configured: false, MaxContext: 200000,
	}, 30, 60)
	if !strings.Contains(needsKey, "needs key") || !strings.Contains(needsKey, "claude-opus-4-7") {
		t.Fatalf("needs-key state should keep the multi-row actionable form, got:\n%s", needsKey)
	}
}

// TestRenderStatsPanelOverviewWorkflowDropsTodoCountDuplicate — Phase B
// dedup slice 3. The runtime strip Work-row (always visible above chat)
// already shows `todos X/Y done, Z doing - <active>`. The Overview
// stats panel's WORKFLOW section used to print the same numbers in a
// different format right beside it. Now the section drops the count
// line and active text and only points to alt+s for the deep breakdown.
func TestRenderStatsPanelOverviewWorkflowDropsTodoCountDuplicate(t *testing.T) {
	info := StatsPanelInfo{
		Provider: "anthropic", Configured: true, MaxContext: 200000,
		TodoTotal: 6, TodoDone: 2, TodoDoing: 1, TodoPending: 3,
		TodoActive: "Wire Phase B dedup",
	}
	out := RenderStatsPanelSized(info, 30, 60)
	for _, banned := range []string{
		"todos 6 | 2 done | 1 doing | 3 pending",
		"active: Wire Phase B dedup",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("Overview WORKFLOW section should not echo runtime-strip TODO counts; saw %q in:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "finish active todo: Wire Phase B dedup") {
		t.Fatalf("expected overview NEXT to surface the active todo, got:\n%s", out)
	}

	// Sanity: in Todos mode the full breakdown is still the canonical
	// home — make sure we didn't accidentally remove that surface too.
	deep := RenderStatsPanelSized(StatsPanelInfo{
		Provider: "anthropic", Configured: true, MaxContext: 200000,
		Mode:      StatsPanelModeTodos,
		TodoTotal: 6, TodoDone: 2, TodoDoing: 1, TodoPending: 3,
	}, 30, 60)
	for _, want := range []string{"6 total", "1 doing", "3 pending"} {
		if !strings.Contains(deep, want) {
			t.Fatalf("Todos mode summary missing %q, got:\n%s", want, deep)
		}
	}
}

// TestRenderStatsPanelSuppressesParkedRowsWhileBannerActive — Phase B
// dedup slice. Two parked-related surfaces sit in the stats panel:
// the runtime block's "parked" row + "/continue to resume" hint, and
// the next-action critical hint "/continue resumes parked agent".
// While the resume banner is up (BannerActive=true), both should drop
// out so the panel doesn't echo the banner's primary message. The
// state-line at the top of the panel still reads "parked" — that's the
// canonical one-line distill and stays put.
func TestRenderStatsPanelSuppressesParkedRowsWhileBannerActive(t *testing.T) {
	withBanner := RenderStatsPanelSized(StatsPanelInfo{
		Provider: "anthropic", Configured: true, MaxContext: 200000,
		Parked: true, BannerActive: true,
	}, 30, 60)
	if strings.Contains(withBanner, "/continue to resume") {
		t.Fatalf("runtime parked hint should be hidden while banner is active, got:\n%s", withBanner)
	}
	if strings.Contains(withBanner, "/continue resumes parked agent") {
		t.Fatalf("next-action parked hint should be hidden while banner is active, got:\n%s", withBanner)
	}

	bannerDismissed := RenderStatsPanelSized(StatsPanelInfo{
		Provider: "anthropic", Configured: true, MaxContext: 200000,
		Parked: true, BannerActive: false,
	}, 30, 60)
	if !strings.Contains(bannerDismissed, "/continue resumes parked agent") {
		t.Fatalf("next-action parked hint should re-emerge when banner is dismissed, got:\n%s", bannerDismissed)
	}
}

func TestRenderStatsPanelModeTabs(t *testing.T) {
	out := RenderStatsPanelModeTabs(StatsPanelModeOverview, 100)
	if out == "" {
		t.Fatal("RenderStatsPanelModeTabs returned empty")
	}
}

func TestContextPayloadFromStatsCanonicalizesNextRequestBudget(t *testing.T) {
	info := StatsPanelInfo{
		Provider:              "openai",
		Model:                 "gpt-5.4",
		ContextProvider:       "zai",
		ContextModel:          "glm-5.1",
		ContextLimitSource:    "models.dev zai/glm-5.1",
		MaxContext:            200000,
		ContextWindowTokens:   88512,
		ContextTokens:         42000,
		ContextBudgetTokens:   160000,
		ContextSystemTokens:   900,
		ContextHistoryTokens:  1200,
		ContextResponseTokens: 16384,
		ContextToolTokens:     512,
		ContextReasons:        []string{"conversation history only; workspace evidence auto-attach is off"},
	}
	payload := ContextPayloadFromStats(info)
	if payload.Identity() != "zai / glm-5.1" {
		t.Fatalf("payload identity = %q", payload.Identity())
	}
	if payload.WindowTokens != 88512 || payload.FreeTokens != 111488 {
		t.Fatalf("payload window/free = %d/%d", payload.WindowTokens, payload.FreeTokens)
	}
	if payload.EvidenceTokens != 42000 || payload.EvidenceBudgetTokens != 160000 {
		t.Fatalf("payload evidence = %d/%d", payload.EvidenceTokens, payload.EvidenceBudgetTokens)
	}
	if !payload.WorkspaceEvidenceOff {
		t.Fatal("payload should preserve workspace-evidence-off state")
	}
}

func TestRenderStatsPanelMatrixANSIWidthInvariant(t *testing.T) {
	info := StatsPanelInfo{
		Provider:              "zai-coding-plan",
		Model:                 "glm-5.1",
		Configured:            true,
		MaxContext:            200000,
		ContextWindowTokens:   88512,
		ContextTokens:         42000,
		ContextBudgetTokens:   160000,
		ContextResponseTokens: 16384,
		ContextToolTokens:     512,
		TodoTotal:             4,
		TodoDoing:             1,
		TodoPending:           2,
		TodoActive:            "Patch the stats panel without turning it into a dashboard dump",
	}
	for _, mode := range []StatsPanelMode{StatsPanelModeOverview, StatsPanelModeTodos, StatsPanelModeTasks, StatsPanelModeProviders} {
		for _, size := range []struct {
			width  int
			height int
		}{{38, 14}, {48, 22}, {64, 30}} {
			info.Mode = mode
			out := RenderStatsPanelSized(info, size.height, size.width)
			maxWidth := size.width + 2 // lipgloss width plus left/right border columns
			for _, line := range strings.Split(out, "\n") {
				if got := ansi.StringWidth(line); got > maxWidth {
					t.Fatalf("mode %s %dx%d rendered ANSI width %d > %d: %q\n%s", mode, size.width, size.height, got, maxWidth, line, out)
				}
			}
			if !strings.Contains(out, "ACTIVE") {
				t.Fatalf("mode %s %dx%d missing ACTIVE:\n%s", mode, size.width, size.height, out)
			}
			if size.height >= 22 && !strings.Contains(out, "BUDGET") {
				t.Fatalf("mode %s %dx%d missing BUDGET:\n%s", mode, size.width, size.height, out)
			}
		}
	}
}

func TestRenderStatsPanelShowsContextDiagnostics(t *testing.T) {
	info := StatsPanelInfo{
		ContextTokens:           3200,
		MaxContext:              128000,
		ContextTask:             "review",
		ContextFileCount:        4,
		ContextMaxFiles:         8,
		ContextBudgetTokens:     16000,
		ContextAvailableTokens:  120000,
		ContextMaxTokensPerFile: 1200,
		ContextCompression:      "standard",
		ContextTopFiles:         []string{"internal/context/manager.go"},
		ContextReasons:          []string{"task=review profile(total x1.20, files x1.10, per-file x1.00)"},
		ContextSystemTokens:     900,
		ContextHistoryTokens:    1200,
		ContextResponseTokens:   2048,
		ContextToolTokens:       512,
		ContextHistoryReserve:   24000,
	}
	out := RenderStatsPanelSized(info, 30, 56)
	for _, want := range []string{"BUDGET", "input 5.3k/128k", "reserve out 2.0k", "tools 512", "hist 24k", "/context for full budget"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected budget summary %q, got:\n%s", want, out)
		}
	}
	for _, banned := range []string{"files 4/8", "task review", "zip standard", "evidence budget free", "manager.go", "why:"} {
		if strings.Contains(out, banned) {
			t.Fatalf("stats panel should not dump context diagnostic %q, got:\n%s", banned, out)
		}
	}
}

func TestRenderStatsPanelShowsProviderContextSourceAndControls(t *testing.T) {
	info := StatsPanelInfo{
		Provider:              "zai-coding-plan",
		Model:                 "glm-5.1",
		Configured:            true,
		ContextProvider:       "zai-coding-plan",
		ContextModel:          "glm-5.1",
		ContextLimitSource:    "models.dev zai/glm-5.1",
		MaxContext:            131072,
		ContextWindowTokens:   1600,
		ContextSystemTokens:   900,
		ContextHistoryTokens:  300,
		ContextTokens:         400,
		ContextResponseTokens: 16384,
		ContextToolTokens:     512,
		ContextMessageCount:   4,
		ContextToolCallCount:  2,
	}
	out := RenderStatsPanelSized(info, 26, 58)
	for _, want := range []string{"limit models.dev", "input 1.6k/131.1k", "free 129.5k", "reserve out 16.4k", "tools 512", "/context for full budget"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected provider budget row %q, got:\n%s", want, out)
		}
	}
	// Provider/model identity belongs to the ACTIVE section; BUDGET must not
	// restate it (request: line removed, limit suffix trimmed).
	for _, banned := range []string{"request: zai-coding-plan / glm-5.1", "models.dev zai/glm-5.1", "messages 4", "tool calls 2", "/context messages", "/context drop <id>"} {
		if strings.Contains(out, banned) {
			t.Fatalf("stats panel should not show context manager detail %q, got:\n%s", banned, out)
		}
	}
}

func TestRenderStatsPanelShowsTokenLedger(t *testing.T) {
	info := StatsPanelInfo{
		Provider:               "openai",
		Model:                  "gpt-5.4",
		Configured:             true,
		MaxContext:             200000,
		ContextWindowTokens:    16000,
		Streaming:              true,
		LiveInputTokens:        12000,
		LiveOutputTokens:       800,
		LastInputTokens:        9000,
		LastOutputTokens:       1000,
		LastTotalTokens:        10000,
		SessionInputTokens:     25000,
		SessionOutputTokens:    4000,
		SessionTotalTokens:     29000,
		TranscriptInputTokens:  700,
		TranscriptOutputTokens: 300,
		ComposerTokens:         42,
		CostPer1kTokens:        0.002,
	}
	out := RenderStatsPanelSized(info, 44, 58)
	for _, want := range []string{"ACTIVE", "NEXT", "BUDGET", "streaming", "input 16k/200k", "free 184k", "/context for full budget"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected compact token/budget signal %q, got:\n%s", want, out)
		}
	}
	for _, banned := range []string{"TOKENS", "last input 9k", "session total 29k", "composer 42", "cost $0.058"} {
		if strings.Contains(out, banned) {
			t.Fatalf("stats panel should not show token ledger detail %q, got:\n%s", banned, out)
		}
	}
}

func TestRenderStatsPanelShowsConversationOnlyContext(t *testing.T) {
	info := StatsPanelInfo{
		ContextWindowTokens:  2400,
		MaxContext:           128000,
		ContextBudgetTokens:  16000,
		ContextMaxFiles:      50,
		ContextReasons:       []string{"conversation history only; workspace evidence auto-attach is off"},
		ContextSystemTokens:  900,
		ContextHistoryTokens: 1500,
	}
	out := RenderStatsPanelSized(info, 30, 56)
	for _, want := range []string{"workspace evidence off", "/context for full budget"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected conversation-only budget summary %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "files 0/50") {
		t.Fatalf("conversation-only mode should not look like file context, got:\n%s", out)
	}
}

func TestRenderStatsPanelShowsOrchestrationMap(t *testing.T) {
	info := StatsPanelInfo{
		Mode:            StatsPanelModeTasks,
		Provider:        "openai",
		Model:           "gpt-5.4",
		Configured:      true,
		TodoTotal:       4,
		TodoDone:        1,
		TodoDoing:       1,
		PlanSubtasks:    3,
		PlanParallel:    true,
		DriveDone:       2,
		DriveTotal:      4,
		ActiveSubagents: 1,
		SubagentLimit:   3,
	}
	out := RenderStatsPanelSized(info, 42, 58)
	for _, want := range []string{
		"ACTIVE",
		"NEXT",
		"BUDGET",
		"plan 3 | parallel",
		"drive | 2/4 done",
		"run planned subtasks",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tasks operator summary missing %q, got:\n%s", want, out)
		}
	}
	for _, banned := range []string{"ORCHESTRATION MAP", "shared checklist", "split/graph", "workflow:"} {
		if strings.Contains(out, banned) {
			t.Fatalf("stats panel should not show orchestration dump %q, got:\n%s", banned, out)
		}
	}
}

func TestRenderStatsPanelFocusedModesShowNextActions(t *testing.T) {
	todos := RenderStatsPanelSized(StatsPanelInfo{
		Mode:       StatsPanelModeTodos,
		Provider:   "openai",
		Model:      "gpt-5.4",
		Configured: true,
		TodoTotal:  3,
		TodoDoing:  1,
		TodoActive: "patch status panel",
	}, 32, 58)
	for _, want := range []string{"NEXT", "finish active todo", "todo_write"} {
		if !strings.Contains(todos, want) {
			t.Fatalf("todos next actions missing %q, got:\n%s", want, todos)
		}
	}

	tasks := RenderStatsPanelSized(StatsPanelInfo{
		Mode:          StatsPanelModeTasks,
		Provider:      "openai",
		Model:         "gpt-5.4",
		Configured:    true,
		PlanSubtasks:  4,
		PlanParallel:  true,
		DriveRunID:    "drv-1",
		DriveDone:     1,
		DriveTotal:    4,
		TaskTreeLines: []string{"[running] wire next actions"},
	}, 36, 58)
	for _, want := range []string{"NEXT", "/tasks tree", "/drive active"} {
		if !strings.Contains(tasks, want) {
			t.Fatalf("tasks next actions missing %q, got:\n%s", want, tasks)
		}
	}

}

func TestFormatSessionDuration_Zero(t *testing.T) {
	out := formatSessionDuration(0)
	if out != "0s" {
		t.Fatalf("formatSessionDuration(0) = %q, want 0s", out)
	}
}

func TestFormatSessionDuration_Seconds(t *testing.T) {
	out := formatSessionDuration(45 * time.Second)
	if out != "45s" {
		t.Fatalf("formatSessionDuration(45s) = %q, want 45s", out)
	}
}

func TestFormatSessionDuration_Minutes(t *testing.T) {
	out := formatSessionDuration(2*time.Minute + 30*time.Second)
	if out != "2m 30s" {
		t.Fatalf("formatSessionDuration(2m30s) = %q, want 2m 30s", out)
	}
}

func TestFormatSessionDuration_Hours(t *testing.T) {
	out := formatSessionDuration(1*time.Hour + 15*time.Minute)
	if out != "1h 15m" {
		t.Fatalf("formatSessionDuration(1h15m) = %q, want 1h 15m", out)
	}
}

// --- tool_chips.go tests ---

func TestRenderInlineToolChips_Empty(t *testing.T) {
	out := RenderInlineToolChips(nil, 80)
	if out != "" {
		t.Fatalf("RenderInlineToolChips(nil) = %q, want empty", out)
	}
	out = RenderInlineToolChips([]ToolChip{}, 80)
	if out != "" {
		t.Fatalf("RenderInlineToolChips([]) = %q, want empty", out)
	}
}

func TestRenderInlineToolChips_Single(t *testing.T) {
	chips := []ToolChip{{Name: "read_file", Status: "ok"}}
	out := RenderInlineToolChips(chips, 80)
	if out == "" {
		t.Fatal("RenderInlineToolChips returned empty for single chip")
	}
}

func TestRenderInlineToolChips_Narrow(t *testing.T) {
	chips := []ToolChip{{Name: "test", Status: "ok"}}
	out := RenderInlineToolChips(chips, 5)
	if out == "" {
		t.Fatal("RenderInlineToolChips returned empty for narrow width")
	}
}

func TestRenderInlineToolChipsSummary_Empty(t *testing.T) {
	out := RenderInlineToolChipsSummary(nil, 80)
	if out != "" {
		t.Fatalf("RenderInlineToolChipsSummary(nil) = %q, want empty", out)
	}
	out = RenderInlineToolChipsSummary([]ToolChip{}, 80)
	if out != "" {
		t.Fatalf("RenderInlineToolChipsSummary([]) = %q, want empty", out)
	}
}

func TestRenderInlineToolChipsSummary_Single(t *testing.T) {
	chips := []ToolChip{{Name: "read_file", Status: "ok"}}
	out := RenderInlineToolChipsSummary(chips, 80)
	if out == "" {
		t.Fatal("RenderInlineToolChipsSummary returned empty for single chip")
	}
}

func TestRenderInlineToolChipsSummary_Multiple(t *testing.T) {
	chips := []ToolChip{
		{Name: "read_file", Status: "ok", DurationMs: 100},
		{Name: "edit_file", Status: "failed"},
		{Name: "delegate_task", Status: "running"},
	}
	out := RenderInlineToolChipsSummary(chips, 100)
	if out == "" {
		t.Fatal("RenderInlineToolChipsSummary returned empty for multiple chips")
	}
}

func TestRenderInlineToolChipsSummary_Narrow(t *testing.T) {
	chips := []ToolChip{{Name: "test", Status: "ok"}}
	out := RenderInlineToolChipsSummary(chips, 5)
	if out == "" {
		t.Fatal("RenderInlineToolChipsSummary returned empty for narrow width")
	}
}

func TestPlural(t *testing.T) {
	if plural(1) != "" {
		t.Errorf("plural(1) = %q, want empty", plural(1))
	}
	if plural(0) != "s" {
		t.Errorf("plural(0) = %q, want s", plural(0))
	}
	if plural(2) != "s" {
		t.Errorf("plural(2) = %q, want s", plural(2))
	}
}

func TestChipIconStyle(t *testing.T) {
	tests := []struct {
		status   string
		wantIcon string
	}{
		{"ok", "·"},
		{"success", "·"},
		{"done", "·"},
		{"failed", "×"},
		{"error", "×"},
		{"fail", "×"},
		{"running", "◌"},
		{"pending", "◌"},
		{"compact", "↕"},
		{"compacted", "↕"},
		{"budget", "!"},
		{"handoff", "→"},
		{"subagent-running", "◈"},
		{"subagent-ok", "◈"},
		{"subagent-failed", "◈"},
		{"unknown", "·"},
	}
	for _, tc := range tests {
		icon, _ := ChipIconStyle(tc.status)
		if icon != tc.wantIcon {
			t.Errorf("chipIconStyle(%q) icon = %q, want %q", tc.status, icon, tc.wantIcon)
		}
	}
}
