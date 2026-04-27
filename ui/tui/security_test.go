package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

func newSecurityTestModel() Model {
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security"},
		activeTab:             11,
		diagnosticPanelsState: newDiagnosticPanelsState(),
	}
}

func sampleSecurityReport() *security.Report {
	return &security.Report{
		FilesScanned: 42,
		Secrets: []security.SecretFinding{
			{Pattern: "AWS Access Key", File: "pkg/foo.go", Line: 17, Match: "AKIA****XYZ", Severity: "critical"},
			{Pattern: "Generic API Key", File: "internal/bar.go", Line: 5, Match: "abc****xyz", Severity: "high"},
			{Pattern: "Slack Token", File: "cmd/main.go", Line: 100, Match: "xoxb****end", Severity: "high"},
		},
		Vulnerabilities: []security.VulnerabilityFinding{
			{Kind: "Potential SQL Injection", File: "pkg/db.go", Line: 22, Severity: "high", CWE: "CWE-89", OWASP: "A03:2021 Injection", Snippet: "db.Query(\"SELECT \" + user.Name)"},
			{Kind: "Potential SSRF", File: "pkg/http.go", Line: 10, Severity: "medium", CWE: "CWE-918", Snippet: "http.Get(target)"},
		},
	}
}

func TestSeverityRankOrders(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"critical", "high", true},
		{"high", "medium", true},
		{"medium", "low", true},
		{"low", "", true},
		{"unknown", "critical", false},
	}
	for _, c := range cases {
		if (severityRank(c.a) < severityRank(c.b)) != c.less {
			t.Fatalf("rank(%q)<rank(%q) should be %v", c.a, c.b, c.less)
		}
	}
}

func TestSortSecretsBySeverityThenFile(t *testing.T) {
	in := []security.SecretFinding{
		{Severity: "high", File: "z.go"},
		{Severity: "critical", File: "a.go"},
		{Severity: "high", File: "a.go"},
	}
	got := sortSecrets(in)
	if got[0].Severity != "critical" || got[0].File != "a.go" {
		t.Fatalf("critical/a.go should come first, got %#v", got[0])
	}
	if got[1].File != "a.go" || got[2].File != "z.go" {
		t.Fatalf("file tiebreak wrong, got %#v", got)
	}
}

func TestFilterSecretsMatchesPatternAndFile(t *testing.T) {
	rep := sampleSecurityReport()
	got := filterSecrets(rep.Secrets, "aws")
	if len(got) != 1 || got[0].Pattern != "AWS Access Key" {
		t.Fatalf("aws filter failed: %#v", got)
	}
	got = filterSecrets(rep.Secrets, "cmd/")
	if len(got) != 1 || got[0].File != "cmd/main.go" {
		t.Fatalf("path filter failed: %#v", got)
	}
	got = filterSecrets(rep.Secrets, "")
	if len(got) != 3 {
		t.Fatalf("empty query should pass through, got %d", len(got))
	}
}

func TestFilterVulnsMatchesCWE(t *testing.T) {
	rep := sampleSecurityReport()
	got := filterVulns(rep.Vulnerabilities, "CWE-89")
	if len(got) != 1 || !strings.Contains(got[0].CWE, "89") {
		t.Fatalf("CWE filter failed: %#v", got)
	}
	got = filterVulns(rep.Vulnerabilities, "SELECT")
	if len(got) != 1 {
		t.Fatalf("snippet filter should find SQL row, got %#v", got)
	}
}

func TestFormatSecretRowContainsSignalFields(t *testing.T) {
	s := sampleSecurityReport().Secrets[0]
	row := formatSecretRow(s, false, 200)
	for _, want := range []string{"CRIT", "pkg/foo.go:17", "AWS Access Key", "AKIA****XYZ"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestFormatSecretRowHighlightsSelected(t *testing.T) {
	s := sampleSecurityReport().Secrets[0]
	selected := formatSecretRow(s, true, 200)
	unselected := formatSecretRow(s, false, 200)
	if !strings.Contains(selected, "▶") {
		t.Fatalf("selected row missing arrow: %q", selected)
	}
	if strings.Contains(unselected, "▶") {
		t.Fatalf("unselected row should not carry arrow: %q", unselected)
	}
}

func TestFormatVulnRowContainsCWEAndSnippet(t *testing.T) {
	v := sampleSecurityReport().Vulnerabilities[0]
	row := formatVulnRow(v, false, 400)
	for _, want := range []string{"HIGH", "pkg/db.go:22", "SQL Injection", "CWE-89", "SELECT"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestRenderSecurityViewNeedsScanCopy(t *testing.T) {
	m := newSecurityTestModel()
	out := m.renderSecurityView(100)
	if !strings.Contains(out, "Security") {
		t.Fatalf("header missing: %s", out)
	}
	if !strings.Contains(out, "Press r to run a security scan") {
		t.Fatalf("initial copy missing: %s", out)
	}
}

func TestRenderSecurityViewCleanResult(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = &security.Report{FilesScanned: 20}
	out := m.renderSecurityView(100)
	if !strings.Contains(out, "scanned 20 files") {
		t.Fatalf("summary line missing: %s", out)
	}
	if !strings.Contains(out, "No secrets detected") {
		t.Fatalf("clean-state copy missing: %s", out)
	}
}

func TestRenderSecurityViewWithFindings(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	out := m.renderSecurityView(160)
	if !strings.Contains(out, "3 secrets") {
		t.Fatalf("summary line missing: %s", out)
	}
	if !strings.Contains(out, "pkg/foo.go:17") {
		t.Fatalf("first secret row missing: %s", out)
	}
	if !strings.Contains(out, "3 shown · 3 total") {
		t.Fatalf("footer count wrong: %s", out)
	}
}

func TestRenderSecurityViewVulnsToggle(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	m.security.view = securityViewVulns
	out := m.renderSecurityView(200)
	if !strings.Contains(out, "pkg/db.go:22") {
		t.Fatalf("vuln row missing: %s", out)
	}
	if !strings.Contains(out, "SQL Injection") {
		t.Fatalf("vuln kind missing: %s", out)
	}
	if !strings.Contains(out, "2 shown · 2 total") {
		t.Fatalf("vuln footer count wrong: %s", out)
	}
}

func TestRenderSecurityViewErrorBanner(t *testing.T) {
	m := newSecurityTestModel()
	m.security.err = "scan failed"
	out := m.renderSecurityView(80)
	if !strings.Contains(out, "error · scan failed") {
		t.Fatalf("error banner missing: %s", out)
	}
}

func TestSecurityViewToggleBinding(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()
	m.security.scroll = 2

	m2, _ := m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = m2.(Model)
	if m.security.view != securityViewVulns {
		t.Fatalf("v should flip to vulns, got %q", m.security.view)
	}
	if m.security.scroll != 0 {
		t.Fatalf("view toggle should reset scroll, got %d", m.security.scroll)
	}

	m2, _ = m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = m2.(Model)
	if m.security.view != securityViewSecrets {
		t.Fatalf("second v should flip back to secrets, got %q", m.security.view)
	}
}

func TestSecurityScrollBindings(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()

	m2, _ := m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.security.scroll != 1 {
		t.Fatalf("j should advance, got %d", m.security.scroll)
	}
	m2, _ = m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.security.scroll != 2 {
		t.Fatalf("G should jump to last, got %d", m.security.scroll)
	}
	m2, _ = m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.security.scroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.security.scroll)
	}
}

func TestSecuritySearchInputFlow(t *testing.T) {
	m := newSecurityTestModel()
	m.security.report = sampleSecurityReport()

	m2, _ := m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = m2.(Model)
	if !m.security.searchActive {
		t.Fatalf("search mode should activate on /")
	}

	for _, r := range "aws" {
		m2, _ = m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(Model)
	}
	if m.security.query != "aws" {
		t.Fatalf("want query=aws, got %q", m.security.query)
	}

	m2, _ = m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.security.searchActive {
		t.Fatalf("enter should exit search mode")
	}

	m2, _ = m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if m.security.query != "" {
		t.Fatalf("c should clear query, got %q", m.security.query)
	}
}

func TestSecurityRefreshSetsLoading(t *testing.T) {
	m := newSecurityTestModel()
	m.security.err = "stale"
	m2, _ := m.handleSecurityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = m2.(Model)
	if !m.security.loading {
		t.Fatalf("r should set loading=true")
	}
	if m.security.err != "" {
		t.Fatalf("r should clear error, got %q", m.security.err)
	}
}

func TestClampScrollBounds(t *testing.T) {
	if clampScroll(-1, 5) != 0 {
		t.Fatalf("negative scroll should clamp to 0")
	}
	if clampScroll(10, 5) != 4 {
		t.Fatalf("overflow scroll should clamp to last, got %d", clampScroll(10, 5))
	}
	if clampScroll(0, 0) != 0 {
		t.Fatalf("empty list should pin scroll at 0")
	}
	if clampScroll(2, 5) != 2 {
		t.Fatalf("in-range scroll should pass through")
	}
}
