package web

// server_context_magicdoc.go — magic-doc generation pipeline shared by
// /api/v1/context/brief/update and the /api/v1/analyze MagicDoc=true path.
// Companion siblings:
//
//   - server_context.go         HTTP handler bodies + runtimeHintsFromQuery
//   - server_context_helpers.go path/string/list utilities reused by handlers
//
// updateMagicDoc is the write-side: build content from engine state +
// AnalyzeWithOptions output, write to disk if changed, return a small
// status payload. buildMagicDocContentForWeb does the rendering;
// collectDependencyStatsForWeb mines import edges; recentMessagesForWeb
// pulls the conversation tail.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (s *Server) updateMagicDoc(ctx context.Context, root string, req MagicDocUpdateRequest) (map[string]any, error) {
	target := resolveMagicDocPath(root, strings.TrimSpace(req.Path))
	content, err := buildMagicDocContentForWeb(ctx, s.engine, root, strings.TrimSpace(req.Title), req.Hotspots, req.Deps, req.Recent)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	prev, _ := os.ReadFile(target)
	updated := string(prev) != content
	if updated {
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"status":  "ok",
		"path":    filepath.ToSlash(target),
		"updated": updated,
		"bytes":   len(content),
	}, nil
}

type webDepStat struct {
	Module string
	Count  int
}

func collectDependencyStatsForWeb(eng *engine.Engine, limit int) []webDepStat {
	if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, e := range eng.CodeMap.Graph().Edges() {
		if e.Type != "imports" {
			continue
		}
		mod := strings.TrimSpace(strings.TrimPrefix(e.To, "module:"))
		if mod == "" {
			continue
		}
		counts[mod]++
	}
	out := make([]webDepStat, 0, len(counts))
	for mod, count := range counts {
		out = append(out, webDepStat{Module: mod, Count: count})
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

func buildMagicDocContentForWeb(ctx context.Context, eng *engine.Engine, projectRoot, title string, hotspotLimit, depLimit, recentLimit int) (string, error) {
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
	deps := collectDependencyStatsForWeb(eng, depLimit)
	toolsList := eng.ListTools()
	sort.Strings(toolsList)

	w := eng.MemoryWorking()
	recentFiles := clipStringListForWeb(w.RecentFiles, recentLimit)

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
		recentUser = recentMessagesForWeb(msgs, types.RoleUser, recentLimit)
		recentAssistant = recentMessagesForWeb(msgs, types.RoleAssistant, recentLimit)
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
			name := strings.TrimSpace(n.Name)
			if name == "" {
				name = strings.TrimSpace(n.ID)
			}
			kind := strings.TrimSpace(n.Kind)
			path := relativeProjectPathForWeb(projectRoot, strings.TrimSpace(n.Path))
			line := "- `" + name + "`"
			if kind != "" {
				line += " kind=" + kind
			}
			if path != "" {
				line += " path=`" + path + "`"
			}
			b.WriteString(line + "\n")
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
	fmt.Fprintf(&b, "- Active conversation: `%s` (branch `%s`, %d messages)\n", fallbackStringForWeb(conversationID, "(none)"), fallbackStringForWeb(conversationBranch, "(none)"), messageCount)
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
			b.WriteString("  - `" + relativeProjectPathForWeb(projectRoot, p) + "`\n")
		}
	}
	b.WriteString("- Registered tools:\n")
	if len(toolsList) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipStringListForWeb(toolsList, 16) {
			b.WriteString("  - `" + name + "`\n")
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

func recentMessagesForWeb(messages []types.Message, role types.MessageRole, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		if messages[i].Role != role {
			continue
		}
		text := strings.TrimSpace(strings.ReplaceAll(messages[i].Content, "\n", " "))
		if text == "" {
			continue
		}
		if len(text) > 160 {
			text = text[:160] + "..."
		}
		out = append(out, text)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
