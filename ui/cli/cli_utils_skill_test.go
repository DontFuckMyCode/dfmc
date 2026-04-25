package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// ---------------------------------------------------------------------------
// cli_utils.go — 0% coverage functions
// ---------------------------------------------------------------------------

func TestParseTier(t *testing.T) {
	tests := []struct {
		input    string
		expected types.MemoryTier
	}{
		{"semantic", types.MemorySemantic},
		{"SEMANTIC", types.MemorySemantic},
		{"  semantic  ", types.MemorySemantic},
		{"episodic", types.MemoryEpisodic},
		{" Episodic ", types.MemoryEpisodic},
		{"unknown", types.MemoryEpisodic},
		{"", types.MemoryEpisodic},
		{"working", types.MemoryEpisodic},
	}
	for _, tc := range tests {
		got := parseTier(tc.input)
		if got != tc.expected {
			t.Errorf("parseTier(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestTruncateLine(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello world", 0, "hello world"},
		{"hello world", -1, "hello world"},
		{"hello world", 5, "hello..."},
		{"hello world", 20, "hello world"},
		{"hello world", 11, "hello world"},
		{"  hello\nworld  ", 8, "hello wo..."},
		{"", 5, ""},
		{"short", 10, "short"},
	}
	for _, tc := range tests {
		got := truncateLine(tc.input, tc.max)
		if got != tc.expected {
			t.Errorf("truncateLine(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.expected)
		}
	}
}

func TestBrowserCommandForOSTable(t *testing.T) {
	tests := []struct {
		goos    string
		wantOK  bool
		wantCmd string
	}{
		{"windows", true, "cmd"},
		{"darwin", true, "open"},
		{"linux", true, "xdg-open"},
		{"freebsd", false, ""},
		{"", false, ""},
	}
	for _, tc := range tests {
		name, args, ok := browserCommandForOS(tc.goos, "http://example.com")
		if ok != tc.wantOK {
			t.Errorf("browserCommandForOS(%q) ok=%v, want %v", tc.goos, ok, tc.wantOK)
		}
		if tc.wantOK && name != tc.wantCmd {
			t.Errorf("browserCommandForOS(%q) name=%q, want %q", tc.goos, name, tc.wantCmd)
		}
		if tc.wantOK && len(args) == 0 {
			t.Errorf("browserCommandForOS(%q) returned ok=true but empty args", tc.goos)
		}
	}
}

func TestTryOpenBrowser_SupportedPlatform(t *testing.T) {
	// tryOpenBrowser calls browserCommandForOS(runtime.GOOS, ...) and
	// executes the browser command. On supported platforms it returns
	// whatever cmd.Start() returns (nil on success). The actual browser
	// opening is a side-effect we don't assert — just verify the call
	// does not panic.
	_ = tryOpenBrowser("http://example.com")
}

func TestExecutableSize(t *testing.T) {
	size := executableSize()
	if size < 0 {
		t.Errorf("executableSize() = %d, must not be negative", size)
	}
}

// ---------------------------------------------------------------------------
// cli_skill.go — skill install / export (no engine required)
// ---------------------------------------------------------------------------

func TestRunSkillInstall_NoArgs(t *testing.T) {
	got := runSkillInstall([]string{})
	if got != 2 {
		t.Errorf("runSkillInstall no args: got %d, want 2", got)
	}
}

func TestRunSkillInstall_EmptyPath(t *testing.T) {
	got := runSkillInstall([]string{""})
	if got != 2 {
		t.Errorf("runSkillInstall empty path: got %d, want 2", got)
	}
}

func TestRunSkillInstall_NonExistentFile(t *testing.T) {
	got := runSkillInstall([]string{"/nonexistent/path/skill.yaml"})
	if got != 1 {
		t.Errorf("runSkillInstall nonexistent file: got %d, want 1", got)
	}
}

func TestRunSkillInstall_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	badFile := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badFile, []byte("not: [yaml: at: all"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	got := runSkillInstall([]string{badFile})
	if got != 1 {
		t.Errorf("runSkillInstall invalid YAML: got %d, want 1", got)
	}
}

func TestRunSkillInstall_MissingNameField(t *testing.T) {
	tmp := t.TempDir()
	badFile := filepath.Join(tmp, "noname.yaml")
	if err := os.WriteFile(badFile, []byte("description: no name field\nprompt: test"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	got := runSkillInstall([]string{badFile})
	if got != 1 {
		t.Errorf("runSkillInstall missing name: got %d, want 1", got)
	}
}

func TestRunSkillInstall_EmptyNameField(t *testing.T) {
	tmp := t.TempDir()
	badFile := filepath.Join(tmp, "emptyname.yaml")
	if err := os.WriteFile(badFile, []byte("name: \"\"\nprompt: test"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	got := runSkillInstall([]string{badFile})
	if got != 1 {
		t.Errorf("runSkillInstall empty name: got %d, want 1", got)
	}
}

func TestRunSkillInstall_Success(t *testing.T) {
	// Use a per-test project root to avoid polluting the global ~/.dfmc/skills
	// and avoid cross-test contamination.
	projRoot := t.TempDir()
	t.Setenv("DFMC_PROJECT_ROOT", projRoot)

	skillFile := filepath.Join(t.TempDir(), "myskill_"+t.Name()+".yaml")
	content := "name: my-test-skill-" + t.Name() + "\ndescription: a test\nprompt: test prompt\n"
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	got := runSkillInstall([]string{skillFile})
	if got != 0 {
		t.Errorf("runSkillInstall success: got %d, want 0", got)
	}
}

func TestRunSkillInstall_DuplicateSkill(t *testing.T) {
	projRoot := t.TempDir()
	t.Setenv("DFMC_PROJECT_ROOT", projRoot)

	skillFile := filepath.Join(t.TempDir(), "dups_"+t.Name()+".yaml")
	content := "name: dup-test-skill-" + t.Name() + "\ndescription: dup\nprompt: test\n"
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if got := runSkillInstall([]string{skillFile}); got != 0 {
		t.Fatalf("first install failed: %d", got)
	}
	if got := runSkillInstall([]string{skillFile}); got != 1 {
		t.Errorf("runSkillInstall duplicate: got %d, want 1", got)
	}
}

func TestRunSkillExport_NoArgs(t *testing.T) {
	got := runSkillExport([]string{})
	if got != 2 {
		t.Errorf("runSkillExport no args: got %d, want 2", got)
	}
}

func TestRunSkillExport_NonExistentSkill(t *testing.T) {
	got := runSkillExport([]string{"nonexistent-skill-xyz-123"})
	if got != 1 {
		t.Errorf("runSkillExport nonexistent: got %d, want 1", got)
	}
}

func TestRunSkillExport_BuiltinSkill(t *testing.T) {
	got := runSkillExport([]string{"review"})
	if got != 0 {
		t.Errorf("runSkillExport builtin review: got %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// cli_update.go — 0% coverage functions
// ---------------------------------------------------------------------------

func TestSuggestUpgradeCommand(t *testing.T) {
	orig := runtimeGOOS
	t.Cleanup(func() { runtimeGOOS = orig })

	for _, goos := range []string{"darwin", "linux", "windows", "freebsd"} {
		runtimeGOOS = func() string { return goos }
		cmd := suggestUpgradeCommand()
		if cmd == "" {
			t.Errorf("suggestUpgradeCommand(%s) returned empty string", goos)
		}
	}
}

func TestPrintUpdateReport_Upgradable(t *testing.T) {
	r := updateReport{
		Current:    "v1.0.0",
		Channel:    "stable",
		Latest:     "v2.0.0",
		Published:  "2024-01-01",
		URL:        "https://github.com/example/releases/tag/v2.0.0",
		Upgradable: true,
		UpToDate:   false,
		Command:    "dfmc update",
		Notes:      "Bug fixes and new features",
	}
	printUpdateReport(r)
}

func TestPrintUpdateReport_UpToDate(t *testing.T) {
	r := updateReport{
		Current:    "v2.0.0",
		Channel:    "stable",
		Latest:     "v2.0.0",
		Upgradable: false,
		UpToDate:   true,
	}
	printUpdateReport(r)
}

func TestPrintUpdateReport_NoNotes(t *testing.T) {
	r := updateReport{
		Current:    "v1.0.0",
		Channel:    "stable",
		Latest:     "v2.0.0",
		Upgradable: true,
		Notes:      "",
	}
	printUpdateReport(r)
}

func TestPrintUpdateReport_NoPublished(t *testing.T) {
	r := updateReport{
		Current:    "v1.0.0",
		Channel:    "stable",
		Latest:     "v2.0.0",
		Upgradable: false,
		UpToDate:   true,
	}
	printUpdateReport(r)
}
