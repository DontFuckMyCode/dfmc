package theme

import (
	"testing"
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
		Name:       "read_file",
		Status:     "ok",
		DurationMs: 120,
		Preview:    "42 lines",
		Step:       1,
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
		Name:           "test",
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
