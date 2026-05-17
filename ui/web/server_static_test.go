package web

import (
	"strings"
	"testing"
)

func TestWorkbenchHTMLIncludesTasksPanel(t *testing.T) {
	html := renderWorkbenchHTML()
	for _, want := range []string{
		`id="tasks-status"`,
		`id="tasks-list"`,
		`id="tasks-detail"`,
		`function loadTasks()`,
		`function clearTasks()`,
		`/api/v1/tasks?limit=200`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded workbench missing %q", want)
		}
	}
}
