package cli

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestFormatVerboseEvent_AllowlistFiltering pins three contracts of
// the -v stream formatter:
//   1. Events on the allowlist render with key fields.
//   2. Events NOT on the allowlist return empty (silenced).
//   3. Successful hook:run is silenced; failed hook:run renders.
//   4. context:error tolerates the plain-string payload shape.
func TestFormatVerboseEvent_AllowlistFiltering(t *testing.T) {
	cases := []struct {
		name      string
		ev        engine.Event
		wantEmpty bool
		wantSubs  []string
	}{
		{
			name: "history:trimmed renders",
			ev: engine.Event{
				Type:    "history:trimmed",
				Payload: map[string]any{"omitted_messages": 12, "kept_messages": 8},
			},
			wantSubs: []string{"history:trimmed"},
		},
		{
			name: "tool:call silenced (off-allowlist)",
			ev: engine.Event{
				Type:    "tool:call",
				Payload: map[string]any{"tool": "read_file"},
			},
			wantEmpty: true,
		},
		{
			name: "agent:loop:thinking silenced (off-allowlist, too noisy)",
			ev: engine.Event{
				Type:    "agent:loop:thinking",
				Payload: map[string]any{"step": 4, "tokens_used": 1200},
			},
			wantEmpty: true,
		},
		{
			name: "successful hook:run silenced",
			ev: engine.Event{
				Type: "hook:run",
				Payload: map[string]any{
					"name":      "audit",
					"event":     "post_tool",
					"exit_code": 0,
				},
			},
			wantEmpty: true,
		},
		{
			name: "failed hook:run renders",
			ev: engine.Event{
				Type: "hook:run",
				Payload: map[string]any{
					"name":      "lint",
					"event":     "pre_tool",
					"exit_code": 1,
					"err":       "exit status 1",
				},
			},
			wantSubs: []string{"hook:run", "lint", "pre_tool", "exit=1", "exit status 1"},
		},
		{
			name: "context:error string payload",
			ev: engine.Event{
				Type:    "context:error",
				Payload: "ast parse failed",
			},
			wantSubs: []string{"context:error", "ast parse failed"},
		},
		{
			name: "tool:denied with reason",
			ev: engine.Event{
				Type: "tool:denied",
				Payload: map[string]any{
					"name":   "run_command",
					"reason": "user denied",
					"source": "agent-loop",
				},
			},
			wantSubs: []string{"tool:denied", "run_command", "user denied"},
		},
		{
			name: "tool:timeout renders (structural fact, not chatter)",
			ev: engine.Event{
				Type: "tool:timeout",
				Payload: map[string]any{
					"name":     "run_command",
					"limit_ms": 30000,
				},
			},
			wantSubs: []string{"tool:timeout", "run_command"},
		},
		{
			name: "agent:loop:safety_bound renders",
			ev: engine.Event{
				Type: "agent:loop:safety_bound",
				Payload: map[string]any{
					"safety_bound": 100,
					"source":       "autonomous",
				},
			},
			wantSubs: []string{"agent:loop:safety_bound"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatVerboseEvent(tc.ev)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("expected empty (silenced), got %q", got)
				}
				return
			}
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q, got %q", want, got)
				}
			}
		})
	}
}
