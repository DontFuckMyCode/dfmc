package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// BuildMagicDoc builds a concise project brief optimized for low-token context reuse.
func (e *Engine) BuildMagicDoc(ctx context.Context, title string, hotspotLimit, depLimit, recentLimit int) (string, error) {
	if hotspotLimit <= 0 {
		hotspotLimit = 8
	}
	if depLimit <= 0 {
		depLimit = 8
	}
	if recentLimit <= 0 {
		recentLimit = 5
	}
	if strings.TrimSpace(title) == "" {
		title = "DFMC Project Brief"
	}

	report, err := e.AnalyzeWithOptions(ctx, AnalyzeOptions{Path: e.ProjectRoot})
	if err != nil {
		return "", err
	}

	hotspots := report.HotSpots
	if len(hotspots) > hotspotLimit {
		hotspots = hotspots[:hotspotLimit]
	}
	deps := e.collectDependencyStats(depLimit)
	toolsList := e.ListTools()
	sort.Strings(toolsList)

	rawSkills := skills.Discover(e.ProjectRoot)
	skillNames := make([]string, 0, len(rawSkills))
	for _, s := range rawSkills {
		skillNames = append(skillNames, s.Name)
	}
	sort.Strings(skillNames)

	w := e.MemoryWorking()
	recentFiles := clipList(w.RecentFiles, recentLimit)

	active := e.ConversationActive()
	conversationID := "(none)"
	conversationBranch := "(none)"
	messageCount := 0
	recentUser := []string{}
	recentAssistant := []string{}
	if active != nil {
		conversationID = strings.TrimSpace(active.ID)
		conversationBranch = strings.TrimSpace(active.Branch)
		msgs := active.Messages()
		messageCount = len(msgs)
		recentUser = recentMessages(msgs, types.RoleUser, recentLimit)
		recentAssistant = recentMessages(msgs, types.RoleAssistant, recentLimit)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# MAGIC DOC: %s\n\n", title)
	b.WriteString("_Current-state project brief optimized for low-token context reuse._\n\n")

	b.WriteString("## Current State\n")
	fmt.Fprintf(&b, "- Generated at: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Project root: `%s`\n", filepath.ToSlash(e.ProjectRoot))
	st := e.Status()
	fmt.Fprintf(&b, "- Provider/model: `%s` / `%s`\n", st.Provider, st.Model)
	fmt.Fprintf(&b, "- Source files scanned: %d\n", report.Files)
	fmt.Fprintf(&b, "- Graph: nodes=%d edges=%d cycles=%d\n", report.Nodes, report.Edges, report.Cycles)

	b.WriteString("\n## Hotspots\n")
	if len(hotspots) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, n := range hotspots {
			b.WriteString("- " + formatHotspot(e.ProjectRoot, n) + "\n")
		}
	}

	b.WriteString("\n## Top Dependencies\n")
	if len(deps) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, d := range deps {
			fmt.Fprintf(&b, "- `%s` (%d imports)\n", d.Module, d.Count)
		}
	}

	b.WriteString("\n## Conversation Snapshot\n")
	fmt.Fprintf(&b, "- Active conversation: `%s` (branch `%s`, %d messages)\n", fallback(conversationID, "(none)"), fallback(conversationBranch, "(none)"), messageCount)
	b.WriteString("- Recent user intents:\n")
	if len(recentUser) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentUser {
			b.WriteString("  - " + item + "\n")
		}
	}
	b.WriteString("- Recent assistant outcomes:\n")
	if len(recentAssistant) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentAssistant {
			b.WriteString("  - " + item + "\n")
		}
	}

	b.WriteString("\n## Active Surface\n")
	b.WriteString("- Recent context files:\n")
	if len(recentFiles) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, p := range recentFiles {
			b.WriteString("  - `" + toProjectRelative(e.ProjectRoot, p) + "`\n")
		}
	}
	b.WriteString("- Registered tools:\n")
	if len(toolsList) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipList(toolsList, 16) {
			b.WriteString("  - `" + name + "`\n")
		}
	}
	b.WriteString("- Available skills:\n")
	if len(skillNames) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipList(skillNames, 16) {
			b.WriteString("  - `" + name + "`\n")
		}
	}

	b.WriteString("\n## Workflow\n")
	b.WriteString("- Build: `go build ./cmd/dfmc`\n")
	b.WriteString("- Tests: `go test ./...`\n")
	b.WriteString("- Refresh this file: `dfmc magicdoc update`\n")

	return b.String(), nil
}

// maybeAutoUpdateMagicDoc checks if the magic doc is missing or older than
// 24 hours and updates it if so. Runs as a non-blocking background task.
func (e *Engine) maybeAutoUpdateMagicDoc(ctx context.Context) {
	if e.ProjectRoot == "" {
		return
	}
	path := filepath.Join(e.ProjectRoot, ".dfmc", "magic", "MAGIC_DOC.md")
	info, err := os.Stat(path)
	if err == nil {
		// If it's fresh (less than 24h old), skip.
		if time.Since(info.ModTime()) < 24*time.Hour {
			return
		}
	}

	// Update in background
	go func() {
		content, err := e.BuildMagicDoc(ctx, "DFMC Project Brief", 8, 8, 5)
		if err != nil {
			return
		}
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(content), 0o644)
	}()
}

type depStat struct {
	Module string
	Count  int
}

func (e *Engine) collectDependencyStats(limit int) []depStat {
	if e == nil || e.CodeMap == nil || e.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, edge := range e.CodeMap.Graph().Edges() {
		if edge.Type != "imports" {
			continue
		}
		mod := strings.TrimPrefix(edge.To, "module:")
		mod = strings.TrimSpace(mod)
		if mod == "" {
			continue
		}
		counts[mod]++
	}
	out := make([]depStat, 0, len(counts))
	for mod, count := range counts {
		out = append(out, depStat{Module: mod, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Module < out[j].Module
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func formatHotspot(projectRoot string, n codemap.Node) string {
	name := strings.TrimSpace(n.Name)
	if name == "" {
		name = strings.TrimSpace(n.ID)
	}
	kind := strings.TrimSpace(n.Kind)
	path := toProjectRelative(projectRoot, strings.TrimSpace(n.Path))

	parts := make([]string, 0, 3)
	if name != "" {
		parts = append(parts, fmt.Sprintf("`%s`", name))
	}
	if kind != "" {
		parts = append(parts, "kind="+kind)
	}
	if path != "" {
		parts = append(parts, "path=`"+path+"`")
	}
	if len(parts) == 0 {
		return "(unknown hotspot)"
	}
	return strings.Join(parts, " ")
}

func recentMessages(messages []types.Message, role types.MessageRole, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		if messages[i].Role != role {
			continue
		}
		line := truncateLine(messages[i].Content, 160)
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func toProjectRelative(root, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	absP, errP := filepath.Abs(p)
	absR, errR := filepath.Abs(strings.TrimSpace(root))
	if errP == nil && errR == nil && strings.TrimSpace(absR) != "" {
		if rel, err := filepath.Rel(absR, absP); err == nil {
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(p)
}

func clipList(list []string, limit int) []string {
	if limit <= 0 || len(list) <= limit {
		out := make([]string, len(list))
		copy(out, list)
		return out
	}
	out := make([]string, limit)
	copy(out, list[:limit])
	return out
}

func fallback(v, alt string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return alt
	}
	return v
}
