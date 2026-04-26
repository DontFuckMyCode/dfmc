package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestRunVersionText(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runVersion(eng, "1.2.3-test", []string{}, false); code != 0 {
		t.Fatalf("runVersion text exit=%d", code)
	}
}

func TestRunVersionJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if code := runVersion(eng, "1.2.3-test", []string{}, true); code != 0 {
			t.Fatalf("runVersion json exit=%d", code)
		}
	})
	if out == "" {
		t.Fatal("expected json output")
	}
	if !containsJSONKey(out, "version") {
		t.Fatalf("expected version in json output: %s", out)
	}
}

func TestRunVersion_JSONFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if code := runVersion(eng, "1.2.3-test", []string{"--json"}, false); code != 0 {
			t.Fatalf("runVersion --json flag exit=%d", code)
		}
	})
	if !containsJSONKey(out, "version") {
		t.Fatalf("expected json output from --json flag: %s", out)
	}
}

func TestRunVersion_ParseError(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runVersion(eng, "1.2.3", []string{"--invalid-flag"}, false); code != 2 {
		t.Fatalf("expected exit 2 for parse error, got %d", code)
	}
}

func TestRunInit_Success(t *testing.T) {
	proj := t.TempDir()
	if code := runInit(false, proj); code != 0 {
		t.Fatalf("runInit exit=%d", code)
	}
	cfgPath := filepath.Join(proj, ".dfmc", "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("expected config.yaml at %s", cfgPath)
	}
}

func TestRunInit_JSON(t *testing.T) {
	proj := t.TempDir()
	out := captureStdout(t, func() {
		if code := runInit(true, proj); code != 0 {
			t.Fatalf("runInit json exit=%d", code)
		}
	})
	if !containsJSONKey(out, "status") {
		t.Fatalf("expected json status in output: %s", out)
	}
}

func TestRunInit_CreatesKnowledgeAndConventions(t *testing.T) {
	proj := t.TempDir()
	if code := runInit(false, proj); code != 0 {
		t.Fatalf("runInit exit=%d", code)
	}
	for _, name := range []string{"knowledge.json", "conventions.json"} {
		path := filepath.Join(proj, ".dfmc", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Fatalf("expected %s to be created", name)
		}
	}
}

func TestRunInit_EmptyOverrideFallsBackToFindProjectRoot(t *testing.T) {
	proj := t.TempDir()
	t.Setenv("HOME", proj)
	t.Setenv("USERPROFILE", proj)

	if code := runInit(false, ""); code != 0 {
		t.Fatalf("runInit with empty override exit=%d", code)
	}
}

func TestSummarizeHooks_NilEngine(t *testing.T) {
	got := summarizeHooks(nil)
	if got.Total != 0 || len(got.PerEvent) != 0 {
		t.Fatalf("nil engine should return zero summary: %#v", got)
	}
}

func TestSummarizeHooks_NilHooks(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Hooks = nil
	got := summarizeHooks(eng)
	if got.Total != 0 || len(got.PerEvent) != 0 {
		t.Fatalf("nil hooks should return zero summary: %#v", got)
	}
}

func TestSummarizeHooks_WithHooks(t *testing.T) {
	eng := newCLITestEngine(t)
	got := summarizeHooks(eng)
	if got.Total < 0 {
		t.Fatalf("total hooks must not be negative: %d", got.Total)
	}
}

func TestSummarizeApprovalGate_NilEngine(t *testing.T) {
	got := summarizeApprovalGate(nil)
	if got.Active {
		t.Error("nil engine should not be active")
	}
}

func TestSummarizeApprovalGate_NoGates(t *testing.T) {
	eng := newCLITestEngine(t)
	got := summarizeApprovalGate(eng)
	if got.Active {
		t.Error("fresh engine should not have active gate")
	}
	if got.Count != 0 {
		t.Errorf("fresh engine should have count 0, got %d", got.Count)
	}
}

func TestSummarizeApprovalGate_WithGates(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file", "run_command"}
	got := summarizeApprovalGate(eng)
	if !got.Active {
		t.Error("engine with RequireApproval should be active")
	}
	if got.Count != 2 {
		t.Errorf("expected count 2, got %d", got.Count)
	}
	if got.Wildcard {
		t.Error("explicit list should not be wildcard")
	}
}

func TestSummarizeApprovalGate_Wildcard(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"*"}
	got := summarizeApprovalGate(eng)
	if !got.Wildcard {
		t.Error("RequireApproval=[\"*\"] should be wildcard")
	}
	if got.Count != -1 {
		t.Errorf("wildcard count should be -1, got %d", got.Count)
	}
}

func TestFormatModelsDevCacheSummary(t *testing.T) {
	samples := []struct {
		name string
		in   engine.ModelsDevCacheStatus
	}{
		{"empty", engine.ModelsDevCacheStatus{Path: "/cache", Exists: true}},
		{"exists", engine.ModelsDevCacheStatus{Exists: true, Path: "/path/to/cache", SizeBytes: 1024}},
		{"missing", engine.ModelsDevCacheStatus{Exists: false, Path: "/cache/path"}},
	}
	for _, s := range samples {
		got := formatModelsDevCacheSummary(s.in)
		if got == "" {
			t.Errorf("%s: got empty string", s.name)
		}
	}
}

func TestFormatModelsDevCacheSummary_EmptyPath(t *testing.T) {
	got := formatModelsDevCacheSummary(engine.ModelsDevCacheStatus{})
	if got != "" {
		t.Errorf("expected empty path to return empty string, got %q", got)
	}
}

func TestParseGlobalFlags_Provider(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--provider", "offline"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if opts.Provider != "offline" {
		t.Errorf("Provider: got %q want offline", opts.Provider)
	}
}

func TestParseGlobalFlags_Model(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--model", "claude-sonnet-4-6"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if opts.Model != "claude-sonnet-4-6" {
		t.Errorf("Model: got %q", opts.Model)
	}
}

func TestParseGlobalFlags_Profile(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--profile", "myprofile"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if opts.Profile != "myprofile" {
		t.Errorf("Profile: got %q", opts.Profile)
	}
}

func TestParseGlobalFlags_Verbose(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--verbose"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if !opts.Verbose {
		t.Error("Verbose should be true")
	}
}

func TestParseGlobalFlags_JSON(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--json"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if !opts.JSON {
		t.Error("JSON should be true")
	}
}

func TestParseGlobalFlags_NoColor(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--no-color"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if !opts.NoColor {
		t.Error("NoColor should be true")
	}
}

func TestParseGlobalFlags_Project(t *testing.T) {
	opts, _, err := parseGlobalFlags([]string{"--project", "/my/path"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if opts.Project != "/my/path" {
		t.Errorf("Project: got %q", opts.Project)
	}
}

func TestParseGlobalFlags_HelpFlag(t *testing.T) {
	opts, rest, err := parseGlobalFlags([]string{"--help"})
	if err != nil {
		t.Error("parseGlobalFlags should not return error for --help (flag.ErrHelp is swallowed)")
	}
	if opts.Provider != "" || rest != nil {
		t.Errorf("opts/rest should be empty/nil on --help: %#v / %#v", opts, rest)
	}
}

func TestParseGlobalFlags_UnknownFlag(t *testing.T) {
	_, _, err := parseGlobalFlags([]string{"--unknown-flag"})
	if err == nil {
		t.Error("expected error for unknown flag")
	}
}

func TestParseGlobalFlags_MultipleFlags(t *testing.T) {
	opts, rest, err := parseGlobalFlags([]string{
		"--provider", "offline",
		"--model", "sonnet",
		"--verbose",
		"--json",
		"--project", "/test",
	})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if opts.Provider != "offline" || opts.Model != "sonnet" || !opts.Verbose || !opts.JSON || opts.Project != "/test" {
		t.Errorf("unexpected opts: %#v", opts)
	}
	if len(rest) != 0 {
		t.Errorf("expected no remaining args, got %#v", rest)
	}
}

func TestParseGlobalFlags_RemainingArgs(t *testing.T) {
	// Flags consume all flag-looking tokens; once a non-flag is seen,
	// flag parsing stops and remaining positional args go to fs.Args().
	// "--verbose" is consumed as a flag (sets opts.Verbose), leaving only "status" in rest.
	opts, rest, err := parseGlobalFlags([]string{
		"--provider", "offline",
		"--verbose",
		"status",
	})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if opts.Provider != "offline" || !opts.Verbose {
		t.Errorf("unexpected opts: %#v", opts)
	}
	if len(rest) != 1 || rest[0] != "status" {
		t.Errorf("rest args: got %#v", rest)
	}
}

func containsJSONKey(s, key string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			start := i + 1
			end := start
			for end < len(s) && s[end] != '"' {
				end++
			}
			if s[start:end] == key {
				return true
			}
			i = end
		}
	}
	return false
}