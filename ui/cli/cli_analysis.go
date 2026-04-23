// Code-analysis subcommands: analyze, map, tool, memory, scan,
// conversation, prompt, and context. Extracted from cli.go so the
// dispatcher stays focused. These commands share dependency-graph
// helpers (codemap DOT/SVG rendering) and promptlib/context-manager
// plumbing so they travel together.

package cli

import (
	"context"
	"flag"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func runAnalyze(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var jsonFlag bool
	var full bool
	var security bool
	var complexity bool
	var deadCode bool
	var duplication bool
	var todos bool
	var deps bool
	var magicDoc bool
	var magicDocPath string
	var magicDocTitle string
	var magicDocHotspots int
	var magicDocDeps int
	var magicDocRecent int
	fs.BoolVar(&jsonFlag, "json", false, "output as json")
	fs.BoolVar(&full, "full", false, "run full analysis set")
	fs.BoolVar(&security, "security", false, "run security analysis")
	fs.BoolVar(&complexity, "complexity", false, "run complexity analysis")
	fs.BoolVar(&deadCode, "dead-code", false, "run dead code analysis")
	fs.BoolVar(&duplication, "duplication", false, "run code duplication analysis")
	fs.BoolVar(&todos, "todos", false, "collect TODO/FIXME/HACK/XXX/NOTE markers from comments")
	fs.BoolVar(&deps, "deps", false, "run dependency analysis summary")
	fs.BoolVar(&magicDoc, "magicdoc", false, "update .dfmc/magic/MAGIC_DOC.md after analyze")
	fs.StringVar(&magicDocPath, "magicdoc-path", "", "custom magic doc path")
	fs.StringVar(&magicDocTitle, "magicdoc-title", "DFMC Project Brief", "magic doc title")
	fs.IntVar(&magicDocHotspots, "magicdoc-hotspots", 8, "max hotspot entries for magic doc")
	fs.IntVar(&magicDocDeps, "magicdoc-deps", 8, "max dependency entries for magic doc")
	fs.IntVar(&magicDocRecent, "magicdoc-recent", 5, "max recent items for magic doc")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || jsonFlag

	path := ""
	if len(fs.Args()) > 0 {
		path = fs.Args()[0]
	}
	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{
		Path:        path,
		Full:        full,
		Security:    security,
		Complexity:  complexity,
		DeadCode:    deadCode,
		Duplication: duplication,
		Todos:       todos,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze failed: %v\n", err)
		return 1
	}
	depSummary := []depStat{}
	if deps || full {
		depSummary = collectDependencyStats(eng, 20)
	}
	magicDocResult := map[string]any{}
	if magicDoc {
		root := strings.TrimSpace(report.ProjectRoot)
		if root == "" {
			root = strings.TrimSpace(eng.Status().ProjectRoot)
		}
		if root == "" {
			if cwd, err := os.Getwd(); err == nil {
				root = cwd
			}
		}
		target := resolveMagicDocPath(root, strings.TrimSpace(magicDocPath))
		content, err := buildMagicDocContent(ctx, eng, root, strings.TrimSpace(magicDocTitle), magicDocHotspots, magicDocDeps, magicDocRecent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc build failed: %v\n", err)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc mkdir failed: %v\n", err)
			return 1
		}
		prev, _ := os.ReadFile(target)
		updated := string(prev) != content
		if updated {
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc write failed: %v\n", err)
				return 1
			}
		}
		magicDocResult = map[string]any{
			"path":    target,
			"updated": updated,
			"bytes":   len(content),
		}
	}
	if jsonMode {
		if deps || full || magicDoc {
			out := map[string]any{
				"report": report,
			}
			if deps || full {
				out["dependencies"] = depSummary
				out["dep_count"] = len(depSummary)
				out["dep_requested"] = true
			}
			if magicDoc {
				out["magicdoc"] = magicDocResult
			}
			mustPrintJSON(out)
			return 0
		}
		mustPrintJSON(report)
		return 0
	}
	fmt.Printf("Project: %s\n", report.ProjectRoot)
	fmt.Printf("Files:   %d\n", report.Files)
	fmt.Printf("Nodes:   %d\n", report.Nodes)
	fmt.Printf("Edges:   %d\n", report.Edges)
	fmt.Printf("Cycles:  %d\n", report.Cycles)
	if len(report.HotSpots) > 0 {
		fmt.Println("Hot spots:")
		for i, n := range report.HotSpots {
			if i >= 5 {
				break
			}
			fmt.Printf("  - %s (%s)\n", n.Name, n.Kind)
		}
	}
	if report.Security != nil {
		fmt.Printf("Security: secrets=%d vulns=%d\n", len(report.Security.Secrets), len(report.Security.Vulnerabilities))
	}
	if report.Complexity != nil {
		fmt.Printf("Complexity: avg=%.2f max=%d functions=%d\n", report.Complexity.Average, report.Complexity.Max, len(report.Complexity.TopFunctions))
	}
	if len(report.DeadCode) > 0 {
		fmt.Printf("Dead code candidates: %d\n", len(report.DeadCode))
	}
	if report.Duplication != nil {
		d := report.Duplication
		fmt.Printf("Duplication: groups=%d duplicated_lines=%d (min=%d)\n",
			len(d.Groups), d.DuplicatedLines, d.MinLines)
		for i, g := range d.Groups {
			if i >= 5 {
				remaining := len(d.Groups) - 5
				if remaining > 0 {
					fmt.Printf("  … %d more groups (use --json for the full list)\n", remaining)
				}
				break
			}
			fmt.Printf("  - %d lines x %d locations\n", g.Length, len(g.Locations))
			for j, loc := range g.Locations {
				if j >= 3 {
					remaining := len(g.Locations) - 3
					if remaining > 0 {
						fmt.Printf("      … %d more locations\n", remaining)
					}
					break
				}
				fmt.Printf("      %s:%d-%d\n", loc.File, loc.StartLine, loc.EndLine)
			}
		}
	}
	if report.Todos != nil && report.Todos.Total > 0 {
		td := report.Todos
		fmt.Printf("Todos: %d markers", td.Total)
		if len(td.Kinds) > 0 {
			// Stable alphabetical order so the line is deterministic.
			keys := make([]string, 0, len(td.Kinds))
			for k := range td.Kinds {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%d", k, td.Kinds[k]))
			}
			fmt.Printf(" (%s)", strings.Join(parts, " "))
		}
		fmt.Println()
		for i, item := range td.Items {
			if i >= 5 {
				remaining := len(td.Items) - 5
				if remaining > 0 {
					fmt.Printf("  … %d more (use --json for the full list)\n", remaining)
				}
				break
			}
			fmt.Printf("  - [%s] %s:%d %s\n", item.Kind, item.File, item.Line, item.Text)
		}
	}
	if deps || full {
		fmt.Printf("Dependencies: %d\n", len(depSummary))
		for i, d := range depSummary {
			if i >= 10 {
				break
			}
			fmt.Printf("  - %s (%d imports)\n", d.Module, d.Count)
		}
	}
	if magicDoc {
		fmt.Printf("MagicDoc: %s (%s)\n", fmt.Sprint(magicDocResult["path"]), map[bool]string{true: "updated", false: "unchanged"}[magicDocResult["updated"] == true])
	}
	return 0
}

func runMap(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	format := fs.String("format", "ascii", "ascii|json|dot|svg")
	jsonFlag := fs.Bool("json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) > 0 {
		*format = fs.Args()[0]
	}
	jsonMode = jsonMode || *jsonFlag
	f := strings.ToLower(*format)
	_, _ = eng.Analyze(ctx, "")

	graph := eng.CodeMap.Graph()
	if graph == nil {
		fmt.Fprintln(os.Stderr, "codemap is not initialized")
		return 1
	}

	if jsonMode || f == "json" {
		_ = printJSON(map[string]any{
			"nodes": graph.Nodes(),
			"edges": graph.Edges(),
		})
		return 0
	}
	if f == "dot" {
		fmt.Println(graphToDOT(graph.Nodes(), graph.Edges()))
		return 0
	}
	if f == "svg" {
		fmt.Println(graphToSVG(graph.Nodes(), graph.Edges()))
		return 0
	}

	for _, e := range graph.Edges() {
		fmt.Printf("%s -> %s (%s)\n", e.From, e.To, e.Type)
	}
	return 0
}

func runTool(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 || args[0] == "list" {
		tools := eng.ListTools()
		if jsonMode {
			mustPrintJSON(map[string]any{"tools": tools})
			return 0
		}
		// Show one line per tool with a short summary pulled from its
		// ToolSpec. Keeps text mode readable without requiring a follow-
		// up `dfmc tool show NAME` just to learn what each verb does.
		var specs map[string]string
		if eng.Tools != nil {
			specs = map[string]string{}
			for _, s := range eng.Tools.Specs() {
				specs[s.Name] = strings.TrimSpace(s.Summary)
			}
		}
		for _, t := range tools {
			summary := ""
			if specs != nil {
				summary = specs[t]
			}
			if summary != "" {
				fmt.Printf("%-18s %s\n", t, summary)
			} else {
				fmt.Println(t)
			}
		}
		return 0
	}

	// `dfmc tool show NAME` — print the ToolSpec so operators can see
	// the parameter shape before invoking `dfmc tool run` blind. This
	// fills the gap where previously you had to grep the source to
	// discover what args a tool accepts.
	if args[0] == "show" || args[0] == "describe" || args[0] == "inspect" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc tool show <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		if eng.Tools == nil {
			fmt.Fprintln(os.Stderr, "tools engine not initialized")
			return 1
		}
		spec, ok := eng.Tools.Spec(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown tool: %s\n", name)
			return 1
		}
		if jsonMode {
			mustPrintJSON(spec)
			return 0
		}
		printToolSpec(spec)
		return 0
	}

	if args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool [list|show <name>|run <name> [--key value ...]]")
		return 2
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dfmc tool run <name> [--key value ...]")
		return 2
	}
	name := args[1]
	params := map[string]any{}
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		part := rest[i]
		if !strings.HasPrefix(part, "--") {
			continue
		}
		key := strings.TrimPrefix(part, "--")
		val := "true"
		if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
			val = rest[i+1]
			i++
		}
		params[key] = val
	}

	res, err := eng.CallTool(ctx, name, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tool error: %v\n", err)
		return 1
	}
	if jsonMode {
		mustPrintJSON(res)
		return 0
	}
	if strings.TrimSpace(res.Output) != "" {
		fmt.Println(res.Output)
	}
	return 0
}

func runMemory(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"working"}
	}
	cmd := args[0]
	switch cmd {
	case "working":
		w := eng.MemoryWorking()
		if jsonMode {
			mustPrintJSON(w)
			return 0
		}
		fmt.Printf("Last question: %s\n", w.LastQuestion)
		fmt.Printf("Last answer: %s\n", truncateLine(w.LastAnswer, 160))
		fmt.Printf("Recent files: %d\n", len(w.RecentFiles))
		fmt.Printf("Recent symbols: %d\n", len(w.RecentSymbols))
		return 0
	case "list":
		fs := flag.NewFlagSet("memory list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		limit := fs.Int("limit", 20, "max entries")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		items, err := eng.MemoryList(parseTier(*tierS), *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory list error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(items)
			return 0
		}
		for _, e := range items {
			fmt.Printf("- %s | %s | %s\n", e.ID, e.Key, truncateLine(e.Value, 120))
		}
		return 0
	case "search":
		fs := flag.NewFlagSet("memory search", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		limit := fs.Int("limit", 20, "max entries")
		query := fs.String("query", "", "search query")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
			*query = strings.Join(fs.Args(), " ")
		}
		items, err := eng.MemorySearch(*query, parseTier(*tierS), *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory search error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(items)
			return 0
		}
		for _, e := range items {
			fmt.Printf("- %s | %s | %s\n", e.ID, e.Key, truncateLine(e.Value, 120))
		}
		return 0
	case "add":
		fs := flag.NewFlagSet("memory add", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		key := fs.String("key", "", "memory key")
		value := fs.String("value", "", "memory value")
		category := fs.String("category", "note", "memory category")
		conf := fs.Float64("confidence", 0.7, "confidence 0..1")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *key == "" || *value == "" {
			fmt.Fprintln(os.Stderr, "memory add requires --key and --value")
			return 2
		}
		entry := types.MemoryEntry{
			Tier:       parseTier(*tierS),
			Category:   *category,
			Key:        *key,
			Value:      *value,
			Confidence: *conf,
			Project:    eng.Status().ProjectRoot,
		}
		if err := eng.MemoryAdd(entry); err != nil {
			fmt.Fprintf(os.Stderr, "memory add error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"status": "ok"})
		} else {
			fmt.Println("memory entry added")
		}
		return 0
	case "clear":
		fs := flag.NewFlagSet("memory clear", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		tierS := fs.String("tier", "episodic", "episodic|semantic")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if err := eng.MemoryClear(parseTier(*tierS)); err != nil {
			fmt.Fprintf(os.Stderr, "memory clear error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(map[string]any{"status": "ok"})
		} else {
			fmt.Println("memory cleared")
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc memory [working|list|search|add|clear]")
		return 2
	}
}

func runScan(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var jsonFlag bool
	fs.BoolVar(&jsonFlag, "json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || jsonFlag
	path := ""
	if len(fs.Args()) > 0 {
		path = fs.Args()[0]
	}

	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{
		Path:     path,
		Security: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		return 1
	}
	if report.Security == nil {
		fmt.Println("No security report generated.")
		return 0
	}
	if jsonMode {
		mustPrintJSON(report.Security)
		return 0
	}
	fmt.Printf("Scanned files: %d\n", report.Security.FilesScanned)
	fmt.Printf("Secrets: %d\n", len(report.Security.Secrets))
	for i, f := range report.Security.Secrets {
		if i >= 10 {
			break
		}
		fmt.Printf("  - [%s] %s:%d %s (%s)\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Pattern, f.Match)
	}
	fmt.Printf("Vulnerabilities: %d\n", len(report.Security.Vulnerabilities))
	// Severity breakdown before the sample: real audits care about the
	// counts more than the first finding.
	sevCounts := map[string]int{}
	for _, f := range report.Security.Vulnerabilities {
		sevCounts[strings.ToLower(strings.TrimSpace(f.Severity))]++
	}
	if len(report.Security.Vulnerabilities) > 0 {
		fmt.Printf("  severity: high=%d medium=%d low=%d info=%d\n",
			sevCounts["high"]+sevCounts["critical"],
			sevCounts["medium"],
			sevCounts["low"],
			sevCounts["info"])
	}
	for i, f := range report.Security.Vulnerabilities {
		if i >= 10 {
			remaining := len(report.Security.Vulnerabilities) - 10
			if remaining > 0 {
				fmt.Printf("  ... +%d more (use --json for the full list)\n", remaining)
			}
			break
		}
		tag := f.CWE
		if f.OWASP != "" {
			tag = f.CWE + " / " + f.OWASP
		}
		fmt.Printf("  - [%s] %s:%d %s | %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Kind, tag)
	}
	return 0
}


type depStat struct {
	Module string `json:"module"`
	Count  int    `json:"count"`
}

func collectDependencyStats(eng *engine.Engine, limit int) []depStat {
	if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, e := range eng.CodeMap.Graph().Edges() {
		if e.Type != "imports" {
			continue
		}
		mod := strings.TrimPrefix(e.To, "module:")
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

func graphToDOT(nodes []codemap.Node, edges []codemap.Edge) string {
	var b strings.Builder
	b.WriteString("digraph DFMC {\n")
	for _, n := range nodes {
		label := n.Name
		if strings.TrimSpace(label) == "" {
			label = n.ID
		}
		if n.Kind != "" {
			label = label + "\\n(" + n.Kind + ")"
		}
		fmt.Fprintf(&b, "  \"%s\" [label=\"%s\"];\n", escapeDOT(n.ID), escapeDOT(label))
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  \"%s\" -> \"%s\" [label=\"%s\"];\n",
			escapeDOT(e.From), escapeDOT(e.To), escapeDOT(e.Type))
	}
	b.WriteString("}\n")
	return b.String()
}

func graphToSVG(nodes []codemap.Node, edges []codemap.Edge) string {
	const (
		width     = 1200.0
		height    = 800.0
		margin    = 90.0
		nodeR     = 14.0
		fontSize  = 12
		labelDy   = 24.0
		strokeW   = 1.2
		centerPad = 20.0
	)

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="800" viewBox="0 0 1200 800">` + "\n")
	b.WriteString(`  <defs><style>
    .edge { stroke: #64748b; stroke-width: 1.2; opacity: 0.8; }
    .node { fill: #0ea5e9; stroke: #075985; stroke-width: 1.2; }
    .label { fill: #0f172a; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; text-anchor: middle; }
    .kind { fill: #334155; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; text-anchor: middle; }
  </style></defs>` + "\n")
	b.WriteString(`  <rect x="0" y="0" width="1200" height="800" fill="#f8fafc"/>` + "\n")

	if len(nodes) == 0 {
		b.WriteString(`  <text x="600" y="400" class="label">No codemap nodes</text>` + "\n")
		b.WriteString(`</svg>` + "\n")
		return b.String()
	}

	type pt struct {
		x float64
		y float64
	}
	pos := make(map[string]pt, len(nodes))
	cx := width / 2
	cy := height / 2
	r := math.Min(width, height)/2 - margin
	if len(nodes) == 1 {
		pos[nodes[0].ID] = pt{x: cx, y: cy}
	} else {
		for i, n := range nodes {
			angle := (2 * math.Pi * float64(i) / float64(len(nodes))) - math.Pi/2
			x := cx + (r-centerPad)*math.Cos(angle)
			y := cy + (r-centerPad)*math.Sin(angle)
			pos[n.ID] = pt{x: x, y: y}
		}
	}

	for _, e := range edges {
		from, okFrom := pos[e.From]
		to, okTo := pos[e.To]
		if !okFrom || !okTo {
			continue
		}
		fmt.Fprintf(&b, `  <line class="edge" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`+"\n",
			from.x, from.y, to.x, to.y)
	}

	for _, n := range nodes {
		p := pos[n.ID]
		label := strings.TrimSpace(n.Name)
		if label == "" {
			label = n.ID
		}
		kind := strings.TrimSpace(n.Kind)
		fmt.Fprintf(&b, `  <circle class="node" cx="%.1f" cy="%.1f" r="%.1f"/>`+"\n", p.x, p.y, nodeR)
		fmt.Fprintf(&b, `  <text class="label" x="%.1f" y="%.1f">%s</text>`+"\n", p.x, p.y+labelDy, xmlEscape(label))
		if kind != "" {
			fmt.Fprintf(&b, `  <text class="kind" x="%.1f" y="%.1f">%s</text>`+"\n", p.x, p.y+labelDy+12, xmlEscape(kind))
		}
	}

	b.WriteString(`</svg>` + "\n")
	return b.String()
}

func xmlEscape(s string) string {
	return html.EscapeString(s)
}

func escapeDOT(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}


func runContext(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"budget"}
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))

	switch action {
	case "budget", "show":
		fs := flag.NewFlagSet("context budget", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		query := fs.String("query", "", "query for task-aware budget simulation")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for budget simulation")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for budget simulation")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for budget simulation")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
			*query = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		runtimeHints := eng.PromptRuntime()
		if p := strings.TrimSpace(*runtimeProvider); p != "" {
			runtimeHints.Provider = p
		}
		if m := strings.TrimSpace(*runtimeModel); m != "" {
			runtimeHints.Model = m
		}
		if ts := strings.TrimSpace(*runtimeToolStyle); ts != "" {
			runtimeHints.ToolStyle = ts
		}
		if *runtimeMaxContext > 0 {
			runtimeHints.MaxContext = *runtimeMaxContext
		}
		preview := eng.ContextBudgetPreviewWithRuntime(*query, runtimeHints)
		if jsonMode {
			mustPrintJSON(preview)
			return 0
		}
		fmt.Printf("context budget: provider=%s model=%s task=%s mentions=%d scale[t=%.2f f=%.2f pf=%.2f] provider_max=%d available=%d reserve_total=%d reserve[prompt=%d history=%d response=%d tools=%d] total=%d per_file=%d history=%d files=%d compression=%s tests=%t docs=%t\n",
			preview.Provider,
			preview.Model,
			preview.Task,
			preview.ExplicitFileMentions,
			preview.TaskTotalScale,
			preview.TaskFileScale,
			preview.TaskPerFileScale,
			preview.ProviderMaxContext,
			preview.ContextAvailableTokens,
			preview.ReserveTotalTokens,
			preview.ReservePromptTokens,
			preview.ReserveHistoryTokens,
			preview.ReserveResponseTokens,
			preview.ReserveToolTokens,
			preview.MaxTokensTotal,
			preview.MaxTokensPerFile,
			preview.MaxHistoryTokens,
			preview.MaxFiles,
			preview.Compression,
			preview.IncludeTests,
			preview.IncludeDocs,
		)
		return 0

	case "recommend", "recommendations":
		fs := flag.NewFlagSet("context recommend", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		query := fs.String("query", "", "query for context tuning recommendations")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for recommendation simulation")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for recommendation simulation")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for recommendation simulation")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
			*query = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		runtimeHints := eng.PromptRuntime()
		if p := strings.TrimSpace(*runtimeProvider); p != "" {
			runtimeHints.Provider = p
		}
		if m := strings.TrimSpace(*runtimeModel); m != "" {
			runtimeHints.Model = m
		}
		if ts := strings.TrimSpace(*runtimeToolStyle); ts != "" {
			runtimeHints.ToolStyle = ts
		}
		if *runtimeMaxContext > 0 {
			runtimeHints.MaxContext = *runtimeMaxContext
		}
		preview := eng.ContextBudgetPreviewWithRuntime(*query, runtimeHints)
		recs := eng.ContextRecommendationsWithRuntime(*query, runtimeHints)
		tuning := eng.ContextTuningSuggestionsWithRuntime(*query, runtimeHints)
		if jsonMode {
			_ = printJSON(map[string]any{
				"query":              strings.TrimSpace(*query),
				"preview":            preview,
				"recommendations":    recs,
				"tuning_suggestions": tuning,
			})
			return 0
		}
		fmt.Printf("context recommend: task=%s mentions=%d available=%d total=%d reserve=%d\n",
			preview.Task,
			preview.ExplicitFileMentions,
			preview.ContextAvailableTokens,
			preview.MaxTokensTotal,
			preview.ReserveTotalTokens,
		)
		for _, rec := range recs {
			fmt.Printf("- [%s] %s: %s\n", strings.ToUpper(rec.Severity), rec.Code, rec.Message)
		}
		if len(tuning) > 0 {
			fmt.Println("tuning suggestions:")
			for _, s := range tuning {
				fmt.Printf("- [%s] %s=%v (%s)\n", strings.ToUpper(strings.TrimSpace(s.Priority)), s.Key, s.Value, s.Reason)
			}
		}
		return 0

	case "recent", "files":
		w := eng.MemoryWorking()
		if jsonMode {
			_ = printJSON(map[string]any{
				"count": len(w.RecentFiles),
				"files": w.RecentFiles,
			})
			return 0
		}
		if len(w.RecentFiles) == 0 {
			fmt.Println("context: no recent files yet")
			return 0
		}
		fmt.Println("recent context files:")
		for _, f := range w.RecentFiles {
			fmt.Printf("- %s\n", f)
		}
		return 0

	case "brief":
		fs := flag.NewFlagSet("context brief", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		maxWords := fs.Int("max-words", 240, "max words for context brief")
		pathFlag := fs.String("path", "", "path to magic doc file (relative to project root or absolute)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		projectRoot := strings.TrimSpace(eng.Status().ProjectRoot)
		if projectRoot == "" {
			fmt.Fprintln(os.Stderr, "context brief error: project root is not set")
			return 1
		}
		targetPath := resolvePromptBriefPath(projectRoot, strings.TrimSpace(*pathFlag))
		data, err := os.ReadFile(targetPath)
		exists := err == nil
		brief := loadPromptProjectBriefWithPath(projectRoot, strings.TrimSpace(*pathFlag), *maxWords)
		if brief == "" {
			brief = "(none)"
		}
		wordCount := len(strings.Fields(strings.TrimSpace(brief)))
		sizeBytes := 0
		if exists {
			sizeBytes = len(data)
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"path":       filepath.ToSlash(targetPath),
				"exists":     exists,
				"max_words":  *maxWords,
				"word_count": wordCount,
				"brief":      brief,
				"size_bytes": sizeBytes,
			})
			return 0
		}
		fmt.Printf("context brief: path=%s exists=%t words=%d max=%d bytes=%d\n", filepath.ToSlash(targetPath), exists, wordCount, *maxWords, sizeBytes)
		fmt.Println(brief)
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc context [budget --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[recommend --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[recent]|[brief --max-words 240 --path ...]")
		return 2
	}
}
