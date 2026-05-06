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
