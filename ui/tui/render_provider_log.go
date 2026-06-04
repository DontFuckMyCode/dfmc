package tui

// render_provider_log.go — renders the Provider Log panel: a live view
// of the per-call archive that internal/providerlog persists to
// <data-dir>/provider_calls/{YYYY-MM-DD}.jsonl. The panel is
// read-only; navigation is the same scrollOnly grammar Orchestrate /
// Shortcuts use (j/k/pgup/pgdn/g/G).
//
// The view is deliberately simple — header + per-record rows — because
// the persisted JSONL itself is the source of truth. Anything richer
// (filtering by model, totals, charts) can be added later without
// touching the underlying archive shape.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/providerlog"
)

// providerLogTailRows is the upper bound of records to materialise into
// the panel. Today's JSONL rarely exceeds a few hundred rows even on a
// heavy day; capping at 500 keeps the render bounded without surprising
// the user with truncated history. Older days remain on disk for ad-hoc
// inspection.
const providerLogTailRows = 500

func (m Model) renderProviderLogView(width int) string {
	if m.eng == nil || m.eng.ProviderLog == nil {
		return strings.Join([]string{
			accentStyle.Render("◇ Provider Log"),
			"",
			subtleStyle.Render("Archive not initialised — engine still warming up, or DataDir is unset."),
			subtleStyle.Render("When live, every provider call writes a JSONL row at <data-dir>/provider_calls/."),
		}, "\n")
	}
	recs := m.eng.ProviderLog.Tail(providerLogTailRows)
	header := []string{
		accentStyle.Bold(true).Render("◇ Provider Log") +
			subtleStyle.Render("  ·  ") +
			boldStyle.Render(fmt.Sprintf("%d call(s) today", len(recs))) +
			subtleStyle.Render("  ·  "+m.eng.ProviderLog.Dir()),
		subtleStyle.Render("Each row: ts · provider/model · in/out/total tokens · source · preview"),
		subtleStyle.Render("Live archive — survives crashes/compaction. ↑↓ page · esc close"),
		"",
	}
	if len(recs) == 0 {
		header = append(header, subtleStyle.Render("(no calls today yet — make a turn and come back)"))
		return strings.Join(header, "\n")
	}

	// Aggregate totals at the top so the user gets the rollup at a
	// glance — "ten calls, mostly Sonnet, 32K in / 8K out today."
	in, out, total := 0, 0, 0
	models := map[string]int{}
	for _, r := range recs {
		in += r.InputTokens
		out += r.OutputTokens
		total += r.TotalTokens
		key := r.Provider + "/" + r.Model
		models[key]++
	}
	header = append(header, subtleStyle.Render(fmt.Sprintf(
		"Totals: in=%s · out=%s · total=%s · across %d model(s)",
		compactMetric(in), compactMetric(out), compactMetric(total), len(models),
	)))
	if len(models) > 0 {
		modelLine := []string{}
		for k, v := range models {
			modelLine = append(modelLine, fmt.Sprintf("%s ×%d", k, v))
		}
		header = append(header, subtleStyle.Render("  · "+strings.Join(modelLine, "  ·  ")))
	}
	header = append(header, "")

	rows := make([]string, 0, len(recs))
	// Newest first — easier to scan on a fresh open without scrolling.
	for i := len(recs) - 1; i >= 0; i-- {
		rows = append(rows, formatProviderLogRow(recs[i], width))
	}
	return strings.Join(append(header, rows...), "\n")
}

// formatProviderLogRow renders a single record as a 2-line block: the
// metadata line (ts, model, tokens, source) and an optional preview
// line. Width-aware so the preview clips cleanly on narrow terminals
// rather than wrapping into a 3rd row.
func formatProviderLogRow(r providerlog.Record, width int) string {
	if width < 40 {
		width = 40
	}
	ts := strings.TrimSpace(r.TS)
	if len(ts) > 19 {
		ts = ts[:19]
	}
	who := r.Provider + "/" + r.Model
	if r.Provider == "" && r.Model == "" {
		who = "(unknown)"
	}
	toks := fmt.Sprintf("in=%s out=%s total=%s",
		compactMetric(r.InputTokens), compactMetric(r.OutputTokens), compactMetric(r.TotalTokens))
	meta := fmt.Sprintf("%s  %s  %s", subtleStyle.Render(ts), boldStyle.Render(who), toks)
	if r.Source != "" {
		meta += subtleStyle.Render("  · " + r.Source)
	}
	if r.DurationMs > 0 {
		meta += subtleStyle.Render(fmt.Sprintf("  · %dms", r.DurationMs))
	}
	if r.Error != "" {
		meta += "  " + failStyle.Render("err: "+truncateForLine(r.Error, 60))
	}
	out := []string{meta}
	previewBudget := width - 6 // leave 2 col indent + 4 col safety
	if previewBudget < 20 {
		previewBudget = 20
	}
	if user := strings.TrimSpace(r.UserPreview); user != "" {
		out = append(out, subtleStyle.Render("    user: ")+truncateForLine(user, previewBudget))
	}
	if asst := strings.TrimSpace(r.AssistantPreview); asst != "" {
		out = append(out, subtleStyle.Render("    asst: ")+truncateForLine(asst, previewBudget))
	}
	return strings.Join(out, "\n")
}
