// slash_analyze.go — the /analyze and /scan slash commands. Both
// surfaces drive the same engine.AnalyzeWithOptions as the CLI so the
// TUI readout stays consistent with `dfmc analyze` / `dfmc scan`.
//
//   - runAnalyzeSlash: dispatcher (full vs security-only).
//   - formatAnalyzeReport: the full scorecard (hotspots, complexity,
//     dead code, duplication, todos).
//   - formatSecurityReport: the security-only slice (secrets + vulns
//     with a severity breakdown line).

package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// runAnalyzeSlash executes /analyze or /scan and returns a compact transcript
// entry. Both paths go through engine.AnalyzeWithOptions so results stay
// consistent with the CLI surface.
func (m Model) runAnalyzeSlash(args []string, securityOnly bool) Model {
	if m.eng == nil {
		return m.appendSystemMessage("Engine unavailable — cannot analyze.")
	}
	var paths []string
	opts := engine.AnalyzeOptions{}
	setFlags := false // true when individual --flags are passed
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" || a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			switch strings.ToLower(a) {
			case "--security":
				opts.Security = true
				setFlags = true
			case "--dead-code", "--deadcode":
				opts.DeadCode = true
				setFlags = true
			case "--complexity", "--complex":
				opts.Complexity = true
				setFlags = true
			case "--duplication", "--dup":
				opts.Duplication = true
				setFlags = true
			case "--todos", "--todo":
				opts.Todos = true
				setFlags = true
			case "--full":
				opts.Full = true
				setFlags = true
			case "--hotspots", "--hotspots-only":
				// hotspots are always included in full analysis;
				// there's no standalone flag, but accept the alias gracefully
				opts.Full = true
				setFlags = true
			default:
				return m.appendSystemMessage(fmt.Sprintf("Unknown /analyze flag %q. Known flags: --full, --security, --dead-code, --complexity, --duplication, --todos.", a))
			}
		} else {
			paths = append(paths, a)
		}
	}
	// Default: full analysis if no specific flags set.
	if !setFlags {
		if securityOnly {
			opts.Security = true
		} else {
			opts.Full = true
		}
	}
	path := strings.Join(paths, " ")
	opts.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := m.eng.AnalyzeWithOptions(ctx, opts)
	if err != nil {
		return m.appendSystemMessage("Analyze failed: " + err.Error())
	}
	if securityOnly {
		return m.appendSystemMessage(formatSecurityReport(report))
	}
	return m.appendSystemMessage(formatAnalyzeReport(report))
}

func formatAnalyzeReport(r engine.AnalyzeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Analyze: %d files, %d nodes, %d edges, %d cycles\n",
		r.Files, r.Nodes, r.Edges, r.Cycles)
	if len(r.HotSpots) > 0 {
		b.WriteString("Hotspots:\n")
		for i, h := range r.HotSpots {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  %d. %s (%s)\n", i+1, h.Name, h.Kind)
		}
	}
	if r.Security != nil && (len(r.Security.Secrets)+len(r.Security.Vulnerabilities)) > 0 {
		fmt.Fprintf(&b, "Security: %d secrets, %d vulns (scanned %d files)\n",
			len(r.Security.Secrets), len(r.Security.Vulnerabilities), r.Security.FilesScanned)
	}
	if r.Complexity != nil {
		fmt.Fprintf(&b, "Complexity: avg=%.2f max=%d (%d funcs scanned of %d symbols)\n",
			r.Complexity.Average, r.Complexity.Max,
			r.Complexity.ScannedSymbol, r.Complexity.TotalSymbols)
		for i, f := range r.Complexity.TopFunctions {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "  - %d %s (%s:%d)\n", f.Score, f.Name, f.File, f.Line)
		}
	}
	if len(r.DeadCode) > 0 {
		fmt.Fprintf(&b, "Dead code: %d candidates\n", len(r.DeadCode))
		for i, d := range r.DeadCode {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "  - %s (%s:%d)\n", d.Name, d.File, d.Line)
		}
	}
	if r.Duplication != nil && len(r.Duplication.Groups) > 0 {
		fmt.Fprintf(&b, "Duplication: %d groups, %d duplicated lines (min=%d)\n",
			len(r.Duplication.Groups), r.Duplication.DuplicatedLines, r.Duplication.MinLines)
		for i, g := range r.Duplication.Groups {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "  - %d lines x %d locations\n", g.Length, len(g.Locations))
		}
	}
	if r.Todos != nil && r.Todos.Total > 0 {
		kinds := make([]string, 0, len(r.Todos.Kinds))
		for k := range r.Todos.Kinds {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		parts := make([]string, 0, len(kinds))
		for _, k := range kinds {
			parts = append(parts, fmt.Sprintf("%s=%d", k, r.Todos.Kinds[k]))
		}
		fmt.Fprintf(&b, "Todos: %d markers (%s)\n", r.Todos.Total, strings.Join(parts, " "))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatSecurityReport(r engine.AnalyzeReport) string {
	if r.Security == nil {
		return "Scan produced no security report."
	}
	sec := r.Security
	var b strings.Builder
	fmt.Fprintf(&b, "Scan: %d files scanned\n", sec.FilesScanned)
	fmt.Fprintf(&b, "  Secrets: %d\n", len(sec.Secrets))
	for i, f := range sec.Secrets {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "    [%s] %s:%d %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Pattern)
	}
	// Severity breakdown — makes it easy to tell "0 high / 2 medium"
	// at a glance instead of scanning every finding line. Same shape
	// the CLI surface prints.
	counts := map[string]int{}
	for _, f := range sec.Vulnerabilities {
		counts[strings.ToLower(strings.TrimSpace(f.Severity))]++
	}
	fmt.Fprintf(&b, "  Vulnerabilities: %d\n", len(sec.Vulnerabilities))
	if len(sec.Vulnerabilities) > 0 {
		fmt.Fprintf(&b, "    severity: high=%d medium=%d low=%d info=%d\n",
			counts["high"]+counts["critical"],
			counts["medium"],
			counts["low"],
			counts["info"])
	}
	for i, f := range sec.Vulnerabilities {
		if i >= 5 {
			break
		}
		tag := f.CWE
		if f.OWASP != "" {
			tag = f.CWE + " / " + f.OWASP
		}
		fmt.Fprintf(&b, "    [%s] %s:%d %s (%s)\n",
			strings.ToUpper(f.Severity), f.File, f.Line, f.Kind, tag)
	}
	return strings.TrimRight(b.String(), "\n")
}
