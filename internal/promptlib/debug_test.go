package promptlib

import (
	"fmt"
	"strings"
	"testing"
)

func TestDebugTemplateContent(t *testing.T) {
	lib := New()

	// List all templates
	for _, tmpl := range lib.templates {
		t.Logf("template: id=%s type=%s task=%s compose=%s priority=%d bodyLen=%d marker=%v",
			tmpl.ID, tmpl.Type, tmpl.Task, tmpl.Compose, tmpl.Priority, len(tmpl.Body),
			strings.Contains(tmpl.Body, CacheBreakMarker))
	}

	var replaceTpl Template
	for _, tmpl := range lib.templates {
		if tmpl.Compose == "replace" && tmpl.Type == "system" && tmpl.Task == "general" {
			replaceTpl = tmpl
			break
		}
	}
	if replaceTpl.Body == "" {
		t.Fatal("replace template body is empty")
	}

	hasMarker := strings.Contains(replaceTpl.Body, CacheBreakMarker)
	t.Logf("Replace template: ID=%s priority=%d bodyLen=%d hasMarker=%v",
		replaceTpl.ID, replaceTpl.Priority, len(replaceTpl.Body), hasMarker)
	if hasMarker {
		pos := strings.Index(replaceTpl.Body, CacheBreakMarker)
		t.Logf("Marker body pos: %d snippet: %q", pos, replaceTpl.Body[pos-30:pos+30])
	} else {
		t.Logf("Body start: %q", replaceTpl.Body[:min(100, len(replaceTpl.Body))])
	}

	out := lib.Render(RenderRequest{
		Type: "system", Task: "general", Vars: map[string]string{
			"project_root":     "/tmp",
			"user_query":       "hi",
			"context_files":    "",
			"injected_context": "",
			"project_brief":    "",
			"tools_overview":   "",
			"tool_call_policy": "",
			"response_policy":  "",
			"language":          "generic",
			"profile":           "compact",
			"role":              "",
		},
	})
	t.Logf("Render len=%d markerInOut=%v", len(out), strings.Contains(out, CacheBreakMarker))
	if strings.Contains(out, CacheBreakMarker) {
		pos := strings.Index(out, CacheBreakMarker)
		t.Logf("Marker output pos: %d snippet: %q", pos, out[pos-20:pos+30])
	}
	fmt.Printf("\n=== FULL OUTPUT ===\n%s\n=== END ===\n", out)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
