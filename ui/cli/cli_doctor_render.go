package cli

// cli_doctor_render.go — text rendering for the dfmc doctor report.
// JSON mode is owned by runDoctor in cli_doctor.go directly; this
// sibling owns the human-facing fail/warn/pass grouped output and the
// status-glyph helpers each row uses. Splitting the report writer
// off from the check runner keeps appearance changes (color, layout,
// glyph choice) from churning the runDoctor body.

import (
	"fmt"
	"strings"
)

// renderDoctorReport prints the human-facing doctor output. Checks are
// grouped fail → warn → pass so the things that need user attention land
// at the top instead of being buried under a wall of green PASS lines.
// Each row uses a status glyph (✗ ⚠ ✓) with a one-line summary; the
// trailing footer carries the overall verdict and a pointer to --fix
// when it could help.
func renderDoctorReport(checks []doctorCheck, overall string, passN, warnN, failN int) {
	headlineGlyph, headlineLabel := doctorOverallGlyph(overall)
	fmt.Printf("%s DFMC doctor — %s  (%d pass · %d warn · %d fail)\n",
		headlineGlyph, headlineLabel, passN, warnN, failN)

	groups := []struct {
		status string
		label  string
	}{
		{"fail", "Failing checks"},
		{"warn", "Warnings"},
		{"pass", "Passing"},
	}
	for _, g := range groups {
		rows := doctorChecksWithStatus(checks, g.status)
		if len(rows) == 0 {
			continue
		}
		fmt.Println()
		fmt.Printf("  %s:\n", g.label)
		for _, c := range rows {
			glyph := doctorStatusGlyph(c.Status)
			detail := strings.TrimSpace(c.Details)
			if detail == "" {
				fmt.Printf("    %s %s\n", glyph, c.Name)
			} else {
				fmt.Printf("    %s %s — %s\n", glyph, c.Name, detail)
			}
		}
	}

	fmt.Println()
	switch overall {
	case "fail":
		fmt.Println("Some checks failed. `dfmc doctor --fix` repairs the safe ones; the rest need manual config edits.")
	case "warn":
		fmt.Println("All blocking checks passed; warnings are advisory. `dfmc doctor --network` adds endpoint reachability probes.")
	default:
		fmt.Println("All checks passed.")
	}
}

func doctorChecksWithStatus(checks []doctorCheck, status string) []doctorCheck {
	out := make([]doctorCheck, 0, len(checks))
	for _, c := range checks {
		if strings.EqualFold(c.Status, status) {
			out = append(out, c)
		}
	}
	return out
}

func doctorStatusGlyph(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass":
		return "✓"
	case "warn":
		return "⚠"
	case "fail":
		return "✗"
	default:
		return "·"
	}
}

func doctorOverallGlyph(overall string) (glyph, label string) {
	switch strings.ToLower(strings.TrimSpace(overall)) {
	case "fail":
		return "✗", "issues found"
	case "warn":
		return "⚠", "ready, with warnings"
	default:
		return "✓", "all systems go"
	}
}
