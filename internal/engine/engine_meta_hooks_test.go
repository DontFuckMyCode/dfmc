package engine

import (
	"reflect"
	"testing"
)

// TestMetaInnerNames pins the extraction logic that lets
// executeToolWithLifecycle fan pre/post_tool hooks out to each inner
// backend tool when the outer call is a meta wrapper. If the unwrap
// rules here drift from meta.go's ToolCallTool.Execute, hooks will
// either miss real calls or fire for phantom ones — both are silent
// failures from the operator's point of view, so the expectations
// below intentionally cover the oddball shapes weaker models emit.
func TestMetaInnerNames(t *testing.T) {
	cases := []struct {
		label  string
		name   string
		params map[string]any
		want   []string
	}{
		{
			label:  "regular tool returns nil",
			name:   "read_file",
			params: map[string]any{"path": "foo.go"},
			want:   nil,
		},
		{
			label: "tool_call canonical",
			name:  "tool_call",
			params: map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "foo.go"},
			},
			want: []string{"read_file"},
		},
		{
			label: "tool_call double-wrap unwraps once",
			name:  "tool_call",
			params: map[string]any{
				"name": "tool_call",
				"args": map[string]any{
					"name": "edit_file",
					"args": map[string]any{"path": "x", "old_string": "a", "new_string": "b"},
				},
			},
			want: []string{"edit_file"},
		},
		{
			label: "tool_call wrapping another meta returns nil",
			name:  "tool_call",
			params: map[string]any{
				"args": map[string]any{
					"name": "tool_search",
					"args": map[string]any{"query": "grep"},
				},
			},
			want: nil,
		},
		{
			label: "tool_batch_call extracts inner names in order",
			name:  "tool_batch_call",
			params: map[string]any{
				"calls": []any{
					map[string]any{"name": "read_file", "args": map[string]any{"path": "a"}},
					map[string]any{"name": "grep_codebase", "args": map[string]any{"pattern": "foo"}},
					map[string]any{"name": "write_file", "args": map[string]any{"path": "b", "content": "x"}},
				},
			},
			want: []string{"read_file", "grep_codebase", "write_file"},
		},
		{
			label: "tool_batch_call skips meta entries silently",
			name:  "tool_batch_call",
			params: map[string]any{
				"calls": []any{
					map[string]any{"name": "read_file"},
					map[string]any{"name": "tool_call", "args": map[string]any{"name": "grep_codebase"}},
					map[string]any{"name": "run_command"},
				},
			},
			want: []string{"read_file", "run_command"},
		},
		{
			label: "tool_batch_call with no valid entries returns nil",
			name:  "tool_batch_call",
			params: map[string]any{
				"calls": []any{
					map[string]any{"name": "tool_help"},
					map[string]any{},
					"garbage",
				},
			},
			want: nil,
		},
		{
			// Hooks should see the intended backend tool even when args are
			// missing — the inner call will fail downstream, but pre_tool
			// hooks still observed the agent's intent, and post_tool will
			// carry the error. Returning nil here would silently hide
			// malformed dispatches from the operator.
			label:  "tool_call with missing args still yields inner name",
			name:   "tool_call",
			params: map[string]any{"name": "read_file"},
			want:   []string{"read_file"},
		},
		{
			label: "tool_call with empty inner name returns nil",
			name:  "tool_call",
			params: map[string]any{
				"args": map[string]any{"args": map[string]any{"path": "x"}},
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := metaInnerNames(tc.name, tc.params)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("metaInnerNames(%q, %+v) = %#v, want %#v", tc.name, tc.params, got, tc.want)
			}
		})
	}
}
