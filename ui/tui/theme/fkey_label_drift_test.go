package theme

import (
	"strings"
	"testing"
)

// fkey_label_drift_test.go pins the F-key → panel-name mapping that
// surfaces in stats-panel footer-row hints. The real map (see
// ui/tui/shortcut_panels.go in the parent package):
//
//	F1 Chat · F2 Files · F3 Patch · F4 Workflow · F5 Activity ·
//	F6 Memory · F7 Conversations · F8 Providers · F9 Status ·
//	F10 CodeMap · F11 Tools · F12 Security
//
// Several stats-panel hints previously named the wrong F-key for
// Workflow (claimed F5) and Activity (claimed F7), sending users
// who follow the hint to the wrong tab. This test catches that drift
// class.

func TestStatsPanelHints_FKeyLabelsMatchRealMap(t *testing.T) {
	cases := []struct {
		name    string
		mode    StatsPanelMode
		mustNot []string
		must    []string
	}{
		{
			name:    "tasks references Workflow as F4",
			mode:    StatsPanelModeTasks,
			mustNot: []string{"F5 Workflow", "F7 Workflow"},
			must:    []string{"F4 Workflow"},
		},
		{
			name:    "subagents references Activity as F5",
			mode:    StatsPanelModeSubagents,
			mustNot: []string{"F7 Activity", "F4 Activity"},
			must:    []string{"F5 Activity"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := statsPanelModeActionHint(tc.mode)
			for _, bad := range tc.mustNot {
				if strings.Contains(hint, bad) {
					t.Errorf("hint still names wrong F-key %q: %s", bad, hint)
				}
			}
			for _, good := range tc.must {
				if !strings.Contains(hint, good) {
					t.Errorf("hint missing correct F-key %q: %s", good, hint)
				}
			}
		})
	}
}
