package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const defaultMagicDocRelPath = ".dfmc/magic/MAGIC_DOC.md"

func runMagicDoc(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"update"}
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))

	switch action {
	case "update", "sync", "generate":
		fs := flag.NewFlagSet("magicdoc update", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		pathFlag := fs.String("path", "", "target magic doc path")
		titleFlag := fs.String("title", "DFMC Project Brief", "document title")
		hotspotsFlag := fs.Int("hotspots", 8, "max hotspot entries")
		depsFlag := fs.Int("deps", 8, "max dependency entries")
		recentFlag := fs.Int("recent", 5, "max recent items per section")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		st := eng.Status()
		root := strings.TrimSpace(st.ProjectRoot)
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc: cannot resolve project root: %v\n", err)
				return 1
			}
			root = cwd
		}

		target := resolveMagicDocPath(root, strings.TrimSpace(*pathFlag))
		content, err := buildMagicDocContent(ctx, eng, root, strings.TrimSpace(*titleFlag), *hotspotsFlag, *depsFlag, *recentFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc build failed: %v\n", err)
			return 1
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc mkdir failed: %v\n", err)
			return 1
		}

		previous, _ := os.ReadFile(target)
		changed := string(previous) != content
		if changed {
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc write failed: %v\n", err)
				return 1
			}
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"status":  "ok",
				"path":    target,
				"updated": changed,
				"bytes":   len(content),
			})
			return 0
		}
		fmt.Printf("magicdoc %s: %s\n", map[bool]string{true: "updated", false: "unchanged"}[changed], target)
		return 0

	case "show", "cat":
		fs := flag.NewFlagSet("magicdoc show", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		pathFlag := fs.String("path", "", "target magic doc path")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		st := eng.Status()
		root := strings.TrimSpace(st.ProjectRoot)
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc: cannot resolve project root: %v\n", err)
				return 1
			}
			root = cwd
		}

		target := resolveMagicDocPath(root, strings.TrimSpace(*pathFlag))
		data, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc read failed: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"path":    target,
				"content": string(data),
			})
			return 0
		}
		fmt.Print(string(data))
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc magicdoc [update|show] [--path <file>] [--title <title>]")
		return 2
	}
}

func resolveMagicDocPath(projectRoot, pathFlag string) string {
	if strings.TrimSpace(pathFlag) == "" {
		return filepath.Join(projectRoot, filepath.FromSlash(defaultMagicDocRelPath))
	}
	if filepath.IsAbs(pathFlag) {
		return pathFlag
	}
	return filepath.Join(projectRoot, pathFlag)
}

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
			b.WriteString("- " + formatHotspot(projectRoot, n) + "\n")
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
			b.WriteString("  - `" + toProjectRelative(projectRoot, p) + "`\n")
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
