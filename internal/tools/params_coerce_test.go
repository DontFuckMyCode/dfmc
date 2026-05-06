package tools

import (
	"math"
	"testing"
)

// TestAsBool_AcceptsCommonProviderShapes pins the wider asBool
// coercion: providers and weaker models serialize booleans in a few
// different ways (`true`, `"true"`, `"yes"`, `"on"`, integer `1`,
// JSON-decoded `1.0`). Earlier asBool only matched bare bool values,
// the literal string "true", and the string "1" — anything else
// silently fell back to the default, so `case_sensitive: 1` to
// grep_codebase quietly returned the wrong answer.
func TestAsBool_AcceptsCommonProviderShapes(t *testing.T) {
	truthy := []any{
		true,
		"true", "True", "TRUE", " true ", "yes", "Yes", "y", "Y",
		"1", "on", "ON",
		1, int32(1), int64(1),
		float64(1), float64(2),
	}
	for _, v := range truthy {
		got := asBool(map[string]any{"k": v}, "k", false)
		if !got {
			t.Errorf("asBool(%T=%v) = false, want true", v, v)
		}
	}

	falsy := []any{
		false,
		"false", "False", "no", "n", "0", "off", "OFF", "",
		0, int32(0), int64(0), float64(0),
	}
	for _, v := range falsy {
		got := asBool(map[string]any{"k": v}, "k", true) // fallback=true to detect mis-fallback
		if got {
			t.Errorf("asBool(%T=%v) = true, want false", v, v)
		}
	}

	// Unrecognised shapes fall back instead of guessing.
	if got := asBool(map[string]any{"k": "maybe"}, "k", false); got {
		t.Errorf(`asBool("maybe") with fallback=false → false, got true`)
	}
	if got := asBool(map[string]any{"k": "maybe"}, "k", true); !got {
		t.Errorf(`asBool("maybe") with fallback=true → true, got false`)
	}
	// Missing key returns fallback.
	if got := asBool(map[string]any{}, "k", true); !got {
		t.Errorf("missing key should return fallback=true")
	}
	// Nil map returns fallback.
	if got := asBool(nil, "k", true); !got {
		t.Errorf("nil map should return fallback=true")
	}
}

// TestAsString_RejectsNonStringScalars pins the asString contract for
// the three nonsense inputs the legacy fmt.Sprint fall-through used to
// turn into corrupt values:
//   - nil → "<nil>"   (read_file path="<nil>" → "file not found: <nil>")
//   - []any{x} → "[x]"  (weak model wrapping a single arg in an array)
//   - map → "map[k:v]"  (model nesting struct where a string was wanted)
// All three should fall back to the caller's default so the
// missingParamError or downstream validation fires with a useful
// message instead of corrupted-value errors.
func TestAsString_RejectsNonStringScalars(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{name: "nil value", v: nil},
		{name: "single-element array", v: []any{"foo"}},
		{name: "map", v: map[string]any{"k": "v"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := asString(map[string]any{"path": tc.v}, "path", "")
			if got != "" {
				t.Errorf("asString(%T=%v) = %q, want fallback empty string", tc.v, tc.v, got)
			}
			// Fallback must propagate when caller passes a default.
			withDefault := asString(map[string]any{"path": tc.v}, "path", "default")
			if withDefault != "default" {
				t.Errorf("asString(%T=%v, fallback=default) = %q, want %q", tc.v, tc.v, withDefault, "default")
			}
		})
	}
	// Sanity: real strings still pass through.
	if got := asString(map[string]any{"path": "src/foo.go"}, "path", ""); got != "src/foo.go" {
		t.Errorf("string passthrough broke: got %q", got)
	}
	// Sanity: numbers still convert via fmt.Sprint (some callers expect
	// `asString(params, "step", "")` to coerce numeric step IDs).
	if got := asString(map[string]any{"step": 42}, "step", ""); got != "42" {
		t.Errorf("numeric coerce broke: got %q", got)
	}
}

// TestAsStringSlice_HandlesProviderShapes pins the shared list-coercion
// contract used by both grep's splitGlobList and git's stringSliceArg.
// Before consolidation each helper had its own (slightly different)
// switch — same provider input could yield empty in one tool and
// populated in another. This test holds the single source of truth.
func TestAsStringSlice_HandlesProviderShapes(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want []string
	}{
		{name: "nil missing", v: nil, want: nil},
		{name: "[]string", v: []string{"*.go", "  ", "*.ts"}, want: []string{"*.go", "*.ts"}},
		{name: "[]any with mix", v: []any{"a", 2, nil, "  c "}, want: []string{"a", "2", "c"}},
		{name: "string CSV", v: "*.go, *.ts , ,*.py", want: []string{"*.go", "*.ts", "*.py"}},
		{name: "single bare string", v: "*.go", want: []string{"*.go"}},
		{name: "empty string", v: "", want: nil}, // CSV split of "" yields [""], all-empty → drops to nil
		{name: "non-list scalar", v: 42, want: nil},
		{name: "map input", v: map[string]any{"k": "v"}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := asStringSlice(map[string]any{"x": tc.v}, "x")
			if !equalStringSlices(got, tc.want) {
				t.Errorf("asStringSlice(%T=%v) = %v, want %v", tc.v, tc.v, got, tc.want)
			}
			// Sanity: the standalone coerce path must agree.
			gotCore := coerceStringSlice(tc.v)
			if !equalStringSlices(gotCore, tc.want) {
				t.Errorf("coerceStringSlice(%T=%v) = %v, want %v", tc.v, tc.v, gotCore, tc.want)
			}
		})
	}
	// Nil map and missing key.
	if got := asStringSlice(nil, "x"); got != nil {
		t.Errorf("nil map should return nil, got %v", got)
	}
	if got := asStringSlice(map[string]any{}, "x"); got != nil {
		t.Errorf("missing key should return nil, got %v", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAsBool_NaNFallsBack ensures the float64 path rejects NaN. Without
// the explicit NaN check, `NaN != 0` would be true and asBool would
// return true for an invalid value — surprising behaviour even if
// providers shouldn't actually ship NaN here.
func TestAsBool_NaNFallsBack(t *testing.T) {
	nan := math.NaN()
	if got := asBool(map[string]any{"k": nan}, "k", false); got {
		t.Errorf("asBool(NaN) should fall back, got true")
	}
}
