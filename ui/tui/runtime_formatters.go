package tui

// runtime_formatters.go — stateless number/time formatters used by the
// runtime strip. Pure functions: no Model receivers, no package state.

import (
	"fmt"
	"time"
)

// computeTurnElapsedSec returns the seconds since the current turn's
// agent:loop:start. Returns 0 when no turn is active (turnStartedAt zero
// or agentLoop inactive) so the badge stays hidden between turns.
func computeTurnElapsedSec(s agentLoopState) int {
	if !s.active || s.turnStartedAt.IsZero() {
		return 0
	}
	d := time.Since(s.turnStartedAt)
	if d < 0 {
		return 0
	}
	return int(d.Seconds())
}

// toolPacePerMinute returns the rolling tools-per-minute pace for the
// current turn, or 0 when not enough data has accumulated to give a
// stable rate. Gated on >=10s elapsed AND >=2 rounds because first-
// tool-call spikes dominate the rate at low elapsed.
func toolPacePerMinute(vm runtimeViewModel) int {
	if vm.TurnElapsedSec < 10 || vm.ToolRounds < 2 {
		return 0
	}
	pace := (vm.ToolRounds * 60) / vm.TurnElapsedSec
	if pace <= 0 {
		return 0
	}
	return pace
}

// formatTurnElapsed renders an int-seconds duration as "2m 34s" /
// "47s" / "1h 12m". Drops sub-minute precision past 60m so a long
// autonomous run reads cleanly instead of "73m 14s".
func formatTurnElapsed(sec int) string {
	if sec <= 0 {
		return ""
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %02ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %02dm", sec/3600, (sec%3600)/60)
}

// formatTokenCount renders a token total compactly (1234 → "1.2k",
// 12345 → "12k") for the auto-resume badge. The badge sits in a
// crowded strip; raw five-digit numbers wash out the rest of the line.
func formatTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}
