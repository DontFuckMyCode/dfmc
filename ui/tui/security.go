package tui

// security.go — the Security panel surfaces the findings from
// internal/security.Scanner. The scanner is already wired into the
// engine via Analyze(Full|Security) — this panel just drives a scoped
// run against the project root and renders the Secrets/Vulnerability
// reports as a filterable list.
//
// Shape: a Report, a view toggle (secrets | vulns), a scroll offset,
// and a search query. Secrets are shown with redacted matches so the
// panel never leaks what it found; vulns show the offending line
// snippet plus CWE/OWASP tags where available.
//
// Refresh is manual — scanning is I/O bound and doing it on every tab
// switch would punish people who just glance at the tab. `r` re-runs.

import (
	"context"
	"fmt"
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

// severityStyle maps a severity label to a colour style so the eye
// picks out criticals before reading any text.
func severityStyle(sev string) string {
	tag := strings.ToUpper(strings.TrimSpace(sev))
	if tag == "" {
		tag = "UNK"
	}
	label := fmt.Sprintf("%-4s", tag[:minInt(len(tag), 4)])
	switch strings.ToLower(sev) {
	case "critical", "high":
		return warnStyle.Render(label)
	case "medium":
		return accentStyle.Render(label)
	default:
		return subtleStyle.Render(label)
	}
}

// formatSecretRow shape: `CRIT  file.go:42  AWS Access Key  AKIA****XYZ`.
func formatSecretRow(s security.SecretFinding, selected bool, width int) string {
	loc := fmt.Sprintf("%s:%d", s.File, s.Line)
	head := severityStyle(s.Severity) + "  " + loc
	tail := "  " + accentStyle.Render(s.Pattern)
	if s.Match != "" {
		tail += subtleStyle.Render("  " + s.Match)
	}
	line := head + tail
	if selected {
		line = accentStyle.Render("▶ ") + line
	} else {
		line = "  " + line
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatVulnRow shape: `HIGH  file.go:42  SQL Injection  CWE-89  <snippet>`.
func formatVulnRow(v security.VulnerabilityFinding, selected bool, width int) string {
	loc := fmt.Sprintf("%s:%d", v.File, v.Line)
	head := severityStyle(v.Severity) + "  " + loc
	tail := "  " + accentStyle.Render(v.Kind)
	if v.CWE != "" {
		tail += subtleStyle.Render("  " + v.CWE)
	}
	if v.Snippet != "" {
		tail += subtleStyle.Render("  " + oneLine(v.Snippet))
	}
	line := head + tail
	if selected {
		line = accentStyle.Render("▶ ") + line
	} else {
		line = "  " + line
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m Model) renderSecurityView(width int) string {
	width = clampInt(width, 24, 1000)
	hint := subtleStyle.Render("j/k scroll · v toggle secrets/vulns · / search · r rescan · c clear search")
	header := sectionHeader("⚠", "Security")
	viewLabel := "secrets"
	if m.securityView == securityViewVulns {
		viewLabel = "vulns"
	}
	viewLine := subtleStyle.Render("view: ") + accentStyle.Render(viewLabel)

	queryLine := subtleStyle.Render("query: ")
	if strings.TrimSpace(m.securityQuery) != "" {
		queryLine += m.securityQuery
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	if m.securitySearchActive {
		queryLine += subtleStyle.Render("  · typing, enter to commit")
	}

	lines := []string{header, hint, viewLine, queryLine, renderDivider(width - 2)}

	if m.securityErr != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.securityErr))
		return strings.Join(lines, "\n")
	}
	if m.securityLoading {
		lines = append(lines, "", subtleStyle.Render("scanning..."))
		return strings.Join(lines, "\n")
	}
	if m.securityReport == nil {
		lines = append(lines, "", subtleStyle.Render("Press r to run a security scan."))
		return strings.Join(lines, "\n")
	}

	// Always-present summary line so the user knows the scan actually ran.
	summary := fmt.Sprintf(
		"scanned %d files · %d secrets · %d vulnerabilities",
		m.securityReport.FilesScanned,
		len(m.securityReport.Secrets),
		len(m.securityReport.Vulnerabilities),
	)
	lines = append(lines, subtleStyle.Render(summary), "")

	if m.securityView == securityViewVulns {
		all := sortVulns(m.securityReport.Vulnerabilities)
		filtered := filterVulns(all, m.securityQuery)
		if len(filtered) == 0 {
			if len(all) == 0 {
				lines = append(lines, subtleStyle.Render("No vulnerabilities found. Codebase looks clean on heuristic rules."))
			} else {
				lines = append(lines, subtleStyle.Render("No vulnerabilities match this query."))
			}
			return strings.Join(lines, "\n")
		}
		scroll := clampScroll(m.securityScroll, len(filtered))
		for i, v := range filtered[scroll:] {
			selected := (scroll + i) == m.securityScroll
			lines = append(lines, formatVulnRow(v, selected, width-2))
		}
		lines = append(lines, "", subtleStyle.Render(fmt.Sprintf(
			"%d shown · %d total",
			len(filtered), len(all),
		)))
		return strings.Join(lines, "\n")
	}

	all := sortSecrets(m.securityReport.Secrets)
	filtered := filterSecrets(all, m.securityQuery)
	if len(filtered) == 0 {
		if len(all) == 0 {
			lines = append(lines, subtleStyle.Render("No secrets detected. Commit with confidence."))
		} else {
			lines = append(lines, subtleStyle.Render("No secrets match this query."))
		}
		return strings.Join(lines, "\n")
	}
	scroll := clampScroll(m.securityScroll, len(filtered))
	for i, s := range filtered[scroll:] {
		selected := (scroll + i) == m.securityScroll
		lines = append(lines, formatSecretRow(s, selected, width-2))
	}
	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf(
		"%d shown · %d total",
		len(filtered), len(all),
	)))
	return strings.Join(lines, "\n")
}

// clampScroll keeps the cursor inside the visible range of the current
// view. Separate from the Model so tests can hit it directly.
func clampScroll(scroll, total int) int {
	if scroll < 0 {
		return 0
	}
	if scroll >= total {
		if total == 0 {
			return 0
		}
		return total - 1
	}
	return scroll
}

// securityViewTotal returns the count of items in the currently
// selected view after filtering, used for scroll-bound maths.
func (m Model) securityViewTotal() int {
	if m.securityReport == nil {
		return 0
	}
	if m.securityView == securityViewVulns {
		return len(filterVulns(m.securityReport.Vulnerabilities, m.securityQuery))
	}
	return len(filterSecrets(m.securityReport.Secrets, m.securityQuery))
}

func (m Model) handleSecurityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.securitySearchActive {
		return m.handleSecuritySearchKey(msg)
	}
	total := m.securityViewTotal()
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.securityScroll+step < total {
			m.securityScroll += step
		}
	case "k", "up":
		if m.securityScroll >= step {
			m.securityScroll -= step
		} else {
			m.securityScroll = 0
		}
	case "pgdown":
		if m.securityScroll+pageStep < total {
			m.securityScroll += pageStep
		} else if total > 0 {
			m.securityScroll = total - 1
		}
	case "pgup":
		if m.securityScroll >= pageStep {
			m.securityScroll -= pageStep
		} else {
			m.securityScroll = 0
		}
	case "g":
		m.securityScroll = 0
	case "G":
		if total > 0 {
			m.securityScroll = total - 1
		}
	case "v":
		// Toggle view — reset scroll so we don't land past the new bounds.
		if m.securityView == securityViewVulns {
			m.securityView = securityViewSecrets
		} else {
			m.securityView = securityViewVulns
		}
		m.securityScroll = 0
	case "r":
		m.securityLoading = true
		m.securityErr = ""
		return m, loadSecurityCmd(m.eng)
	case "/":
		m.securitySearchActive = true
		return m, nil
	case "c":
		m.securityQuery = ""
		m.securityScroll = 0
	}
	return m, nil
}

func (m Model) handleSecuritySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.securitySearchActive = false
		m.securityScroll = 0
		return m, nil
	case tea.KeyEsc:
		m.securitySearchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.securityQuery); len(r) > 0 {
			m.securityQuery = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.securityQuery += msg.String()
		return m, nil
	}
	return m, nil
}
