package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestToolsListPaneKeepsSelectedToolVisible(t *testing.T) {
	m := Model{}
	tools := make([]string, 30)
	for i := range tools {
		tools[i] = fmt.Sprintf("tool-%02d", i)
	}
	m.toolView.index = 25

	view := stripANSI(m.renderToolsListPane(42, 10, paletteForTab("Tools", false), tools))
	if !strings.Contains(view, "tool-25") {
		t.Fatalf("selected tool should be visible, got:\n%s", view)
	}
	if strings.Contains(view, "tool-00") {
		t.Fatalf("tools list should window around the selected tool, got:\n%s", view)
	}
}
