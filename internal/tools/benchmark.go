// benchmark.go — Phase 7 tool for running and parsing Go benchmark results.
// Part of the benchmark/perf tools expansion.
//
// Runs `go test -bench=. -benchmem -run=^$ <target>` and parses the output
// into structured per-benchmark records so the model can compare before/after
// performance without having to parse text tables itself.
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type BenchmarkTool struct {
	engine *Engine
}

func NewBenchmarkTool() *BenchmarkTool { return &BenchmarkTool{} }
func (t *BenchmarkTool) Name() string    { return "benchmark" }
func (t *BenchmarkTool) Description() string {
	return "Run Go benchmark tests and return structured performance metrics."
}

// SetEngine wires the engine reference.
func (t *BenchmarkTool) SetEngine(e *Engine) { t.engine = e }

func (t *BenchmarkTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "benchmark",
		Title:   "Benchmark",
		Summary: "Run Go benchmarks and return structured ns/op, allocs/op, and MB/s data.",
		Purpose: `Use to measure the performance of a function or package before and after a change. Run benchmark, compare ns/op and allocs/op between two runs, and get a deterministic verdict rather than eyeballing a terminal table.`,
		Prompt: `Runs ` + "`go test -bench=. -benchmem -run=^$ <target>`" + ` and parses the results into structured JSON. Works on Go packages and files; non-Go projects return a clear error.

Pipeline:
1. Validate target is a Go package or file under the project root
2. Run benchmark with -benchmem to collect allocation data
3. Parse each benchmark line into name, iterations, ns/op, allocs/op, bytes/op
4. Return per-benchmark records sorted by name

For accurate results, run twice (once warm-up, once measured) and compare the measured runs. The model should run the same benchmark twice before and after a change and compare the numbers.`,
		Risk:     RiskRead,
		Tags:     []string{"benchmark", "performance", "go", "test", "profiling"},
		Args: []Arg{
			{Name: "target", Type: ArgString, Required: true, Description: `Go package or file to benchmark (e.g. "internal/engine", "./foo.go").`},
			{Name: "benchmem", Type: ArgBoolean, Default: true, Description: `Include memory allocation metrics (allocs/op, bytes/op). Default true.`},
			{Name: "benchtime", Type: ArgString, Default: "1s", Description: `Minimum benchmark duration (e.g. "1s", "500ms", "3x").`},
			{Name: "count", Type: ArgInteger, Default: 1, Description: `Number of benchmark runs (use 3 for more stable results).`},
			{Name: "cpuprofile", Type: ArgString, Description: `Write CPU profile to this file path (for deep analysis with go tool pprof).`},
			{Name: "memprofile", Type: ArgString, Description: `Write memory profile to this file path.`},
			{Name: "timeout", Type: ArgString, Default: "60s", Description: `Maximum time for the benchmark run.`},
		},
		Returns:        "Structured JSON: {benchmarks: [{name, iterations, ns_per_op, allocs_per_op, bytes_per_op, mb_per_s}], warmup_runs int, count int}",
		Idempotent:     true,
		CostHint:       "cpu-bound",
	}
}

// BenchmarkSpec is the structured result shape for one benchmark.
type BenchmarkSpec struct {
	Name         string  `json:"name"`
	Iterations   int64   `json:"iterations"`
	NsPerOp      float64 `json:"ns_per_op"`
	AllocsPerOp  int64   `json:"allocs_per_op"`
	BytesPerOp   int64   `json:"bytes_per_op"`
	MBPerSec     float64 `json:"mb_per_sec"`
	MeasuredNS   int64   `json:"measured_ns"`
}

func (t *BenchmarkTool) Execute(ctx context.Context, req Request) (Result, error) {
	target := strings.TrimSpace(asString(req.Params, "target", ""))
	if target == "" {
		return Result{}, missingParamError("benchmark", "target", req.Params,
			`{"target":"./internal/engine"}`,
			`target is required — a Go package path or file path to benchmark.`)
	}

	benchmem := asBool(req.Params, "benchmem", true)
	benchtime := strings.TrimSpace(asString(req.Params, "benchtime", "1s"))
	count := asInt(req.Params, "count", 1)
	timeout := resolveTimeout(req.Params, 60*1000) // milliseconds

	// Build the go test arguments.
	args := []string{"test", "-bench=.", "-benchtime", benchtime}
	if benchmem {
		args = append(args, "-benchmem")
	}
	if count > 1 {
		args = append(args, "-count", strconv.Itoa(count))
	}
	if cpuprofile := strings.TrimSpace(asString(req.Params, "cpuprofile", "")); cpuprofile != "" {
		cpuprofileAbs, err := EnsureWithinRoot(req.ProjectRoot, cpuprofile)
		if err != nil {
			return Result{}, fmt.Errorf("cpuprofile: %w", err)
		}
		args = append(args, "-cpuprofile", cpuprofileAbs)
	}
	if memprofile := strings.TrimSpace(asString(req.Params, "memprofile", "")); memprofile != "" {
		memprofileAbs, err := EnsureWithinRoot(req.ProjectRoot, memprofile)
		if err != nil {
			return Result{}, fmt.Errorf("memprofile: %w", err)
		}
		args = append(args, "-memprofile", memprofileAbs)
	}

	// Add the target — must come after the flags.
	// Insert "--" separator so any user-supplied flags in target can't override
	// the benchmark flags we set above (e.g. target="--help" or target="-bench=...").
	if strings.HasPrefix(target, "-") {
		return Result{}, fmt.Errorf("target %q begins with -, which would inject a flag: prepend a package path (e.g. ./internal/engine)", target)
	}
	args = append(args, "--", target)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "go", args...)
	cmd.Dir = req.ProjectRoot
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	output := string(out)

	// Parse benchmark lines.
	benchmarks := parseBenchmarkOutput(output)

	data := map[string]any{
		"target":   target,
		"exit_code": exitCode,
	}

	if len(benchmarks) > 0 {
		data["benchmarks"] = benchmarks
		data["count"] = len(benchmarks)
	}

	if exitCode != 0 {
		// Still return what we can parse — but include the error
		data["error"] = err.Error()
		data["stderr"] = output
	}

	summary := fmt.Sprintf("benchmark: %d results for %s", len(benchmarks), target)
	return Result{
		Output: summary,
		Data:   data,
	}, nil
}

// parseBenchmarkOutput extracts benchmark rows from `go test -bench` output.
// The output format is:
//
//   BenchmarkFoo-8    1234567      987 ns/op     123 B/op     4 allocs/op
//   BenchmarkBar-8    123456      9876 ns/op   12345 B/op    12 allocs/op
//
// Some lines may also include MB/s at the end when -benchmem is used.
func parseBenchmarkOutput(output string) []BenchmarkSpec {
	var results []BenchmarkSpec
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		// Format: BenchmarkName-8  1234567  987 ns/op  123 B/op  4 allocs/op
		// or with MB/s: BenchmarkName-8  1234567  987 ns/op  1.23 MB/s  123 B/op  4 allocs/op
		// or with custom time: BenchmarkName-8    1000      5000 ns/op
		b := parseBenchmarkLine(line)
		if b.Name != "" {
			results = append(results, b)
		}
	}
	return results
}

// parseBenchmarkLine parses a single benchmark output line.
// Examples:
//   BenchmarkHash-8    1234567      987 ns/op
//   BenchmarkHash-8    1234567      987 ns/op     1.23 MB/s
//   BenchmarkHash-8    1234567      987 ns/op     123 B/op     4 allocs/op
//   BenchmarkHash-8    1234567      987 ns/op     1.23 MB/s   123 B/op     4 allocs/op
//   BenchmarkMap-8      10000    123456 ns/op   1024 B/op      2 allocs/op
func parseBenchmarkLine(line string) BenchmarkSpec {
	var b BenchmarkSpec
	// Regex: BenchmarkName-8  iterations  time/op  [extra]...
	// Name format: Benchmark<Name>-<CPUs>
	re := regexp.MustCompile(`^Benchmark([^\s]+)-[0-9]+\s+([0-9]+)\s+([0-9.]+)\s+ns/op`)
	m := re.FindStringSubmatch(line)
	if len(m) < 4 {
		return b
	}
	b.Name = m[1]

	b.Iterations, _ = strconv.ParseInt(m[2], 10, 64)
	b.NsPerOp, _ = strconv.ParseFloat(m[3], 64)

	// Parse remaining metrics via regex — order is not guaranteed.
	// Pattern: <number><unit> where unit is MB/s, B/op, or allocs/op.
	fieldRe := regexp.MustCompile(`([0-9.]+)\s+(MB/s|B/op|allocs/op)`)
	matches := fieldRe.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		val, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			continue
		}
		switch match[2] {
		case "MB/s":
			b.MBPerSec = val
		case "B/op":
			b.BytesPerOp = int64(val)
		case "allocs/op":
			b.AllocsPerOp = int64(val)
		}
	}

	return b
}

// resolveTimeout converts timeout params to milliseconds.
func resolveTimeout(params map[string]any, defMs int) int {
	if raw := strings.TrimSpace(asString(params, "timeout", "")); raw != "" {
		if d, err := parseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	if ms := asInt(params, "timeout_ms", 0); ms > 0 {
		return ms
	}
	return defMs
}

// parseDuration parses "1s", "500ms", "1m" etc into milliseconds.
// Does not handle benchtime multipliers like "3x" — those are not
// durations and should not be treated as valid time values.
func parseDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// "3x" is a benchtime multiplier, not a time duration — return error.
	if strings.HasSuffix(s, "x") {
		return 0, fmt.Errorf("benchtime multiplier %q is not a duration", s)
	}
	// Parse go duration string.
	if strings.HasSuffix(s, "ms") {
		v, err := strconv.Atoi(strings.TrimSuffix(s, "ms"))
		if err != nil {
			return 0, err
		}
		return v, nil
	}
	if strings.HasSuffix(s, "s") {
		v, err := strconv.Atoi(strings.TrimSuffix(s, "s"))
		if err != nil {
			return 0, err
		}
		return v * 1000, nil
	}
	if strings.HasSuffix(s, "m") {
		v, err := strconv.Atoi(strings.TrimSuffix(s, "m"))
		if err != nil {
			return 0, err
		}
		return v * 60 * 1000, nil
	}
	return 0, fmt.Errorf("unknown duration format: %s", s)
}