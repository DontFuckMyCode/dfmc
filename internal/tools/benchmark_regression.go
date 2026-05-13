// benchmark_regression.go — Phase 7 tool: Compare two benchmark runs and detect regressions.
// Part of the benchmark/perf tools expansion.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BenchmarkRegressionTool compares two benchmark results and reports performance changes.
type BenchmarkRegressionTool struct {
	engine *Engine
}

func NewBenchmarkRegressionTool() *BenchmarkRegressionTool {
	return &BenchmarkRegressionTool{}
}

func (t *BenchmarkRegressionTool) Name() string        { return "benchmark_regression" }
func (t *BenchmarkRegressionTool) SetEngine(e *Engine) { t.engine = e }

func (t *BenchmarkRegressionTool) Description() string {
	return "Compare two benchmark runs and detect performance regressions."
}

func (t *BenchmarkRegressionTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "benchmark_regression",
		Title:   "Benchmark Regression",
		Summary: "Compare before/after benchmark results and report regressions.",
		Purpose: `Use to detect if a code change has degraded performance. Run benchmarks before and after a change, save the JSON output, then compare them to see which benchmarks regressed and by how much.`,
		Risk:    RiskRead,
		Tags:    []string{"benchmark", "performance", "regression", "comparison"},
		Args: []Arg{
			{
				Name:        "baseline",
				Type:        ArgString,
				Required:    true,
				Description: `Path to baseline benchmark JSON file.`,
			},
			{
				Name:        "current",
				Type:        ArgString,
				Required:    true,
				Description: `Path to current benchmark JSON file.`,
			},
			{
				Name:        "threshold_pct",
				Type:        ArgNumber,
				Default:     10.0,
				Description: `Percentage degradation to flag as regression (default: 10%).`,
			},
			{
				Name:        "compare_metric",
				Type:        ArgString,
				Default:     "ns_per_op",
				Description: `Metric to compare: ns_per_op, allocs_per_op, bytes_per_op (default: ns_per_op).`,
			},
		},
		Returns:    "Structured JSON: {regressions: [{name, baseline_value, current_value, pct_change, severity}], summary: {total, improved, regressed}",
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

// BenchmarkResult wraps benchmark output for comparison.
type BenchmarkResult struct {
	Target     string          `json:"target"`
	Benchmarks []BenchmarkSpec `json:"benchmarks"`
}

// regressionEntry represents a single benchmark comparison.
type regressionEntry struct {
	Name        string  `json:"name"`
	Baseline    float64 `json:"baseline_value"`
	Current     float64 `json:"current_value"`
	PctChange   float64 `json:"pct_change"`
	Severity    string  `json:"severity"`
	NsPerOp     float64 `json:"ns_per_op,omitempty"`
	AllocsPerOp int64   `json:"allocs_per_op,omitempty"`
	BytesPerOp  int64   `json:"bytes_per_op,omitempty"`
}

func (t *BenchmarkRegressionTool) Execute(ctx context.Context, req Request) (Result, error) {
	baseline := strings.TrimSpace(asString(req.Params, "baseline", ""))
	current := strings.TrimSpace(asString(req.Params, "current", ""))
	threshold := asFloat(req.Params, "threshold_pct", 10.0)
	compareMetric := strings.TrimSpace(asString(req.Params, "compare_metric", "ns_per_op"))

	if baseline == "" || current == "" {
		return Result{}, missingParamError("benchmark_regression", "baseline/current", req.Params,
			`{"baseline": "bench_before.json", "current": "bench_after.json"}`,
			"baseline and current are required — paths to benchmark JSON files.")
	}

	baselineData, err := t.loadBenchmarkResults(req.ProjectRoot, baseline)
	if err != nil {
		return Result{}, fmt.Errorf("baseline: %w", err)
	}

	currentData, err := t.loadBenchmarkResults(req.ProjectRoot, current)
	if err != nil {
		return Result{}, fmt.Errorf("current: %w", err)
	}

	baselineMap := make(map[string]BenchmarkSpec)
	for _, b := range baselineData.Benchmarks {
		baselineMap[b.Name] = b
	}
	currentMap := make(map[string]BenchmarkSpec)
	for _, b := range currentData.Benchmarks {
		currentMap[b.Name] = b
	}

	var regressions []regressionEntry
	var allEntries []regressionEntry

	for name, baseSpec := range baselineMap {
		curSpec, exists := currentMap[name]
		if !exists {
			regressions = append(regressions, regressionEntry{
				Name:      name,
				Baseline:  baseSpec.NsPerOp,
				Current:   0,
				PctChange: -100,
				Severity:  "regression",
			})
			allEntries = append(allEntries, regressions[len(regressions)-1])
			continue
		}

		baseVal := getMetricValue(baseSpec, compareMetric)
		curVal := getMetricValue(curSpec, compareMetric)

		var pct float64
		var severity string
		if baseVal > 0 {
			pct = ((curVal - baseVal) / baseVal) * 100
		}

		if pct > threshold {
			severity = "regression"
		} else if pct < -threshold {
			severity = "improvement"
		} else {
			severity = "stable"
		}

		entry := regressionEntry{
			Name:        name,
			Baseline:    baseVal,
			Current:     curVal,
			PctChange:   pct,
			Severity:    severity,
			NsPerOp:     curSpec.NsPerOp,
			AllocsPerOp: curSpec.AllocsPerOp,
			BytesPerOp:  curSpec.BytesPerOp,
		}
		regressions = append(regressions, entry)
		allEntries = append(allEntries, entry)
	}

	for name, curSpec := range currentMap {
		if _, exists := baselineMap[name]; !exists {
			entry := regressionEntry{
				Name:        name,
				Baseline:    0,
				Current:     getMetricValue(curSpec, compareMetric),
				PctChange:   0,
				Severity:    "new",
				NsPerOp:     curSpec.NsPerOp,
				AllocsPerOp: curSpec.AllocsPerOp,
				BytesPerOp:  curSpec.BytesPerOp,
			}
			allEntries = append(allEntries, entry)
		}
	}

	sort.Slice(regressions, func(i, j int) bool {
		if regressions[i].Severity != regressions[j].Severity {
			return regressions[i].Severity == "regression"
		}
		si := regressions[i].PctChange
		if si < 0 {
			si = -si
		}
		sj := regressions[j].PctChange
		if sj < 0 {
			sj = -sj
		}
		return si > sj
	})

	var regressed, improved, stable int
	for _, e := range allEntries {
		switch e.Severity {
		case "regression":
			regressed++
		case "improvement":
			improved++
		default:
			stable++
		}
	}

	summary := map[string]any{
		"total":         len(allEntries),
		"regressed":     regressed,
		"improved":      improved,
		"stable":        stable,
		"threshold_pct": threshold,
		"metric":        compareMetric,
	}

	data := map[string]any{
		"regressions": regressions,
		"summary":     summary,
	}

	summaryText := fmt.Sprintf("benchmark_regression: %d regressed, %d improved, %d stable (threshold: %.1f%%, metric: %s)",
		regressed, improved, stable, threshold, compareMetric)

	return Result{
		Output: summaryText,
		Data:   data,
	}, nil
}

func (t *BenchmarkRegressionTool) loadBenchmarkResults(projectRoot, source string) (*BenchmarkResult, error) {
	path := source
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectRoot, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	var result BenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil {
		benchmarks := parseBenchmarkOutput(string(data))
		if len(benchmarks) == 0 {
			return nil, fmt.Errorf("invalid benchmark JSON: %w", err)
		}
		result.Benchmarks = benchmarks
	}

	return &result, nil
}

func getMetricValue(spec BenchmarkSpec, metric string) float64 {
	switch metric {
	case "ns_per_op":
		return spec.NsPerOp
	case "allocs_per_op":
		return float64(spec.AllocsPerOp)
	case "bytes_per_op":
		return float64(spec.BytesPerOp)
	case "mb_per_sec":
		return spec.MBPerSec
	default:
		return spec.NsPerOp
	}
}
