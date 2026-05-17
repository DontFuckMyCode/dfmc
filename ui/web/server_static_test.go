package web

import (
	"os"
	"strings"
	"testing"
)

func TestWorkbenchHTMLIncludesTasksPanel(t *testing.T) {
	html := renderWorkbenchHTML()
	for _, want := range []string{
		`id="root"`,
		`type="module"`,
		`/assets/`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded workbench missing %q", want)
		}
	}
}

func TestReactSourceIncludesTasksPanelParity(t *testing.T) {
	source, err := os.ReadFile("src/App.tsx")
	if err != nil {
		t.Fatalf("read React app source: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`Matches TUI /tasks list/tree/roots/show/clear.`,
		`/api/v1/tasks?limit=200`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("React app source missing %q", want)
		}
	}
}
