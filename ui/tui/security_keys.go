package tui

// security_keys.go — Security panel key handlers + arrow-driven action
// menu. handleSecurityKey is the panel's normal mode (j/k scroll, v
// toggle view, / search, r rescan, c clear, enter/right opens menu);
// handleSecuritySearchKey runs while the search input is active.
// openSecurityActionMenu wires the same actions into the global
// arrow-only action surface used by every panel. Sibling files:
// security.go (data + sort/filter), security_render.go (rendering).
//
// Phase J item 1 — whitelist / ignore mechanism: `i` toggles the
// highlighted finding between IGNORED and active. Ignored findings
// stay in the list (so the user can un-ignore) but render with a
// muted IGN chip and drop out of the unfiltered count.

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// secretFingerprint returns a stable hash of (kind, file, line, rule).
// Matches across reruns so a re-scan correctly keeps the ignore mark
// on the same finding while letting the mark fall off if the code
// reshuffles enough that the line/rule no longer match.
func secretFingerprint(s security.SecretFinding) string {
	h := sha1.Sum([]byte(fmt.Sprintf("secret|%s|%d|%s", s.File, s.Line, s.Pattern)))
	return hex.EncodeToString(h[:8])
}

func vulnFingerprint(v security.VulnerabilityFinding) string {
	h := sha1.Sum([]byte(fmt.Sprintf("vuln|%s|%d|%s|%s", v.File, v.Line, v.Kind, v.CWE)))
	return hex.EncodeToString(h[:8])
}

// securitySelectedFingerprint returns the fingerprint of the
// currently-highlighted row in the active view, or "" when there's
// no selection.
func (m Model) securitySelectedFingerprint() string {
	if m.security.report == nil {
		return ""
	}
	if m.security.view == securityViewVulns {
		all := sortVulns(m.security.report.Vulnerabilities)
		filtered := filterVulns(all, m.security.query)
		if m.security.scroll < 0 || m.security.scroll >= len(filtered) {
			return ""
		}
		return vulnFingerprint(filtered[m.security.scroll])
	}
	all := sortSecrets(m.security.report.Secrets)
	filtered := filterSecrets(all, m.security.query)
	if m.security.scroll < 0 || m.security.scroll >= len(filtered) {
		return ""
	}
	return secretFingerprint(filtered[m.security.scroll])
}

// openSecurityActionMenu — arrow-driven action surface for Security.
func (m Model) openSecurityActionMenu() Model {
	actions := []panelAction{
		{Label: "Toggle view (secrets ↔ vulns)", Accel: "v",
			Handler: func(m Model) (Model, tea.Cmd) {
				if m.security.view == securityViewVulns {
					m.security.view = securityViewSecrets
				} else {
					m.security.view = securityViewVulns
				}
				m.security.scroll = 0
				return m, nil
			}},
		{Label: "Run / re-run scan", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.security.loading = true
				m.security.err = ""
				return m, loadSecurityCmd(m.eng)
			}},
		{Label: "Search…", Accel: "/",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.security.searchActive = true
				return m, nil
			}},
		{Label: "Clear search query", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.security.query = ""
				m.security.scroll = 0
				return m, nil
			}},
		{Label: "Toggle ignore on the highlighted finding (whitelist)", Accel: "i",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.toggleSecurityIgnore(), nil
			}},
		{Label: "Open in chat with fix request — sends file/line/snippet to the agent", Accel: "f",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.openSecurityFinding(), nil
			}},
	}
	return m.openActionMenu("Security", "Security actions", actions)
}

// openSecurityFinding seeds the chat composer with a structured
// "fix this" prompt for the highlighted finding and jumps to Chat.
// Phase J item 2 — turns triage into a one-keystroke handoff to the
// agent rather than the user retyping the file/line/severity.
func (m Model) openSecurityFinding() Model {
	if m.security.report == nil {
		m.notice = "No scan results — press r to run the scanner first."
		return m
	}
	prompt := ""
	if m.security.view == securityViewVulns {
		all := sortVulns(m.security.report.Vulnerabilities)
		filtered := filterVulns(all, m.security.query)
		if m.security.scroll < 0 || m.security.scroll >= len(filtered) {
			m.notice = "No finding selected."
			return m
		}
		v := filtered[m.security.scroll]
		prompt = securityFixPromptForVuln(v)
	} else {
		all := sortSecrets(m.security.report.Secrets)
		filtered := filterSecrets(all, m.security.query)
		if m.security.scroll < 0 || m.security.scroll >= len(filtered) {
			m.notice = "No finding selected."
			return m
		}
		s := filtered[m.security.scroll]
		prompt = securityFixPromptForSecret(s)
	}
	m.activeTab = m.activityTabIndex("Chat")
	m.setChatInput(prompt)
	m.notice = "Loaded fix request into chat — review the prompt and ctrl+x to send."
	return m
}

func securityFixPromptForSecret(s security.SecretFinding) string {
	tail := ""
	if s.Match != "" {
		tail = " The matched fragment was: `" + s.Match + "` (treat as compromised — rotate it)."
	}
	return fmt.Sprintf(
		"Security: hard-coded secret detected at [[file:%s#L%d]] (%s, severity=%s).%s "+
			"Propose a minimal patch that removes the secret from source, "+
			"replaces it with an env-var or config lookup, and adds a TODO "+
			"to rotate the credential. Show the diff before applying.",
		s.File, s.Line, s.Pattern, s.Severity, tail)
}

func securityFixPromptForVuln(v security.VulnerabilityFinding) string {
	cwe := ""
	if v.CWE != "" {
		cwe = " (" + v.CWE + ")"
	}
	owasp := ""
	if v.OWASP != "" {
		owasp = " · " + v.OWASP
	}
	snippet := ""
	if v.Snippet != "" {
		snippet = " Snippet: `" + oneLine(v.Snippet) + "`."
	}
	return fmt.Sprintf(
		"Security: %s vulnerability detected at [[file:%s#L%d]] (severity=%s%s%s).%s "+
			"Propose a minimal patch that fixes the vulnerability without changing "+
			"unrelated behaviour. Show the diff and call out any tests that should "+
			"be updated.",
		v.Kind, v.File, v.Line, v.Severity, cwe, owasp, snippet)
}

// toggleSecurityIgnore flips the ignore flag on the currently-
// highlighted finding. The fingerprint is hashed off (kind, file,
// line, rule) so a rescan re-applies the mark to the same finding
// even if the surrounding lines shift slightly. Phase J item 1.
func (m Model) toggleSecurityIgnore() Model {
	fp := m.securitySelectedFingerprint()
	if fp == "" {
		m.notice = "No finding selected — j/k highlights a row, then i ignores it."
		return m
	}
	if m.security.ignored == nil {
		m.security.ignored = map[string]bool{}
	}
	if m.security.ignored[fp] {
		delete(m.security.ignored, fp)
		m.notice = "Un-ignored — finding is active again."
	} else {
		m.security.ignored[fp] = true
		m.notice = "Ignored — counts will exclude this finding. Press i again to revert."
	}
	// Phase J item 1 — persist after every toggle so the mark survives
	// a TUI restart. Best-effort: a write failure surfaces in the
	// notice but the in-memory map still reflects the toggle so the
	// row updates immediately.
	if path := m.securityIgnoresPath(); path != "" {
		if err := saveSecurityIgnoresToDisk(path, m.security.ignored); err != nil {
			m.notice = "ignore toggled — but couldn't persist: " + err.Error()
		}
	}
	return m
}

func (m Model) handleSecurityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.security.searchActive {
		return m.handleSecuritySearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "enter" || s == "right" || s == "l" {
		return m.openSecurityActionMenu(), nil
	}
	total := m.securityViewTotal()
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.security.scroll+step < total {
			m.security.scroll += step
		}
	case "k", "up":
		if m.security.scroll >= step {
			m.security.scroll -= step
		} else {
			m.security.scroll = 0
		}
	case "pgdown":
		if m.security.scroll+pageStep < total {
			m.security.scroll += pageStep
		} else if total > 0 {
			m.security.scroll = total - 1
		}
	case "pgup":
		if m.security.scroll >= pageStep {
			m.security.scroll -= pageStep
		} else {
			m.security.scroll = 0
		}
	case "g":
		m.security.scroll = 0
	case "G":
		if total > 0 {
			m.security.scroll = total - 1
		}
	case "v":
		// Toggle view — reset scroll so we don't land past the new bounds.
		if m.security.view == securityViewVulns {
			m.security.view = securityViewSecrets
		} else {
			m.security.view = securityViewVulns
		}
		m.security.scroll = 0
	case "r":
		m.security.loading = true
		m.security.loaded = false
		m.security.err = ""
		return m, loadSecurityCmd(m.eng)
	case "/":
		m.security.searchActive = true
		return m, nil
	case "c":
		m.security.query = ""
		m.security.scroll = 0
	case "i":
		// Phase J item 1 — toggle the ignore (whitelist) flag on the
		// highlighted finding. The render path mutes ignored rows and
		// excludes them from the unfiltered counts so triage attention
		// stays on what's still hot.
		return m.toggleSecurityIgnore(), nil
	case "f":
		// Phase J item 2 — seed the chat composer with a structured
		// fix-request prompt for the highlighted finding and jump to
		// Chat so the user can review and ctrl+x to send.
		return m.openSecurityFinding(), nil
	}
	return m, nil
}

func (m Model) handleSecuritySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.security.searchActive = false
		m.security.scroll = 0
		return m, nil
	case tea.KeyEsc:
		m.security.searchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.security.query); len(r) > 0 {
			m.security.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.security.query += msg.String()
		return m, nil
	}
	return m, nil
}
