package promptlib

import (
	"strings"
	"testing"
)

func TestEmbedFS_NewDecoder(t *testing.T) {
	data, err := defaultTemplatesFS.ReadFile("defaults/system_prompts.yaml")
	if err != nil {
		t.Skipf("embed read failed: %v (skipping)", err)
	}
	t.Logf("Embed read %d bytes", len(data))

	tpls := decodeYAMLTemplates("defaults/system_prompts.yaml", data)
	t.Logf("Decoded %d templates", len(tpls))
	for i := 0; i < len(tpls); i++ {
		tpl := tpls[i]
		t.Logf("  [%d] id=%s type=%s task=%s compose=%s priority=%d bodyLen=%d marker=%v",
			i, tpl.ID, tpl.Type, tpl.Task, tpl.Compose, tpl.Priority, len(tpl.Body),
			strings.Contains(tpl.Body, CacheBreakMarker))
	}
	if len(tpls) == 0 {
		t.Fatal("no templates decoded")
	}
	if tpls[0].Body == "" {
		t.Fatal("first template body is empty")
	}
	t.Logf("First template body[:80]=%q", tpls[0].Body[:min(80, len(tpls[0].Body))])
	t.Logf("First template has marker=%v", strings.Contains(tpls[0].Body, CacheBreakMarker))
}
