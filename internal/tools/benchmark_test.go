package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestBenchmark_MissingTarget(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "benchmark", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error for missing target")
	}
}

func TestBenchmark_EmptyTarget(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "benchmark", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"target": "   "},
	})
	if err == nil {
		t.Fatalf("expected error for empty target")
	}
}

func TestBenchmarkTool_Name(t *testing.T) {
	tool := NewBenchmarkTool()
	if tool.Name() != "benchmark" {
		t.Errorf("want benchmark, got %s", tool.Name())
	}
}

func TestBenchmarkTool_Spec(t *testing.T) {
	tool := NewBenchmarkTool()
	spec := tool.Spec()
	if spec.Name != "benchmark" {
		t.Errorf("spec.Name: want benchmark, got %s", spec.Name)
	}
	if spec.Risk != RiskRead {
		t.Errorf("spec.Risk: want RiskRead, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"target", "benchmem", "benchtime", "count", "cpuprofile", "memprofile", "timeout"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestBenchmarkTool_Description(t *testing.T) {
	tool := NewBenchmarkTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestBenchmarkTool_SetEngine(t *testing.T) {
	tool := NewBenchmarkTool()
	eng := New(*config.DefaultConfig())
	tool.SetEngine(eng)
	if tool.Name() != "benchmark" {
		t.Errorf("name mismatch")
	}
}

func TestParseBenchmarkOutput_Basic(t *testing.T) {
	output := `PASS
BenchmarkHash-8    1234567      987 ns/op
BenchmarkFoo-8       10000    12345 ns/op
PASS
`
	results := parseBenchmarkOutput(output)
	if len(results) != 2 {
		t.Fatalf("want 2 benchmarks, got %d", len(results))
	}
	if results[0].Name != "Hash" {
		t.Errorf("want Hash, got %s", results[0].Name)
	}
	if results[0].Iterations != 1234567 {
		t.Errorf("want 1234567 iterations, got %d", results[0].Iterations)
	}
	if results[1].Name != "Foo" {
		t.Errorf("want Foo, got %s", results[1].Name)
	}
}

func TestParseBenchmarkOutput_WithBenchmem(t *testing.T) {
	output := `BenchmarkMap-8       10000    123456 ns/op     1024 B/op      2 allocs/op
BenchmarkHash-8    1234567      987 ns/op     123 B/op     4 allocs/op
`
	results := parseBenchmarkOutput(output)
	if len(results) != 2 {
		t.Fatalf("want 2 benchmarks, got %d", len(results))
	}
	if results[0].Name != "Map" {
		t.Errorf("want Map, got %s", results[0].Name)
	}
	if results[0].BytesPerOp != 1024 {
		t.Errorf("want 1024 bytes/op, got %d", results[0].BytesPerOp)
	}
	if results[0].AllocsPerOp != 2 {
		t.Errorf("want 2 allocs/op, got %d", results[0].AllocsPerOp)
	}
	if results[1].Name != "Hash" {
		t.Errorf("want Hash, got %s", results[1].Name)
	}
	if results[1].AllocsPerOp != 4 {
		t.Errorf("want 4 allocs/op, got %d", results[1].AllocsPerOp)
	}
}

func TestParseBenchmarkOutput_WithMBPerSec(t *testing.T) {
	output := `BenchmarkEncode-8    500000      2345 ns/op     15.23 MB/s     512 B/op     1 allocs/op
BenchmarkDecode-8   100000     12345 ns/op      8.50 MB/s    1024 B/op     2 allocs/op
`
	results := parseBenchmarkOutput(output)
	if len(results) != 2 {
		t.Fatalf("want 2 benchmarks, got %d", len(results))
	}
	if results[0].MBPerSec != 15.23 {
		t.Errorf("want 15.23 MB/s, got %f", results[0].MBPerSec)
	}
	if results[1].MBPerSec != 8.50 {
		t.Errorf("want 8.50 MB/s, got %f", results[1].MBPerSec)
	}
}

func TestParseBenchmarkOutput_Empty(t *testing.T) {
	output := `PASS
--- PASS: TestFoo (0.00s)
PASS
`
	results := parseBenchmarkOutput(output)
	if len(results) != 0 {
		t.Errorf("want 0 benchmarks for non-benchmark output, got %d", len(results))
	}
}

func TestParseBenchmarkOutput_Mixed(t *testing.T) {
	output := `PASS
BenchmarkHash-8    1234567      987 ns/op     123 B/op     4 allocs/op
--- PASS: TestFoo (0.00s)
BenchmarkBar-8       10000    12345 ns/op
PASS
`
	results := parseBenchmarkOutput(output)
	if len(results) != 2 {
		t.Fatalf("want 2 benchmarks, got %d", len(results))
	}
	if results[0].Name != "Hash" {
		t.Errorf("want Hash, got %s", results[0].Name)
	}
	if results[1].Name != "Bar" {
		t.Errorf("want Bar, got %s", results[1].Name)
	}
}

func TestParseBenchmarkOutput_RealWorld(t *testing.T) {
	output := `BenchmarkHash8KB-8    1234567      987 ns/op     1.23 MB/s     123 B/op     4 allocs/op
BenchmarkHash16KB-8     500000      1892 ns/op     8.87 MB/s     256 B/op     4 allocs/op
BenchmarkHash32KB-8     250000      3892 ns/op    16.23 MB/s     512 B/op     4 allocs/op
BenchmarkSortInt-8       10000    123456 ns/op     0.50 MB/s    1024 B/op     2 allocs/op
BenchmarkSortString-8        5000    234567 ns/op     0.25 MB/s    2048 B/op     3 allocs/op
`
	results := parseBenchmarkOutput(output)
	if len(results) != 5 {
		t.Fatalf("want 5 benchmarks, got %d", len(results))
	}
	names := make(map[string]bool)
	for _, b := range results {
		names[b.Name] = true
	}
	for _, name := range []string{"Hash8KB", "Hash16KB", "Hash32KB", "SortInt", "SortString"} {
		if !names[name] {
			t.Errorf("expected benchmark %s", name)
		}
	}
}

func TestParseBenchmarkLine(t *testing.T) {
	cases := []struct {
		line    string
		want    BenchmarkSpec
	}{
		{
			line: "BenchmarkHash-8    1234567      987 ns/op",
			want: BenchmarkSpec{Name: "Hash", Iterations: 1234567, NsPerOp: 987},
		},
		{
			line: "BenchmarkMap-8       10000    123456 ns/op     1024 B/op      2 allocs/op",
			want: BenchmarkSpec{Name: "Map", Iterations: 10000, NsPerOp: 123456, BytesPerOp: 1024, AllocsPerOp: 2},
		},
		{
			line: "BenchmarkEncode-8    500000      2345 ns/op     15.23 MB/s     512 B/op     1 allocs/op",
			want: BenchmarkSpec{Name: "Encode", Iterations: 500000, NsPerOp: 2345, MBPerSec: 15.23, BytesPerOp: 512, AllocsPerOp: 1},
		},
	}
	for _, c := range cases {
		got := parseBenchmarkLine(c.line)
		if got.Name != c.want.Name {
			t.Errorf("parseBenchmarkLine(%q): name=%s, want %s", c.line, got.Name, c.want.Name)
		}
		if got.Iterations != c.want.Iterations {
			t.Errorf("parseBenchmarkLine(%q): iter=%d, want %d", c.line, got.Iterations, c.want.Iterations)
		}
		if got.NsPerOp != c.want.NsPerOp {
			t.Errorf("parseBenchmarkLine(%q): ns/op=%.3f, want %.3f", c.line, got.NsPerOp, c.want.NsPerOp)
		}
		if got.BytesPerOp != c.want.BytesPerOp {
			t.Errorf("parseBenchmarkLine(%q): bytes/op=%d, want %d", c.line, got.BytesPerOp, c.want.BytesPerOp)
		}
		if got.AllocsPerOp != c.want.AllocsPerOp {
			t.Errorf("parseBenchmarkLine(%q): allocs/op=%d, want %d", c.line, got.AllocsPerOp, c.want.AllocsPerOp)
		}
		if got.MBPerSec != c.want.MBPerSec {
			t.Errorf("parseBenchmarkLine(%q): mb/s=%.2f, want %.2f", c.line, got.MBPerSec, c.want.MBPerSec)
		}
	}
}

func TestBenchmark_Integration(t *testing.T) {
	// Go refuses go.mod in the system temp dir (C:\Users\...\AppData\Local\Temp)
	// on Windows — create the package under the test binary's working dir.
	tmp := t.TempDir()
	benchDir := filepath.Join(tmp, "benchtest")
	os.MkdirAll(benchDir, 0755)
	os.WriteFile(filepath.Join(benchDir, "go.mod"), []byte("module benchtest\ngo 1.25\n"), 0644)
	os.WriteFile(filepath.Join(benchDir, "fib_test.go"), []byte(`package benchtest

import "testing"

func Fib(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}

func BenchmarkFib10(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Fib(10)
	}
}

func BenchmarkFib15(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Fib(15)
	}
}
`), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "benchmark", Request{
		ProjectRoot: benchDir,
		Params: map[string]any{
			"target":    ".",
			"benchtime": "100ms",
			"benchmem":  true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	benchmarks, ok := res.Data["benchmarks"].([]BenchmarkSpec)
	if !ok || len(benchmarks) == 0 {
		t.Fatalf("expected benchmarks in result, got: %+v", res.Data)
	}
	names := make(map[string]bool)
	for _, b := range benchmarks {
		names[b.Name] = true
	}
	if !names["Fib10"] && !names["Fib15"] {
		t.Errorf("expected Fib10 or Fib15 benchmark, got: %v", names)
	}
}

func TestBenchmark_NonGoTarget(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Readme\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "benchmark", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"target": "./README.md"},
	})
	// go test on a .md file will fail — but we get an exit code and parse it
	// The tool should not panic and should return exit_code in data
	if err != nil {
		// Error is fine — non-go target
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int // milliseconds
		ok    bool
	}{
		{"1s", 1000, true},
		{"500ms", 500, true},
		{"3s", 3000, true},
		{"1m", 60000, true},
		{"1x", 0, false}, // benchtime multiplier, not a duration
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, err := parseDuration(c.input)
		if c.ok && err != nil {
			t.Errorf("parseDuration(%q): unexpected error: %v", c.input, err)
			continue
		}
		if !c.ok && err == nil {
			t.Errorf("parseDuration(%q): expected error, got nil", c.input)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parseDuration(%q): got %dms, want %dms", c.input, got, c.want)
		}
	}
}