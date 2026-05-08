package drive

// report.go — Markdown rollup of a finished drive run. Mirrors the
// "what just happened" theme that the in-memory event firehose and the
// provider-call archive cover for shorter horizons: every drive run
// finalize() also emits a human-readable Markdown file under
// .dfmc/reports/drive-<runID>.md so the user can see exactly which
// TODOs landed, which got stuck, and how long each took without
// scrubbing through the event log.
//
// RenderRunReport is a pure function (no I/O) so tests pin the layout
// directly. The Driver's finalize() writes the rendered string when
// reportDir is configured (engine-side adapter sets it to
// <project>/.dfmc/reports/).

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RenderRunReport produces a Markdown rollup of a finished drive run.
// Layout: header (task / status / counts / duration), summary table of
// TODOs, then per-TODO detail blocks. Returns "" for a nil run.
//
// Status counts go directly into the header so a glance answers "did
// this run actually finish what I asked?" — the whole point of the
// archive. Per-TODO detail surfaces verification criteria, retry
// attempts, and the planner's brief so a future debug session has
// what was already known at run time.
func RenderRunReport(run *Run) string {
	if run == nil {
		return ""
	}
	var b strings.Builder
	b.Grow(2048)
	writeReportHeader(&b, run)
	writeReportSummaryTable(&b, run)
	writeReportTodoDetails(&b, run)
	return b.String()
}

func writeReportHeader(b *strings.Builder, run *Run) {
	fmt.Fprintf(b, "# Drive Run %s\n\n", run.ID)
	if task := strings.TrimSpace(run.Task); task != "" {
		fmt.Fprintf(b, "**Task**: %s\n\n", task)
	}
	done, blocked, skipped, pending := run.Counts()
	fmt.Fprintf(b, "**Status**: %s · %d done · %d blocked · %d skipped",
		run.Status, done, blocked, skipped)
	if pending > 0 {
		fmt.Fprintf(b, " · %d pending", pending)
	}
	if reason := strings.TrimSpace(run.Reason); reason != "" {
		fmt.Fprintf(b, " · reason: %s", reason)
	}
	b.WriteByte('\n')
	if !run.CreatedAt.IsZero() {
		fmt.Fprintf(b, "**Started**: %s\n", run.CreatedAt.UTC().Format(time.RFC3339))
	}
	if !run.EndedAt.IsZero() {
		fmt.Fprintf(b, "**Ended**: %s\n", run.EndedAt.UTC().Format(time.RFC3339))
		if !run.CreatedAt.IsZero() && run.EndedAt.After(run.CreatedAt) {
			fmt.Fprintf(b, "**Duration**: %s\n", run.EndedAt.Sub(run.CreatedAt).Round(time.Second))
		}
	}
	b.WriteByte('\n')
}

func writeReportSummaryTable(b *strings.Builder, run *Run) {
	if len(run.Todos) == 0 {
		b.WriteString("_No TODOs in this run._\n\n")
		return
	}
	b.WriteString("## Summary\n\n")
	b.WriteString("| ID | Title | Status | Worker | Attempts | Duration |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	todos := append([]Todo(nil), run.Todos...)
	sort.SliceStable(todos, func(i, j int) bool {
		return todos[i].ID < todos[j].ID
	})
	for _, t := range todos {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			title = strings.TrimSpace(t.Detail)
		}
		title = compactCell(title, 60)
		worker := blankFallback(t.WorkerClass, "—")
		dur := todoDuration(t)
		fmt.Fprintf(b, "| %s | %s | %s | %s | %d | %s |\n",
			t.ID, title, t.Status, worker, t.Attempts, dur)
	}
	b.WriteByte('\n')
}

func writeReportTodoDetails(b *strings.Builder, run *Run) {
	if len(run.Todos) == 0 {
		return
	}
	b.WriteString("## Details\n\n")
	todos := append([]Todo(nil), run.Todos...)
	sort.SliceStable(todos, func(i, j int) bool {
		return todos[i].ID < todos[j].ID
	})
	for _, t := range todos {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(b, "### %s. %s (%s)\n\n", t.ID, title, t.Status)
		if detail := strings.TrimSpace(t.Detail); detail != "" && detail != title {
			fmt.Fprintf(b, "%s\n\n", detail)
		}
		facts := []string{}
		if w := strings.TrimSpace(t.WorkerClass); w != "" {
			facts = append(facts, "worker: "+w)
		}
		if t.Attempts > 0 {
			facts = append(facts, fmt.Sprintf("attempts: %d", t.Attempts))
		}
		if dur := todoDuration(t); dur != "—" {
			facts = append(facts, "duration: "+dur)
		}
		if len(t.FileScope) > 0 {
			facts = append(facts, "file_scope: "+strings.Join(t.FileScope, ", "))
		}
		if len(t.DependsOn) > 0 {
			facts = append(facts, "depends_on: "+strings.Join(t.DependsOn, ", "))
		}
		if v := strings.TrimSpace(t.Verification); v != "" {
			facts = append(facts, "verification: "+v)
		}
		if len(facts) > 0 {
			fmt.Fprintf(b, "**%s**\n\n", strings.Join(facts, " · "))
		}
		if brief := strings.TrimSpace(t.Brief); brief != "" {
			fmt.Fprintf(b, "Brief: %s\n\n", compactCell(brief, 400))
		}
		if t.Status == TodoBlocked || strings.TrimSpace(t.Error) != "" {
			if reason := strings.TrimSpace(string(t.BlockedReason)); reason != "" {
				fmt.Fprintf(b, "**Blocked reason**: %s\n\n", reason)
			}
			if errMsg := strings.TrimSpace(t.Error); errMsg != "" {
				fmt.Fprintf(b, "**Error**: %s\n\n", compactCell(errMsg, 300))
			}
		}
	}
}

// todoDuration formats a TODO's start→end span as a compact string,
// returning "—" when either timestamp is missing or the span is zero
// (a TODO that never actually ran in this attempt).
func todoDuration(t Todo) string {
	if t.StartedAt.IsZero() || t.EndedAt.IsZero() {
		return "—"
	}
	span := t.EndedAt.Sub(t.StartedAt).Round(time.Second)
	if span <= 0 {
		return "—"
	}
	return span.String()
}

// compactCell trims newlines and clamps a string to fit a Markdown
// table cell or single inline paragraph. We keep enough length to be
// useful as a one-glance summary; users wanting the full text can
// open the run JSON in .dfmc/data/drive_runs.
func compactCell(s string, max int) string {
	s = strings.ReplaceAll(s, "|", "/")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 4 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func blankFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
