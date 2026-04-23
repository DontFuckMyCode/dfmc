// Memory & Scan CLI: `dfmc memory [working|list|search|add|clear]` and
// `dfmc scan [path]`. Extracted from cli_analysis.go. Unrelated
// surfaces but both small and self-contained, so they share a sibling
// file to avoid a forest of 60-line extractions.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

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
