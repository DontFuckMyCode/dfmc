package tui

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// TestWorkflowViewV2_EmptyStateOffersGuidance — when no runs are
// loaded, the panel should advertise /drive in Chat (or the CLI
// command) instead of leaving the panes blank.
func TestWorkflowViewV2_EmptyStateOffersGuidance(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderWorkflowViewV2(140))
	for _, want := range []string{
		"WORKFLOW",
		"No drive runs yet",
		"/drive",
		"dfmc drive",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("empty workflow view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestWorkflowViewV2_BannerCountsByStatus — the banner chips must
// reflect every status the runs are in.
func TestWorkflowViewV2_BannerCountsByStatus(t *testing.T) {
	m := newCoverageModel(t)
	m.workflow.runs = []*drive.Run{
		{ID: "a", Task: "x", Status: drive.RunRunning},
		{ID: "b", Task: "y", Status: drive.RunRunning},
		{ID: "c", Task: "z", Status: drive.RunDone},
		{ID: "d", Task: "w", Status: drive.RunFailed},
	}
	view := stripANSI(m.renderWorkflowViewV2(160))
	for _, want := range []string{
		"2 running",
		"1 done",
		"1 failed",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("banner missing %q. Got:\n%s", want, view)
		}
	}
}

// TestWorkflowViewV2_SelectedRunRendersMetaCards — once a run is
// selected, the metadata pane (wide widths) must render the RUN card
// + ACTIONS card.
func TestWorkflowViewV2_SelectedRunRendersMetaCards(t *testing.T) {
	m := newCoverageModel(t)
	m.workflow.runs = []*drive.Run{
		{
			ID:     "drv-1",
			Task:   "Refactor auth module",
			Status: drive.RunRunning,
			Todos: []drive.Todo{
				{ID: "t1", Title: "plan", Status: drive.TodoDone},
				{ID: "t2", Title: "code", Status: drive.TodoRunning},
			},
		},
	}
	m.workflow.selectedRunID = "drv-1"
	view := stripANSI(m.renderWorkflowViewV2(140))
	for _, want := range []string{
		"RUN", "ACTIONS",
		"drv-1",
		"TODOs:",
		"Refactor auth",
		// counts strip in the tree pane
		"running",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("selected-run view missing %q. Got:\n%s", want, view)
		}
	}
}

func TestWorkflowTreePaneScrollsToSelectedTodoWindow(t *testing.T) {
	m := newCoverageModel(t)
	todos := make([]drive.Todo, 24)
	for i := range todos {
		todos[i] = drive.Todo{
			ID:     "todo-" + string(rune('a'+i)),
			Title:  "task row " + string(rune('a'+i)),
			Status: drive.TodoPending,
		}
	}
	m.workflow.runs = []*drive.Run{{
		ID:     "drv-scroll",
		Task:   "Long tree",
		Status: drive.RunRunning,
		Todos:  todos,
	}}
	m.workflow.selectedRunID = "drv-scroll"
	m.workflow.scrollY = 18

	view := stripANSI(m.renderWorkflowTreePane(88, 14, paletteForTab("Workflow", false)))
	if !strings.Contains(view, "task row s") {
		t.Fatalf("selected TODO row should be visible after scrolling, got:\n%s", view)
	}
	if strings.Contains(view, "task row a") {
		t.Fatalf("tree pane should not keep rendering from the top after scroll, got:\n%s", view)
	}
}

// TestWorkflowPanelWidths_BreakpointsBehave pins the 3/2/1-pane split.
func TestWorkflowPanelWidths_BreakpointsBehave(t *testing.T) {
	t.Run("three-pane", func(t *testing.T) {
		l, tr, mw := workflowPanelWidths(140, true, false)
		if l < 28 || tr < 32 || mw < 28 {
			t.Errorf("three-pane below floor: l=%d t=%d m=%d", l, tr, mw)
		}
		if l+tr+mw+4 > 140 {
			t.Errorf("three-pane overflow: %d+%d+%d+4=%d > 140", l, tr, mw, l+tr+mw+4)
		}
	})
	t.Run("two-pane", func(t *testing.T) {
		l, tr, mw := workflowPanelWidths(100, false, true)
		if mw != 0 {
			t.Errorf("two-pane should have meta=0, got %d", mw)
		}
		if l < 28 || tr < 28 {
			t.Errorf("two-pane below floor: l=%d t=%d", l, tr)
		}
	})
	t.Run("one-pane", func(t *testing.T) {
		l, tr, mw := workflowPanelWidths(60, false, false)
		if l != 60 || tr != 60 || mw != 0 {
			t.Errorf("one-pane should give full width: l=%d t=%d m=%d", l, tr, mw)
		}
	})
}
