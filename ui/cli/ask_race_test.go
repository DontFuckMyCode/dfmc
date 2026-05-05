package cli

import (
	"reflect"
	"testing"
)

// TestSplitCSVTrimsAndDropsEmpties covers the tiny helper that parses
// `--race-providers`. A user typing "anthropic, openai,,deepseek" must
// end up with ["anthropic","openai","deepseek"] — whitespace stripped,
// empties dropped — so the engine gets a clean candidate list.
func TestSplitCSVTrimsAndDropsEmpties(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",,", nil},
		{"anthropic", []string{"anthropic"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" anthropic , openai ,, deepseek ", []string{"anthropic", "openai", "deepseek"}},
		{",anthropic,,openai,", []string{"anthropic", "openai"}},
	}
	for _, tc := range cases {
		got := splitCSV(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("splitCSV(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
