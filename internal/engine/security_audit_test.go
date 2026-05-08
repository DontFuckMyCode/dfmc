package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

func TestIsSensitivePath_MatchesSecurityRelatedDirs(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"internal/auth/token.go", true},
		{"internal/security/scanner.go", true},
		{"internal/provider/router.go", true},
		{"internal/crypto/aes.go", true},
		{"pkg/oauth/client.go", true},
		{"cmd/keychain/main.go", true},
		// case-insensitive
		{"internal/Auth/Token.go", true},
		// Windows-style separator
		{"internal\\auth\\token.go", true},
		// Test files MUST be skipped — they routinely carry stub creds.
		{"internal/auth/token_test.go", false},
		{"src/auth/login.test.ts", false},
		// Unrelated paths must miss.
		{"ui/tui/render_layout.go", false},
		{"docs/architecture.md", false},
		{"", false},
	}
	for _, c := range cases {
		got := isSensitivePath(c.path)
		if got != c.want {
			t.Errorf("isSensitivePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestExtractWriteToolPaths_ShapesPerTool(t *testing.T) {
	// edit_file / write_file / symbol_rename use plain "path"
	for _, tool := range []string{"edit_file", "write_file", "symbol_rename", "EDIT_FILE"} {
		got := extractWriteToolPaths(tool, map[string]any{"path": "internal/auth/foo.go"})
		if len(got) != 1 || got[0] != "internal/auth/foo.go" {
			t.Errorf("%s: want [internal/auth/foo.go], got %v", tool, got)
		}
	}
	// symbol_move emits both endpoints
	got := extractWriteToolPaths("symbol_move", map[string]any{
		"from_path": "old/path.go",
		"to_path":   "new/path.go",
	})
	if len(got) != 2 || got[0] != "old/path.go" || got[1] != "new/path.go" {
		t.Errorf("symbol_move: want [old/path.go new/path.go], got %v", got)
	}
	// apply_patch returns nil today (diff parsing is future work)
	if got := extractWriteToolPaths("apply_patch", map[string]any{"diff": "..."}); got != nil {
		t.Errorf("apply_patch should return nil for now, got %v", got)
	}
	// Missing path → nil
	if got := extractWriteToolPaths("write_file", map[string]any{}); got != nil {
		t.Errorf("missing path should return nil, got %v", got)
	}
	// Unknown tool → nil
	if got := extractWriteToolPaths("read_file", map[string]any{"path": "x"}); got != nil {
		t.Errorf("unknown write tool should return nil, got %v", got)
	}
}

func TestSeverityCounters_IgnoreCase(t *testing.T) {
	r := security.Report{
		Secrets: []security.SecretFinding{
			{Severity: "Critical"},
			{Severity: "high"},
			{Severity: "low"},
		},
		Vulnerabilities: []security.VulnerabilityFinding{
			{Severity: "CRITICAL"},
			{Severity: "High"},
			{Severity: "medium"},
		},
	}
	if got := countCriticalFindings(r); got != 2 {
		t.Errorf("critical: want 2, got %d", got)
	}
	if got := countHighFindings(r); got != 2 {
		t.Errorf("high: want 2, got %d", got)
	}
}

func TestFormatAuditCoachNote_ConveysCounts(t *testing.T) {
	note := formatAuditCoachNote("write_file", []string{"internal/auth/token.go"}, 1, 2)
	if !strings.Contains(note, "1 critical") {
		t.Errorf("missing critical count: %q", note)
	}
	if !strings.Contains(note, "2 high") {
		t.Errorf("missing high count: %q", note)
	}
	if !strings.Contains(note, "internal/auth/token.go") {
		t.Errorf("missing path: %q", note)
	}
	if !strings.Contains(note, "/audit") {
		t.Errorf("missing /audit hint: %q", note)
	}

	// Multiple paths get a "+N more" suffix.
	multi := formatAuditCoachNote("symbol_move", []string{"a.go", "b.go", "c.go"}, 1, 0)
	if !strings.Contains(multi, "+2 more") {
		t.Errorf("multi-path missing '+2 more': %q", multi)
	}
}

func TestMaybeAuditSensitiveWrite_NilSafe(t *testing.T) {
	var e *Engine
	e.maybeAuditSensitiveWrite("write_file", map[string]any{"path": "x"}) // must not panic
}
