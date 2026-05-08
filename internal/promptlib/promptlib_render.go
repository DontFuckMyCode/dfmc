package promptlib

// promptlib_render.go — Render path: best-template selection, append-axis
// resolution, scoring, body interpolation, and the cache-break splice that
// keeps overlays inside the prefix-cacheable region of the rendered prompt.
//
// Core types (Library, Template, RenderRequest) and the upsert/load lifecycle
// live in promptlib.go. File loaders live in promptlib_decode.go. Task /
// language inference lives in promptlib_detect.go.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

func (l *Library) Render(req RenderRequest) string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	baseTpl, baseScore := l.bestReplaceTemplate(req)
	base := defaultFallbackPrompt(req)
	if baseScore >= 0 {
		base = renderBody(baseTpl.Body, req.Vars)
	}

	appendParts := l.renderAppendParts(req)
	rendered := make([]string, 0, len(appendParts))
	for _, p := range appendParts {
		if s := strings.TrimSpace(renderBody(p.Body, req.Vars)); s != "" {
			rendered = append(rendered, s)
		}
	}
	if len(rendered) == 0 {
		return base
	}
	return spliceAppendBeforeCacheBreak(base, rendered)
}

// spliceAppendBeforeCacheBreak inserts task/role/profile overlays ahead
// of the stable/dynamic boundary so they join the cacheable prefix
// instead of being shunted into the per-request tail. Overlays are
// stable per conversation (the task/profile rarely changes mid-thread)
// so placing them in the cacheable region materially raises the share
// that Anthropic's prompt caching amortises. Templates without a marker
// keep the old behavior — the overlays are appended at the end, which
// reads as "dynamic" to the catalog splitter but is the historical
// layout we shouldn't regress away from silently.
func spliceAppendBeforeCacheBreak(base string, overlays []string) string {
	if len(overlays) == 0 {
		return base
	}
	overlaysText := strings.Join(overlays, "\n\n")
	stable, dynamic, found := strings.Cut(base, CacheBreakMarker)
	if !found {
		return strings.TrimSpace(base + "\n\n" + overlaysText)
	}
	stable = strings.TrimRight(stable, "\n")
	dynamic = strings.TrimLeft(dynamic, "\n")
	var b strings.Builder
	if s := strings.TrimSpace(stable); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString(overlaysText)
	b.WriteString("\n\n")
	b.WriteString(CacheBreakMarker)
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(dynamic))
	return strings.TrimSpace(b.String())
}

func normalizeTemplate(t Template) Template {
	t.ID = strings.TrimSpace(t.ID)
	t.Type = normalizeKey(t.Type)
	if t.Type == "" {
		t.Type = "system"
	}
	t.Task = normalizeKey(t.Task)
	if t.Task == "" {
		t.Task = "general"
	}
	t.Language = normalizeKey(t.Language)
	t.Profile = normalizeKey(t.Profile)
	t.Role = normalizeKey(t.Role)
	t.Compose = normalizeComposeMode(t.Compose)
	t.Description = strings.TrimSpace(t.Description)
	t.Body = strings.TrimSpace(t.Body)
	return t
}

func normalizeKey(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func normalizeComposeMode(s string) string {
	switch normalizeKey(s) {
	case "append":
		return "append"
	default:
		return "replace"
	}
}

func (l *Library) bestReplaceTemplate(req RenderRequest) (Template, int) {
	best := Template{}
	bestScore := -1_000_000
	for _, t := range l.templates {
		if t.Compose != "replace" {
			continue
		}
		score, ok := templateScore(t, req)
		if !ok {
			continue
		}
		if score > bestScore || (score == bestScore && t.Priority > best.Priority) {
			bestScore = score
			best = t
		}
	}
	return best, bestScore
}

func (l *Library) renderAppendParts(req RenderRequest) []Template {
	type scored struct {
		template Template
		score    int
	}
	picks := map[string]scored{}
	extras := make([]scored, 0, 4)
	for _, t := range l.templates {
		if t.Compose != "append" {
			continue
		}
		score, ok := templateScore(t, req)
		if !ok {
			continue
		}
		axis := appendAxis(t, req)
		entry := scored{template: t, score: score}
		if axis == "extra" {
			extras = append(extras, entry)
			continue
		}
		if cur, ok := picks[axis]; !ok || entry.score > cur.score || (entry.score == cur.score && entry.template.Priority > cur.template.Priority) {
			picks[axis] = entry
		}
	}

	out := make([]Template, 0, 6)
	for _, axis := range []string{"global", "task", "language", "profile", "role"} {
		if v, ok := picks[axis]; ok {
			out = append(out, v.template)
		}
	}
	if len(extras) > 0 {
		sort.Slice(extras, func(i, j int) bool {
			if extras[i].score == extras[j].score {
				return extras[i].template.Priority > extras[j].template.Priority
			}
			return extras[i].score > extras[j].score
		})
		out = append(out, extras[0].template)
	}
	return out
}

func appendAxis(t Template, req RenderRequest) string {
	task := normalizeKey(req.Task)
	lang := normalizeKey(req.Language)
	profile := normalizeKey(req.Profile)
	role := normalizeKey(req.Role)

	switch {
	case t.Task == "general" && t.Language == "" && t.Profile == "" && t.Role == "":
		return "global"
	case task != "" && t.Task == task && t.Language == "" && t.Profile == "" && t.Role == "":
		return "task"
	case lang != "" && t.Language == lang && t.Task == "general" && t.Profile == "" && t.Role == "":
		return "language"
	case profile != "" && t.Profile == profile && t.Task == "general" && t.Language == "" && t.Role == "":
		return "profile"
	case role != "" && t.Role == role && t.Task == "general" && t.Language == "" && t.Profile == "":
		return "role"
	default:
		return "extra"
	}
}

func templateScore(t Template, req RenderRequest) (int, bool) {
	typ := normalizeKey(req.Type)
	task := normalizeKey(req.Task)
	lang := normalizeKey(req.Language)
	profile := normalizeKey(req.Profile)
	role := normalizeKey(req.Role)

	if typ != "" && t.Type != typ {
		return 0, false
	}

	score := t.Priority

	if task != "" {
		switch {
		case t.Task == task:
			score += 100
		case t.Task == "general":
			score += 25
		default:
			return 0, false
		}
	}

	if lang != "" {
		switch {
		case t.Language == "":
			score += 5
		case t.Language == lang:
			score += 30
		default:
			return 0, false
		}
	}

	if profile != "" {
		switch {
		case t.Profile == "":
			score += 2
		case t.Profile == profile:
			score += 20
		default:
			return 0, false
		}
	}
	if role != "" {
		switch {
		case t.Role == "":
			score += 1
		case t.Role == role:
			score += 18
		default:
			return 0, false
		}
	}

	return score, true
}

func defaultFallbackPrompt(req RenderRequest) string {
	projectRoot := strings.TrimSpace(req.Vars["project_root"])
	task := strings.TrimSpace(req.Task)
	if task == "" {
		task = "general"
	}
	lang := strings.TrimSpace(req.Language)
	if lang == "" {
		lang = "generic"
	}
	if projectRoot == "" {
		return fmt.Sprintf("You are DFMC. Task=%s Language=%s. Be correct, concise, and safe.", task, lang)
	}
	return fmt.Sprintf("You are DFMC for project %s. Task=%s Language=%s. Be correct, concise, and safe.", projectRoot, task, lang)
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func renderBody(body string, vars map[string]string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	if vars == nil {
		return body
	}
	return placeholderRe.ReplaceAllStringFunc(body, func(match string) string {
		parts := placeholderRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return ""
		}
		return strings.TrimSpace(vars[parts[1]])
	})
}
