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
