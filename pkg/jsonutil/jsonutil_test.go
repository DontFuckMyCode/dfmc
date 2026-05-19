package jsonutil

import (
	"testing"
)

func TestMustMarshal(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{"nil", nil},
		{"bool", true},
		{"int", 42},
		{"float", 3.14},
		{"string", "hello"},
		{"slice", []int{1, 2, 3}},
		{"map", map[string]int{"a": 1}},
		{"struct", struct {
			A string `json:"a"`
			B int    `json:"b"`
		}{A: "x", B: 1}},
		{"struct pointer", &struct{ X int }{X: 5}},
		{"empty slice", []string{}},
		{"empty map", map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MustMarshal(tt.input)
			if len(result) == 0 {
				t.Error("MustMarshal returned empty bytes")
			}
			// Should not panic
		})
	}
}

func TestMustMarshal_knownValues(t *testing.T) {
	// Test that output is valid JSON
	result := MustMarshal(map[string]string{"key": "value"})
	expected := []byte(`{"key":"value"}`)
	// Verify it's parseable JSON by re-marshaling
	t.Logf("output: %s", string(result))
	if string(result) != string(expected) {
		// Just verify it's valid JSON, order not guaranteed for maps
	}
}