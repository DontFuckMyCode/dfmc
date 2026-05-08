package tui

// runtime_state_label_test.go — pins the Phase E item 3 precedence
// rule: streaming > agent-active > parked > drive > todo > queued >
// needs-key > needs-provider > ready. Each test sets the flags that
// should map to the named state plus one or more flags that would map
// to a lower-precedence state, and asserts the label wins by virtue
// of position in the switch.

import "testing"

func TestRuntimeStateLabelPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		info      statsPanelInfo
		wantLabel string
		wantStyle string
	}{
		{
			name: "streaming wins over parked + queued",
			info: statsPanelInfo{
				Provider: "anthropic", Configured: true,
				Streaming: true, Parked: true, QueuedCount: 2,
			},
			wantLabel: "waiting", wantStyle: "info",
		},
		{
			name: "agent-active wins over parked + queued (no streaming)",
			info: statsPanelInfo{
				Provider: "anthropic", Configured: true,
				AgentActive: true, Parked: true, QueuedCount: 2,
			},
			wantLabel: "running", wantStyle: "accent",
		},
		{
			name: "parked wins over queued + drive (when not active)",
			info: statsPanelInfo{
				Provider: "anthropic", Configured: true,
				Parked: true, QueuedCount: 2,
				DriveRunID: "drv-1", DriveTotal: 5,
			},
			wantLabel: "parked", wantStyle: "warn",
		},
		{
			name: "drive wins over queued + working (no live state)",
			info: statsPanelInfo{
				Provider: "anthropic", Configured: true,
				DriveRunID: "drv-1", DriveTotal: 5,
				QueuedCount: 2, TodoDoing: 1,
			},
			wantLabel: "drive", wantStyle: "accent",
		},
		{
			name: "needs key when configured=false but provider set",
			info: statsPanelInfo{
				Provider: "anthropic", Configured: false,
			},
			wantLabel: "needs key", wantStyle: "warn",
		},
		{
			name: "ready by default",
			info: statsPanelInfo{
				Provider: "anthropic", Configured: true,
			},
			wantLabel: "ready", wantStyle: "ok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLabel, gotStyle := runtimeStateLabel(tc.info)
			if gotLabel != tc.wantLabel {
				t.Errorf("label: got %q, want %q", gotLabel, tc.wantLabel)
			}
			if gotStyle != tc.wantStyle {
				t.Errorf("style: got %q, want %q", gotStyle, tc.wantStyle)
			}
		})
	}
}
