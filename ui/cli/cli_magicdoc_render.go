// cli_magicdoc_render.go — Markdown brief builder for `dfmc magicdoc
// update`. Sibling of cli_magicdoc.go which keeps the subcommand
// dispatcher (runMagicDoc) + defaultMagicDocRelPath + the
// resolveMagicDocPath path helper.
//
// Splitting the renderer out keeps cli_magicdoc.go scannable when
// adjusting flag wiring or the show/cat path, and groups the
// project-state extraction + per-section formatters
// (formatHotspot, recentMessages, toProjectRelative, clipList,
// fallback) together with buildMagicDocContent.

package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func buildMagicDocContent(ctx context.Context, eng *engine.Engine, projectRoot, title string, hotspotLimit, depLimit, recentLimit int) (string, error) {
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

	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{Path: projectRoot})
	if err != nil {
		return "", err
	}

	hotspots := report.HotSpots
	if len(hotspots) > hotspotLimit {
		hotspots = hotspots[:hotspotLimit]
	}
	deps := collectDependencyStats(eng, depLimit)
	toolsList := eng.ListTools()
	sort.Strings(toolsList)
	skills := discoverSkills(projectRoot)
	skillNames := make([]string, 0, len(skills))
	for _, s := range skills {
		skillNames = append(skillNames, s.Name)
	}
	sort.Strings(skillNames)

	w := eng.MemoryWorking()
	recentFiles := clipList(w.RecentFiles, recentLimit)

	active := eng.ConversationActive()
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
	fmt.Fprintf(&b, "- Project root: `%s`\n", filepath.ToSlash(projectRoot))
	fmt.Fprintf(&b, "- Provider/model: `%s` / `%s`\n", eng.Status().Provider, eng.Status().Model)
	fmt.Fprintf(&b, "- Source files scanned: %d\n", report.Files)
	fmt.Fprintf(&b, "- Graph: nodes=%d edges=%d cycles=%d\n", report.Nodes, report.Edges, report.Cycles)

	b.WriteString("\n## Hotspots\n")
	if len(hotspots) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, n := range hotspots {
			b.WriteString("- ")
			b.WriteString(formatHotspot(projectRoot, n))
			b.WriteByte('\n')
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
			b.WriteString("  - ")
			b.WriteString(item)
			b.WriteByte('\n')
		}
	}
	b.WriteString("- Recent assistant outcomes:\n")
	if len(recentAssistant) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentAssistant {
			b.WriteString("  - ")
			b.WriteString(item)
			b.WriteByte('\n')
		}
	}

	b.WriteString("\n## Active Surface\n")
	b.WriteString("- Recent context files:\n")
	if len(recentFiles) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, p := range recentFiles {
			fmt.Fprintf(&b, "  - `%s`\n", toProjectRelative(projectRoot, p))
		}
	}
	b.WriteString("- Registered tools:\n")
	if len(toolsList) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipList(toolsList, 16) {
			fmt.Fprintf(&b, "  - `%s`\n", name)
		}
	}
	b.WriteString("- Available skills:\n")
	if len(skillNames) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipList(skillNames, 16) {
			fmt.Fprintf(&b, "  - `%s`\n", name)
		}
	}

	b.WriteString("\n## Workflow\n")
	b.WriteString("- Build: `go build ./cmd/dfmc`\n")
	b.WriteString("- Tests: `go test ./...`\n")
	b.WriteString("- Context budget preview: `go run ./cmd/dfmc context budget --query \"security audit\"`\n")
	b.WriteString("- Prompt preview: `go run ./cmd/dfmc prompt render --query \"review auth module\"`\n")
	b.WriteString("- Refresh this file: `go run ./cmd/dfmc magicdoc update`\n")

	return b.String(), nil
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
