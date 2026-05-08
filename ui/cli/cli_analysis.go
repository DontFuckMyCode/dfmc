// Code-analysis subcommands: analyze, map, tool, memory, scan,
// conversation, prompt, and context. Extracted from cli.go so the
// dispatcher stays focused. These commands share dependency-graph
// helpers (codemap DOT/SVG rendering) and promptlib/context-manager
// plumbing so they travel together.
//
// File layout: this file owns runAnalyze (with its --magicdoc side
// pipeline) + runMap. runTool lives in cli_analysis_tool.go;
// dependency-graph rendering (collectDependencyStats + graphToDOT +
// graphToSVG + escapeDOT + xmlEscape + depStat) lives in
// cli_analysis_graph.go.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
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
