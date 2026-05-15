package tui

// security_render.go — view-side rendering for the Security panel.
// renderSecurityView frames the panel with the top banner, a query
// echo, the keyboard hint strip, and a divider, then dispatches into
// the active view (secrets or vulns) and lays out per-finding rows.
// formatSecretRow / formatVulnRow keep the per-row shape consistent
// across both views; severityStyle paints the leading badge so the eye
// catches CRIT/HIGH first. Sibling files: security.go (data + sort /
// filter helpers + scanner loader), security_keys.go (action menu and
// key handlers).

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

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

func formatSecretRow(s security.SecretFinding, selected, ignored bool, width int) string {
	loc := fmt.Sprintf("%s:%d", s.File, s.Line)
	head := severityStyle(s.Severity) + "  " + loc
	tail := "  " + accentStyle.Render(s.Pattern)
	if s.Match != "" {
		tail += subtleStyle.Render("  " + s.Match)
	}
	line := head + tail
	switch {
	case ignored:
		line = subtleStyle.Render("[IGN] ") + subtleStyle.Render(loc+"  "+s.Pattern)
		if s.Match != "" {
			line += subtleStyle.Render("  " + s.Match)
		}
	}
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

func formatVulnRow(v security.VulnerabilityFinding, selected, ignored bool, width int) string {
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
	if ignored {
		line = subtleStyle.Render("[IGN] " + loc + "  " + v.Kind)
		if v.CWE != "" {
			line += subtleStyle.Render("  " + v.CWE)
		}
		if v.Snippet != "" {
			line += subtleStyle.Render("  " + oneLine(v.Snippet))
		}
	}
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

func (m Model) securityTopBanner(width int, viewLabel string) string {
	title := titleStyle.Bold(true).Render("⚠ SECURITY")
	chipText, chipStyle := " NOT SCANNED ", subtleStyle
	switch {
	case m.security.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.security.loading:
		chipText, chipStyle = " SCANNING ", infoStyle
	case m.security.report != nil:
		secrets := len(m.security.report.Secrets)
		vulns := len(m.security.report.Vulnerabilities)
		switch {
		case secrets > 0:
			chipText, chipStyle = fmt.Sprintf(" %d SECRETS ", secrets), warnStyle
		case vulns > 0:
			chipText, chipStyle = fmt.Sprintf(" %d VULNS ", vulns), warnStyle
		default:
			chipText, chipStyle = " CLEAN ", okStyle
		}
	}
	chip := chipStyle.Render(chipText)
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chip)-2, 1)
	return title + strings.Repeat(" ", gap) + chip
}

func (m Model) renderSecurityView(width int) string {
	out := m.renderSecurityViewInner(width)
	if m.actionMenu.open && m.actionMenu.owner == "Security" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func (m Model) renderSecurityViewInner(width int) string {
	width = clampInt(width, 24, 1000)
	viewLabel := "secrets"
	if m.security.view == securityViewVulns {
		viewLabel = "vulns"
	}
	banner := m.securityTopBanner(width, viewLabel)

	affordance := subtleStyle.Render(
		"ctrl+f search  ·  ctrl+r rescan  ·  ctrl+i ignore  ·  ctrl+g/G top/bottom",
	)

	lines := []string{banner, affordance}

	if m.security.err != "" {
		lines = append(lines, "", warnStyle.Render("✗ error · "+m.security.err))
		return strings.Join(lines, "\n")
	}
	if m.security.loading {
		lines = append(lines, "", infoStyle.Render("◌ scanning..."))
		return strings.Join(lines, "\n")
	}
	if m.security.report == nil {
		lines = append(lines, "",
			subtleStyle.Render("No security scan run yet."),
			subtleStyle.Render("DFMC's heuristic scanner walks the project for hard-coded secrets (AWS keys, GitHub tokens, private keys, ...) and common vulnerability patterns (SQL string concat, command injection, weak crypto, ...)."),
			subtleStyle.Render("Press ctrl+r to scan, or run `dfmc security` from the CLI."))
		return strings.Join(lines, "\n")
	}

	ignoredSecrets := 0
	for _, s := range m.security.report.Secrets {
		if m.security.ignored[secretFingerprint(s)] {
			ignoredSecrets++
		}
	}
	ignoredVulns := 0
	for _, v := range m.security.report.Vulnerabilities {
		if m.security.ignored[vulnFingerprint(v)] {
			ignoredVulns++
		}
	}
	totalSecrets := len(m.security.report.Secrets)
	totalVulns := len(m.security.report.Vulnerabilities)
	activeSecrets := totalSecrets - ignoredSecrets
	activeVulns := totalVulns - ignoredVulns

	if activeSecrets == 0 && activeVulns == 0 && totalSecrets == 0 && totalVulns == 0 {
		summary := fmt.Sprintf("scanned %d files · 0 secrets · 0 vulns", m.security.report.FilesScanned)
		lines = append(lines, "", subtleStyle.Render(summary), okStyle.Render("✓ No secrets detected — commit with confidence."))
		return strings.Join(lines, "\n")
	}

	if activeSecrets == 0 && activeVulns == 0 {
		lines = append(lines, "", okStyle.Render("✓ all findings ignored."))
	} else if activeSecrets+activeVulns == 1 {
		single := ""
		if activeSecrets == 1 {
			single = fmt.Sprintf("%d secret", activeSecrets)
		} else {
			single = fmt.Sprintf("%d vulnerability", activeVulns)
		}
		if totalSecrets+totalVulns > 1 {
			lines = append(lines, "", warnStyle.Render("⚠ 1 active ")+subtleStyle.Render(single+fmt.Sprintf(" remaining · %d ignored", ignoredSecrets+ignoredVulns)))
		} else {
			lines = append(lines, "", warnStyle.Render("⚠ 1 active ")+subtleStyle.Render(single))
		}
	} else {
		summary := fmt.Sprintf("scanned %d files · %d secrets · %d vulns", m.security.report.FilesScanned, activeSecrets, activeVulns)
		if ignoredSecrets+ignoredVulns > 0 {
			summary += fmt.Sprintf(" · %d ignored (ctrl+i toggles)", ignoredSecrets+ignoredVulns)
		}
		lines = append(lines, "", subtleStyle.Render(summary))
	}

	if m.security.view == securityViewVulns {
		all := sortVulns(m.security.report.Vulnerabilities)
		filtered := filterVulns(all, m.security.query)
		if len(filtered) == 0 {
			if len(all) == 0 {
				lines = append(lines, "", okStyle.Render("✓ no vulnerabilities found. Codebase looks clean."))
			} else {
				lines = append(lines, "", subtleStyle.Render("no vulnerabilities match this query"))
			}
			return strings.Join(lines, "\n")
		}
		scroll := clampScroll(m.security.scroll, len(filtered))
		for i, v := range filtered[scroll:] {
			selected := (scroll + i) == m.security.scroll
			ignored := m.security.ignored[vulnFingerprint(v)]
			lines = append(lines, formatVulnRow(v, selected, ignored, width-2))
		}
		lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("%d shown · %d total", len(filtered), len(all))))
		return strings.Join(lines, "\n")
	}

	all := sortSecrets(m.security.report.Secrets)
	filtered := filterSecrets(all, m.security.query)
	if len(filtered) == 0 {
		if len(all) == 0 {
			lines = append(lines, "", okStyle.Render("✓ no secrets detected."))
		} else {
			lines = append(lines, "", subtleStyle.Render("no secrets match this query"))
		}
		return strings.Join(lines, "\n")
	}
	scroll := clampScroll(m.security.scroll, len(filtered))
	for i, s := range filtered[scroll:] {
		selected := (scroll + i) == m.security.scroll
		ignored := m.security.ignored[secretFingerprint(s)]
		lines = append(lines, formatSecretRow(s, selected, ignored, width-2))
	}
	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("%d shown · %d total", len(filtered), len(all))))
	return strings.Join(lines, "\n")
}

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

func (m Model) securityViewTotal() int {
	if m.security.report == nil {
		return 0
	}
	if m.security.view == securityViewVulns {
		return len(filterVulns(m.security.report.Vulnerabilities, m.security.query))
	}
	return len(filterSecrets(m.security.report.Secrets, m.security.query))
}
