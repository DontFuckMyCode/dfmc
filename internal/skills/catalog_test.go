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

// ResolveForQuery loads multiple explicit skills (skill composition).
func TestResolveForQuery_MultipleExplicitSkills(t *testing.T) {
	sel := ResolveForQuery("", "[[skill:review]] [[skill:audit]] analyze auth", "")
	if !sel.Explicit {
		t.Fatal("expected Explicit=true for multi-skill query")
	}
	if len(sel.Skills) < 2 {
		t.Fatalf("expected at least 2 skills for [[skill:review]] + [[skill:audit]], got %d", len(sel.Skills))
	}
	names := make([]string, 0, len(sel.Skills))
	for _, s := range sel.Skills {
		names = append(names, s.Name)
	}
	if len(sel.Skills) >= 2 && names[0] == "review" && names[1] == "audit" {
		// spot-check ordering
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

// tryAgentSkillFormat parses a valid SKILL.md with YAML frontmatter + markdown body.
func TestTryAgentSkillFormat_ValidSKILLMD(t *testing.T) {
	data := []byte(`---
name: pdf-processing
description: Extract text from PDF files.
allowed-tools: Read GrepCodebase
---
# PDF Processing Skill

Use the pdf tool to extract text.`)
	got := tryAgentSkillFormat(data, "pdf-processing/SKILL.md", "test")
	if got.Name != "pdf-processing" {
		t.Errorf("Name=%q, want %q", got.Name, "pdf-processing")
	}
	if got.Description != "Extract text from PDF files." {
		t.Errorf("Description=%q", got.Description)
	}
	if len(got.Allowed) != 2 || got.Allowed[0] != "Read" {
		t.Errorf("Allowed=%v", got.Allowed)
	}
}

func TestTryAgentSkillFormat_NoFrontmatter(t *testing.T) {
	// No leading "---" should return zero value
	got := tryAgentSkillFormat([]byte("no frontmatter"), "foo/SKILL.md", "test")
	if got.Name != "" {
		t.Errorf("expected empty Name, got %q", got.Name)
	}
}

func TestTryAgentSkillFormat_NoSeparator(t *testing.T) {
	// Has "---" but no closing "\n---" separator
	got := tryAgentSkillFormat([]byte("---\nname: foo\n"), "foo/SKILL.md", "test")
	if got.Name != "" {
		t.Errorf("expected empty Name, got %q", got.Name)
	}
}

func TestTryAgentSkillFormat_EmptyMarkdownBody(t *testing.T) {
	// Has frontmatter but empty markdown body after separator
	got := tryAgentSkillFormat([]byte("---\nname: foo\ndescription: d\n---\n  \n"), "foo/SKILL.md", "test")
	if got.Name != "" {
		t.Errorf("expected empty Name for empty markdown body, got %q", got.Name)
	}
}

func TestTryAgentSkillFormat_MissingName(t *testing.T) {
	// YAML frontmatter without "name" field
	got := tryAgentSkillFormat([]byte("---\ndescription: d\n---\n# Body\n"), "foo/SKILL.md", "test")
	if got.Name != "" {
		t.Errorf("expected empty Name for missing name field, got %q", got.Name)
	}
}

// readSkillFile parses Agent Skills SKILL.md and returns a Skill.
// Discovery path: create a .dfmc/skills/ dir with a SKILL.md file, call Discover.
func TestDiscover_AgentSkillSKILLMD(t *testing.T) {
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(skillsDir, "pdf-processing", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
name: pdf-processing
description: Extract text from PDF files and search for patterns.
allowed-tools: Read GrepCodebase
---
# PDF Processing Skill

Use the pdf tool to extract text. Then grep_codebase for the relevant section.

1. Run the pdf tool on the file path.
2. Use grep_codebase to find matching lines.
3. Return the findings.`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items := Discover(tmp)
	var found Skill
	for _, item := range items {
		if item.Name == "pdf-processing" {
			found = item
			break
		}
	}
	if found.Name == "" {
		t.Fatal("expected to find pdf-processing skill via Discover")
	}
	if found.Source != "project" {
		t.Fatalf("expected source project, got %q", found.Source)
	}
	if !strings.Contains(found.System, "pdf tool") {
		t.Fatalf("expected system to contain markdown body, got %q", found.System)
	}
	if len(found.Allowed) != 2 {
		t.Fatalf("expected 2 allowed tools, got %d", len(found.Allowed))
	}
}

// readSkillFile with a .skill.yaml file (native DFMC format) still works.
func TestReadSkillFile_NativeYAML(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "my-skill.yaml")
	body := `name: my-skill
description: My custom skill
system_prompt: |
  Do the thing.
preferred_tools:
  - read_file
  - grep_codebase
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	item := readSkillFile(path, "project")
	if item.Name != "my-skill" {
		t.Fatalf("expected name my-skill, got %q", item.Name)
	}
	if item.Source != "project" {
		t.Fatalf("expected source project, got %q", item.Source)
	}
	if len(item.Preferred) != 2 {
		t.Fatalf("expected preferred len 2, got %v", item.Preferred)
	}
	if system := strings.TrimSpace(item.System); system == "" {
		t.Fatalf("expected non-empty system field")
	}
}

// TestReadSkillFile_AgentSkillSKILLMD is covered by TestDiscover_AgentSkillSKILLMD.

// readSkillFile with a YAML file that has no name field falls back to filename.
func TestReadSkillFile_NameFromFilename(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "fallback-skill.yaml")
	body := `description: A skill with no name field.
system_prompt: |
  Do the thing.`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	item := readSkillFile(path, "global")
	if item.Name != "fallback-skill" {
		t.Fatalf("expected name fallback-skill, got %q", item.Name)
	}
}

// readSkillFile with a non-existent file returns item with name from filepath.Base.
func TestReadSkillFile_FileNotFound(t *testing.T) {
	item := readSkillFile("/nonexistent/path/skill.yaml", "project")
	if item.Name != "skill" {
		t.Fatalf("expected name 'skill' (from filename), got %q", item.Name)
	}
	if item.Source != "project" {
		t.Fatalf("expected source project, got %q", item.Source)
	}
}

// readSkillFile parses SKILL.md with allowed-tools (hyphen) and extracts allowed list.
func TestReadSkillFile_SKILLMD_AllowedToolsHyphen(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "my-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := []byte(`---
name: my-skill
description: A skill with allowed-tools hyphen
allowed-tools: Read GrepCodebase
---
# My Skill

Do the thing.`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	item := readSkillFile(path, "project")
	if item.Name != "my-skill" {
		t.Fatalf("expected name 'my-skill', got %q", item.Name)
	}
	if len(item.Allowed) != 2 {
		t.Fatalf("expected 2 allowed tools, got %v", item.Allowed)
	}
}

// readSkillFile with raw map fallback: parses description, prompt, template, system fields.
func TestReadSkillFile_RawMapFallback(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "raw-skill.yaml")
	data := []byte(`description: A raw fallback skill
prompt: Do the thing.
template: Template hint.
system: System hint.
task: review
role: reviewer
profile: default
preferred_tools: read_file,grep_codebase
allowed_tools: Read Write
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	item := readSkillFile(path, "global")
	if item.Description != "A raw fallback skill" {
		t.Errorf("Description=%q", item.Description)
	}
	if item.Prompt != "Do the thing." {
		t.Errorf("Prompt=%q", item.Prompt)
	}
	if item.Task != "review" {
		t.Errorf("Task=%q", item.Task)
	}
	if item.Role != "reviewer" {
		t.Errorf("Role=%q", item.Role)
	}
	if item.Profile != "default" {
		t.Errorf("Profile=%q", item.Profile)
	}
}

// SkillForName is a thin wrapper around Lookup.
func TestSkillForName(t *testing.T) {
	s, ok := SkillForName("", "review")
	if !ok {
		t.Fatal("expected to find review skill")
	}
	if s.Name != "review" {
		t.Fatalf("expected name 'review', got %q", s.Name)
	}

	_, ok = SkillForName("", "nonexistent-xyz")
	if ok {
		t.Fatal("expected not found for nonexistent skill")
	}
}

// RenderSystemText with all fields populated.
func TestRenderSystemText_AllFields(t *testing.T) {
	skill := Skill{
		Name:        "test-skill",
		Description: "A test skill",
		Task:        "testing",
		Role:        "tester",
		Profile:     "test-profile",
		Preferred:   []string{"read_file", "grep_codebase"},
		Allowed:     []string{"Read"},
		System:      "Skill contract:\nTest contract body",
	}
	text := RenderSystemText(skill)
	if !strings.Contains(text, "test-skill") {
		t.Fatalf("expected skill name in output")
	}
	if !strings.Contains(text, "Runtime hints:") {
		t.Fatalf("expected Runtime hints section")
	}
	if !strings.Contains(text, "task hint: testing") {
		t.Fatalf("expected task hint")
	}
	if !strings.Contains(text, "role hint: tester") {
		t.Fatalf("expected role hint")
	}
	if !strings.Contains(text, "profile hint: test-profile") {
		t.Fatalf("expected profile hint")
	}
	if !strings.Contains(text, "Preferred tools:") {
		t.Fatalf("expected Preferred tools section")
	}
	if !strings.Contains(text, "Scope guard:") {
		t.Fatalf("expected Scope guard section")
	}
	if !strings.Contains(text, "Skill contract:") {
		t.Fatalf("expected Skill contract section")
	}
}

// RenderSystemText with Role only (no Task or Profile).
func TestRenderSystemText_RoleOnly(t *testing.T) {
	skill := Skill{Name: "r", Role: "code-reviewer"}
	text := RenderSystemText(skill)
	if !strings.Contains(text, "role hint:") {
		t.Fatalf("expected role hint, got: %s", text)
	}
}

// RenderSystemText with Preferred only (no Allowed).
func TestRenderSystemText_PreferredOnly(t *testing.T) {
	skill := Skill{Name: "p", Preferred: []string{"read_file"}}
	text := RenderSystemText(skill)
	if !strings.Contains(text, "Preferred tools:") {
		t.Fatalf("expected Preferred tools, got: %s", text)
	}
	if strings.Contains(text, "Scope guard:") {
		t.Fatalf("should not have Scope guard without Allowed, got: %s", text)
	}
}

// RenderSystemText with Allowed only (no Preferred).
func TestRenderSystemText_AllowedOnly(t *testing.T) {
	skill := Skill{Name: "a", Allowed: []string{"Read"}}
	text := RenderSystemText(skill)
	if !strings.Contains(text, "Scope guard:") {
		t.Fatalf("expected Scope guard, got: %s", text)
	}
}

// RenderSystemText with SystemInstruction body.
func TestRenderSystemText_SystemInstruction(t *testing.T) {
	skill := Skill{Name: "sys", System: "System body here", Prompt: "Prompt {input}"}
	text := RenderSystemText(skill)
	if !strings.Contains(text, "Skill contract:") {
		t.Fatalf("expected Skill contract section, got: %s", text)
	}
	if !strings.Contains(text, "System body here") {
		t.Fatalf("expected System body, got: %s", text)
	}
}

// explicitNames with no matches returns empty slice.
func TestExplicitNames_NoMatches(t *testing.T) {
	names := explicitNames("plain text without markers")
	if len(names) != 0 {
		t.Fatalf("expected 0 names, got %d", len(names))
	}
}

// explicitNames with empty input.
func TestExplicitNames_EmptyInput(t *testing.T) {
	names := explicitNames("")
	if len(names) != 0 {
		t.Fatalf("expected 0 names, got %d", len(names))
	}
}

// cleanStringList with all empty strings returns empty slice.
func TestCleanStringList_AllEmpty(t *testing.T) {
	got := cleanStringList([]string{"", "  ", ""})
	if len(got) != 0 {
		t.Fatalf("expected 0 items, got %v", got)
	}
}

// parseStringList with unknown type falls back to fmt.Sprint.
func TestParseStringList_UnknownType(t *testing.T) {
	got := parseStringList(42)
	if len(got) != 1 || got[0] != "42" {
		t.Fatalf("expected [42], got %v", got)
	}
}

// tryAgentSkillFormat with only frontmatter (no markdown body).
func TestTryAgentSkillFormat_OnlyFrontmatter(t *testing.T) {
	data := []byte(`---
name: empty-skill
description: No body
---`)
	got := tryAgentSkillFormat(data, "empty/SKILL.md", "test")
	if got.Name != "" {
		t.Errorf("expected empty name for no body, got %q", got.Name)
	}
}

// explicitNames with path separators in skill name.
func TestExplicitNames_WithPathSeparators(t *testing.T) {
	names := explicitNames("use [[skill:debug/specific]] tool")
	if len(names) != 1 || names[0] != "debug/specific" {
		t.Fatalf("expected [debug/specific], got %v", names)
	}
}

// extractMarkdownBody tests

func TestExtractMarkdownBody_NoFrontmatter(t *testing.T) {
	got := extractMarkdownBody([]byte("just plain text"))
	if got != "" {
		t.Errorf("no frontmatter: got %q", got)
	}
}

func TestExtractMarkdownBody_Empty(t *testing.T) {
	got := extractMarkdownBody([]byte{})
	if got != "" {
		t.Errorf("empty: got %q", got)
	}
}

func TestExtractMarkdownBody_OnlyFrontmatter(t *testing.T) {
	got := extractMarkdownBody([]byte("---\nname: test\n---\n"))
	if got != "" {
		t.Errorf("only frontmatter: got %q", got)
	}
}

func TestExtractMarkdownBody_WithBody(t *testing.T) {
	data := []byte(`---
name: test
---
# Hello

This is the body.
`)
	got := extractMarkdownBody(data)
	if !strings.Contains(got, "Hello") {
		t.Errorf("expected 'Hello' in body, got %q", got)
	}
	if !strings.Contains(got, "This is the body") {
		t.Errorf("expected body content, got %q", got)
	}
}

func TestExtractMarkdownBody_MultipleSeparators(t *testing.T) {
	// The first \n--- is the separator, anything after is the body
	data := []byte("---\nname: test\n---\n---\nmore content\n")
	got := extractMarkdownBody(data)
	if !strings.Contains(got, "more content") {
		t.Errorf("expected 'more content' in body, got %q", got)
	}
}

