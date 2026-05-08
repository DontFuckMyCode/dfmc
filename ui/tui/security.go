package tui

// security.go — data side of the Security panel: the loader command
// that drives a Security-only Analyze, the sort + filter helpers
// shared by both the secrets and vulnerabilities views, and the
// loaded-message + view-mode constants. Rendering (banner, rows,
// view dispatch, scroll) lives in security_render.go; key handlers
// and the action menu live in security_keys.go.
//
// Refresh is manual — scanning is I/O bound and doing it on every tab
// switch would punish people who just glance at the tab. `r` re-runs.

import (
	"context"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/security"
)

const (
	securityViewSecrets = "secrets"
	securityViewVulns   = "vulns"
)

type securityLoadedMsg struct {
	report *security.Report
	err    error
}

// loadSecurityCmd runs the scanner over the current project via the
// engine's Analyze entrypoint. We ask for Security-only; Full would
// also run dead-code and complexity which are expensive and not what
// this panel shows.
func loadSecurityCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return securityLoadedMsg{}
		}
		root := ""
		if st := eng.Status(); st.ProjectRoot != "" {
			root = st.ProjectRoot
		}
		rep, err := eng.AnalyzeWithOptions(context.Background(), engine.AnalyzeOptions{
			Path:     root,
			Security: true,
		})
		if err != nil {
			return securityLoadedMsg{err: err}
		}
		return securityLoadedMsg{report: rep.Security}
	}
}

// severityRank sorts critical > high > medium > low > unknown. Used
// for both findings lists so the first thing the user sees is the
// scariest.
func severityRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	}
	return 9
}

// sortSecrets sorts findings by severity desc, then file, then line.
func sortSecrets(in []security.SecretFinding) []security.SecretFinding {
	out := append([]security.SecretFinding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := severityRank(out[i].Severity), severityRank(out[j].Severity); a != b {
			return a < b
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// sortVulns mirrors sortSecrets for the vulnerability list.
func sortVulns(in []security.VulnerabilityFinding) []security.VulnerabilityFinding {
	out := append([]security.VulnerabilityFinding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := severityRank(out[i].Severity), severityRank(out[j].Severity); a != b {
			return a < b
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// filterSecrets narrows by substring over pattern name and file. The
// redacted match itself is a poor search key (mostly asterisks).
func filterSecrets(in []security.SecretFinding, query string) []security.SecretFinding {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return in
	}
	out := make([]security.SecretFinding, 0, len(in))
	for _, f := range in {
		hay := strings.ToLower(f.Pattern + " " + f.File + " " + f.Severity)
		if strings.Contains(hay, q) {
			out = append(out, f)
		}
	}
	return out
}

// filterVulns narrows by substring over kind/file/CWE/OWASP/snippet so
// a user searching "CWE-89" lands directly on SQLi rows.
func filterVulns(in []security.VulnerabilityFinding, query string) []security.VulnerabilityFinding {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return in
	}
	out := make([]security.VulnerabilityFinding, 0, len(in))
	for _, f := range in {
		hay := strings.ToLower(strings.Join([]string{
			f.Kind, f.File, f.CWE, f.OWASP, f.Severity, f.Snippet,
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, f)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
