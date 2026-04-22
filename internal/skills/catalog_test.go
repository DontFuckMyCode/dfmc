package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lookup returns false for empty name.
func TestLookup_EmptyName(t *testing.T) {
	_, ok := Lookup("", "")
	if ok {
		t.Fatal("expected false for empty name")
	}
}

// Lookup returns false for nonexistent skill.
func TestLookup_NotFound(t *testing.T) {
	_, ok := Lookup("", "nonexistent_skill_xyz")
	if ok {
		t.Fatal("expected false for nonexistent skill")
	}
}

// Lookup finds builtin skill case-insensitively.
func TestLookup_BuiltinCaseInsensitive(t *testing.T) {
	s, ok := Lookup("", "REVIEW")
	if !ok {
		t.Fatal("expected to find REIVEW (uppercase)")
	}
	if s.Name != "review" {
		t.Fatalf("expected name 'review', got %q", s.Name)
	}
	if !s.Builtin {
		t.Fatalf("expected builtin=true")
	}
}

// Discover returns a non-empty catalog even with no project root.
func TestDiscover_NonEmptyCatalog(t *testing.T) {
	items := Discover("")
	if len(items) == 0 {
		t.Fatal("expected non-empty builtin catalog")
	}
	// All items should have non-empty names.
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			t.Fatal("found skill with empty name")
		}
	}
}

// Discover catalog is sorted case-insensitively by name.
func TestDiscover_SortedByName(t *testing.T) {
	items := Discover("")
	for i := 1; i < len(items); i++ {
		prev := strings.ToLower(items[i-1].Name)
		curr := strings.ToLower(items[i].Name)
		if curr < prev {
			t.Fatalf("catalog not sorted: %q before %q", prev, curr)
		}
	}
}

// Discover project skills with same name as builtin are skipped (builtin takes precedence).
func TestDiscover_ProjectSkillSameNameAsBuiltin(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(skillsDir, "review.yaml")
	body := `name: review
description: Custom project review
system_prompt: |
  Project custom review prompt.
task: review
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items := Discover(root)
	var found Skill
	for _, item := range items {
		if strings.EqualFold(item.Name, "review") {
			found = item
			break
		}
	}
	// Builtin takes precedence; project skill is skipped as duplicate.
	if found.Source != "builtin" {
		t.Fatalf("expected builtin source (project skill skipped as duplicate), got %q", found.Source)
	}
}

// DecorateQuery with empty name returns input unchanged.
func TestDecorateQuery_EmptyName(t *testing.T) {
	got := DecorateQuery("", "user input")
	if got != "user input" {
		t.Fatalf("expected unchanged input, got %q", got)
	}
}

// DecorateQuery with empty input returns skill marker only.
func TestDecorateQuery_EmptyInput(t *testing.T) {
	got := DecorateQuery("debug", "")
	expected := "[[skill:debug]]"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// DecorateQuery with both args returns wrapped form.
func TestDecorateQuery_BothArgs(t *testing.T) {
	got := DecorateQuery("debug", "fix auth bug")
	expected := "[[skill:debug]]\nfix auth bug"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// StripMarkers removes all skill markers.
func TestStripMarkers_RemovesAllMarkers(t *testing.T) {
	query := "[[skill:debug]]\n[[skill:review]]\nfix auth bug"
	got := StripMarkers(query)
	if strings.Contains(got, "skill:") {
		t.Fatalf("skill markers should be removed, got %q", got)
	}
	if !strings.Contains(got, "fix auth bug") {
		t.Fatalf("user content should be preserved, got %q", got)
	}
}

// StripMarkers collapses multiple blank lines.
func TestStripMarkers_CollapsesBlankLines(t *testing.T) {
	query := "[[skill:debug]]\n\n\n\nsome text"
	got := StripMarkers(query)
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("multiple blank lines should collapse, got %q", got)
	}
}

// StripMarkers handles query with no markers.
func TestStripMarkers_NoMarkers(t *testing.T) {
	query := "plain user query"
	got := StripMarkers(query)
	if got != query {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

// Selection.Primary returns false when no skills selected.
func TestSelection_Primary_Empty(t *testing.T) {
	s := Selection{}
	_, ok := s.Primary()
	if ok {
		t.Fatal("expected false for empty selection")
	}
}

// Selection.Primary returns first skill.
func TestSelection_Primary_ReturnsFirst(t *testing.T) {
	s := Selection{
		Skills: []Skill{
			{Name: "first"},
			{Name: "second"},
		},
	}
	skill, ok := s.Primary()
	if !ok {
		t.Fatal("expected true")
	}
	if skill.Name != "first" {
		t.Fatalf("expected first skill, got %q", skill.Name)
	}
}

// explicitNames extracts skill names from markers.
func TestExplicitNames_ExtractsNames(t *testing.T) {
	query := "use [[skill:debug]] and [[skill:review]] to investigate"
	names := explicitNames(query)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "debug" || names[1] != "review" {
		t.Fatalf("unexpected names: %v", names)
	}
}

// explicitNames deduplicates.
func TestExplicitNames_Deduplicates(t *testing.T) {
	query := "[[skill:debug]] [[skill:debug]] [[skill:review]]"
	names := explicitNames(query)
	if len(names) != 2 {
		t.Fatalf("expected 2 (deduplicated), got %d", len(names))
	}
}

// skillForTask maps task strings to skill names.
func TestSkillForTask_MapsCorrectly(t *testing.T) {
	cases := []struct {
		task  string
		want  string
		ok    bool
	}{
		{"review", "review", true},
		{"REFACTOR", "refactor", true},
		{"debug", "debug", true},
		{"test", "test", true},
		{"doc", "doc", true},
		{"security", "audit", true},
		{"planning", "onboard", true},
		{"unknown", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got := skillForTask(tc.task)
		if tc.ok && got != tc.want {
			t.Errorf("skillForTask(%q): got %q want %q", tc.task, got, tc.want)
		}
		if !tc.ok && got != "" {
			t.Errorf("skillForTask(%q): got %q want empty", tc.task, got)
		}
	}
}

// ResolveForQuery with empty query and no detected task returns empty selection.
func TestResolveForQuery_EmptyQueryEmptyTask(t *testing.T) {
	sel := ResolveForQuery("", "", "")
	if len(sel.Skills) != 0 {
		t.Fatalf("expected empty selection, got %d skills", len(sel.Skills))
	}
}

// ResolveForQuery with empty query falls back to task mapping.
func TestResolveForQuery_EmptyQueryWithTaskFallback(t *testing.T) {
	sel := ResolveForQuery("", "", "review")
	if len(sel.Skills) != 1 {
		t.Fatalf("expected 1 skill (task fallback), got %d", len(sel.Skills))
	}
	if sel.Skills[0].Name != "review" {
		t.Fatalf("expected review skill, got %q", sel.Skills[0].Name)
	}
}

// ResolveForQuery explicit skill name resolves to that skill.
func TestResolveForQuery_ExplicitSkillName(t *testing.T) {
	sel := ResolveForQuery("", "use [[skill:debug]] to investigate", "")
	if !sel.Explicit {
		t.Fatal("expected Explicit=true")
	}
	if len(sel.Skills) == 0 {
		t.Fatal("expected at least one skill")
	}
	if sel.Skills[0].Name != "debug" {
		t.Fatalf("expected debug skill, got %q", sel.Skills[0].Name)
	}
}

// ResolveForQuery deduplicates duplicate markers.
func TestResolveForQuery_Deduplicates(t *testing.T) {
	sel := ResolveForQuery("", "[[skill:debug]] [[skill:debug]]", "")
	if len(sel.Skills) != 1 {
		t.Fatalf("expected 1 skill (deduplicated), got %d", len(sel.Skills))
	}
}

// RenderSystemText produces non-empty text.
func TestRenderSystemText_NonEmptySkill(t *testing.T) {
	skill := Skill{Name: "debug", Description: "debugging skill", Task: "debug"}
	text := RenderSystemText(skill)
	if text == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(text, "debug") {
		t.Fatalf("expected skill name in output, got %q", text)
	}
}

// RenderSystemText omits empty optional fields.
func TestRenderSystemText_OmitsEmptyFields(t *testing.T) {
	skill := Skill{Name: "test"}
	text := RenderSystemText(skill)
	// Should not contain "Runtime hints:" when no hints are set.
	if strings.Contains(text, "Runtime hints:") {
		t.Fatalf("should not contain Runtime hints when no hints set: %q", text)
	}
}

// Skill.SystemInstruction returns system field when non-empty.
func TestSystemInstruction_SystemField(t *testing.T) {
	skill := Skill{System: "system prompt text"}
	if text := skill.SystemInstruction(); text != "system prompt text" {
		t.Fatalf("expected system field, got %q", text)
	}
}

// Skill.SystemInstruction falls back to Prompt when system is empty.
func TestSystemInstruction_FallsBackToPrompt(t *testing.T) {
	skill := Skill{Prompt: "prompt with {input} placeholder", System: ""}
	text := skill.SystemInstruction()
	if strings.Contains(text, "{input}") {
		t.Fatalf("expected {input} placeholder removed, got %q", text)
	}
}

// parseStringList handles nil.
func TestParseStringList_Nil(t *testing.T) {
	got := parseStringList(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// parseStringList handles []string.
func TestParseStringList_StringSlice(t *testing.T) {
	got := parseStringList([]string{"go", "test"})
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
}

// parseStringList handles []any.
func TestParseStringList_AnySlice(t *testing.T) {
	got := parseStringList([]any{"go", 123, "test"})
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
}

// parseStringList handles comma-separated string.
func TestParseStringList_CommaString(t *testing.T) {
	got := parseStringList("go, test, vet")
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
}

// cleanStringList deduplicates and trims.
func TestCleanStringList_DeduplicatesAndTrims(t *testing.T) {
	got := cleanStringList([]string{" go ", "test", " go ", "VET", "test"})
	if len(got) != 3 {
		t.Fatalf("expected 3 (deduped+trimmed), got %d", len(got))
	}
}
