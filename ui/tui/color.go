// Color-profile initialisation. Respects standard no-color opt-outs
// BEFORE any other package renders styled text, so CLI output and
// TUI panels follow the same policy.
//
// Precedence (first match wins):
//
//  1. DFMC_NO_COLOR=<non-empty>      — DFMC-specific override, takes
//     precedence over everything so an operator can force monochrome
//     even when the terminal advertises colour.
//  2. NO_COLOR=<any>                 — https://no-color.org convention;
//     any non-empty value disables colour.
//  3. TERM=dumb                      — POSIX convention for
//     no-capability terminals (older CI runners, some scripting
//     pipes). Forces ASCII output.
//  4. Otherwise                      — let lipgloss auto-detect the
//     terminal's actual capabilities via termenv.
//
// The check runs from package init() so it fires before the first
// style is resolved — CLI output that uses lipgloss helpers (TUI
// panels, doctor banners, markdown rendering) all see the same
// profile. Tests can override by calling lipgloss.SetColorProfile
// directly.

package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() {
	if shouldDisableColor() {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// shouldDisableColor reports whether the process should render
// without ANSI colour sequences. Split out so tests can exercise
// the policy without mutating the global lipgloss state.
func shouldDisableColor() bool {
	if v := strings.TrimSpace(os.Getenv("DFMC_NO_COLOR")); v != "" {
		return true
	}
	// NO_COLOR is the no-color.org convention: any non-empty value
	// means "disable colour". We follow that spec exactly — no
	// value-parsing, no numeric comparison.
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return true
	}
	return false
}
