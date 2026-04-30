// Package planning — deterministic task decomposition. The splitter takes a
// free-text request and returns the list of atomic subtasks it detects, so
// the caller (agent loop, CLI, or a model-driven `task_split` tool) can
// decide whether to fan out to parallel sub-agents.
//
// The rules are deliberately conservative: a query that doesn't clearly
// decompose comes back as a single subtask (or empty, if the caller wants
// to distinguish "don't split" from "one task"). False positives here are
// more expensive than false negatives — fanning out a single-shot question
// wastes tokens; missing a fan-out opportunity just means the model
// handles it sequentially.
package planning

import (
	"regexp"
	"strings"
)

// Subtask is one unit of work the splitter extracted from the query.
type Subtask struct {
	// Title is a short label (under ~60 chars) suitable for a TUI list.
	Title string
	// Description is the full task text the agent loop (or sub-agent) can
	// use as its prompt. Preserves the original wording as closely as
	// possible so intent isn't lost.
	Description string
	// Hint is the splitter's reason for calling this a separate subtask
	// — e.g. "numbered-list", "conjunction", "stage". Useful for the UI
	// to explain why the split happened.
	Hint string
}

// Plan describes the decomposition of a query.
type Plan struct {
	// Original is the input query verbatim.
	Original string
	// Subtasks is the ordered list of atomic pieces. Length 0 means the
	// splitter did not detect any split candidates; length 1 means it
	// processed the query but concluded it's a single unit.
	Subtasks []Subtask
	// Parallel reports whether the subtasks can plausibly run in parallel.
	// "Stage" splits ("first do X, then Y") mark Parallel=false because
	// Y depends on X. Independent conjunctions stay Parallel=true.
	Parallel bool
	// Confidence in [0,1]. Rough signal — TUI can use it to decide whether
	// to auto-fan-out (>=0.7) or just surface the plan for user approval.
	Confidence float64
}

// numberedMarkerRE locates the leading "1.", "2)", "3-" markers that split
// an enumerated list. We find each marker's position, then slice the input
// between consecutive markers — cleaner than trying to capture the body
// with a single monolithic pattern.
var numberedMarkerRE = regexp.MustCompile(`(?:^|[\s:;,])([0-9]+)[\.\)\-]\s+`)

// stageMarkerRE identifies sequential markers ("first", "then", "next",
// "after that") that imply a pipeline rather than parallel fan-out.
var stageMarkerRE = regexp.MustCompile(`(?i)\b(?:first|then|next|after\s+that|afterwards|finally|lastly|önce|sonra|ardından)\b`)

// conjunctionPattern splits on high-level conjunctions between verb
// phrases. We only fire when conjunctions appear ≥2 times in the same
// sentence, signalling an honest list rather than a passing "X and Y".
var conjunctionSplitRE = regexp.MustCompile(`(?i)\s*(?:,\s*and\s+|\s+and\s+also\s+|\s+as\s+well\s+as\s+|\s+plus\s+)`)

// SplitTask analyzes the query and returns its Plan. Caller can then
// inspect Plan.Subtasks and Plan.Parallel to decide whether to fan out.
func SplitTask(query string) Plan {
	q := strings.TrimSpace(query)
	plan := Plan{Original: q}
	if q == "" {
		return plan
	}

	if subs := splitNumberedList(q); len(subs) >= 2 {
		plan.Subtasks = subs
		plan.Parallel = !containsStageMarker(q)
		plan.Confidence = 0.85
		return plan
	}

	if subs := splitStages(q); len(subs) >= 2 {
		plan.Subtasks = subs
		plan.Parallel = false
		plan.Confidence = 0.7
		return plan
	}

	if subs := splitConjunctions(q); len(subs) >= 2 {
		plan.Subtasks = subs
		plan.Parallel = true
		plan.Confidence = 0.6
		return plan
	}

	// Single task — return it as the sole subtask with a low confidence
	// signal so callers know the splitter ran but declined to fan out.
	plan.Subtasks = []Subtask{{
		Title:       truncateTitle(q, 60),
		Description: q,
		Hint:        "single",
	}}
	plan.Parallel = false
	plan.Confidence = 0.2
	return plan
}

// splitNumberedList extracts "1) X 2) Y 3) Z" items. Most reliable signal
// because the user explicitly enumerated the pieces. We locate every
// marker's byte index, then slice the text between consecutive markers.
func splitNumberedList(q string) []Subtask {
	idx := numberedMarkerRE.FindAllStringIndex(q, -1)
	if len(idx) < 2 {
		return nil
	}
	out := make([]Subtask, 0, len(idx))
	for i, m := range idx {
		// Body starts just after the matched marker (m[1]) and ends at
		// the next marker's start (or end of string).
		start := m[1]
		end := len(q)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		desc := strings.TrimSpace(q[start:end])
		desc = strings.Trim(desc, ".,;:")
		if desc == "" {
			continue
		}
		out = append(out, Subtask{
			Title:       truncateTitle(desc, 60),
			Description: desc,
			Hint:        "numbered-list",
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// splitStages splits on "first/then/next/after" markers. Produces an
// ordered plan — subtasks are sequential, not parallel.
func splitStages(q string) []Subtask {
	if !stageMarkerRE.MatchString(q) {
		return nil
	}
	// Split the query at each stage marker, keeping the markers as hints.
	// We lower-case the text for matching but keep the original indices.
	lower := strings.ToLower(q)
	indexes := stageMarkerRE.FindAllStringIndex(lower, -1)
	if len(indexes) < 1 {
		return nil
	}
	// Build segments: [0..first], [first..second], ..., [lastN..end]
	points := make([]int, 0, len(indexes)+2)
	points = append(points, 0)
	for _, idx := range indexes {
		points = append(points, idx[0])
	}
	points = append(points, len(q))

	out := make([]Subtask, 0, len(points)-1)
	for i := 0; i < len(points)-1; i++ {
		seg := strings.TrimSpace(q[points[i]:points[i+1]])
		seg = strings.Trim(seg, ".,;:")
		if seg == "" {
			continue
		}
		out = append(out, Subtask{
			Title:       truncateTitle(seg, 60),
			Description: seg,
			Hint:        "stage",
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// splitConjunctions breaks on ", and" / "as well as" style conjunctions.
// Only fires when we see at least two such connectors, so "fix A and B" stays
// as one task but "fix A, and fix B, and fix C" splits.
func splitConjunctions(q string) []Subtask {
	matches := conjunctionSplitRE.FindAllStringIndex(q, -1)
	if len(matches) < 2 {
		return nil
	}
	parts := conjunctionSplitRE.Split(q, -1)
	out := make([]Subtask, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, ".,;:")
		if p == "" {
			continue
		}
		out = append(out, Subtask{
			Title:       truncateTitle(p, 60),
			Description: p,
			Hint:        "conjunction",
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func containsStageMarker(q string) bool {
	return stageMarkerRE.MatchString(q)
}

// truncateTitle cuts s to at most max RUNES (not bytes), preferring an ASCII
// space in the trailing 15-rune window so the cut lands on a word boundary.
// The package tests Turkish stage markers (önce/sonra/ardından) and titles
// from those queries are routinely multi-byte; a byte-indexed cut could land
// inside a rune (e.g. between the 0xC3 and 0xA7 of "ç") and emit invalid
// UTF-8 followed by the ellipsis. Walking by rune index keeps the boundary
// safe regardless of input character set.
func truncateTitle(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	cut := max
	for i := max; i > max-15 && i > 0; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + "…"
}
