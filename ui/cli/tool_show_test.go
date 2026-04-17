package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunTool_ShowPrintsSpec(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if rc := runTool(context.Background(), eng, []string{"show", "read_file"}, false); rc != 0 {
			t.Fatalf("tool show read_file exit=%d", rc)
		}
	})
	// The shipped read_file tool has a well-known arg name and risk
	// profile; if either disappears the spec layer has regressed.
	if !strings.Contains(out, "read_file") {
		t.Fatalf("expected tool name in output, got:\n%s", out)
	}
	if !strings.Contains(out, "risk:") {
		t.Fatalf("expected risk line, got:\n%s", out)
	}
	if !strings.Contains(out, "args:") {
		t.Fatalf("expected args section, got:\n%s", out)
	}
}

func TestRunTool_ShowJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if rc := runTool(context.Background(), eng, []string{"show", "read_file"}, true); rc != 0 {
			t.Fatalf("tool show json exit=%d", rc)
		}
	})
	var payload struct {
		Name string `json:"name"`
		Risk string `json:"risk"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal tool show json: %v\n%s", err, out)
	}
	if payload.Name != "read_file" {
		t.Fatalf("expected name=read_file, got %q", payload.Name)
	}
	if payload.Risk == "" {
		t.Fatalf("risk field must be populated")
	}
}

func TestRunTool_ShowMissingToolExits1(t *testing.T) {
	eng := newCLITestEngine(t)
	if rc := runTool(context.Background(), eng, []string{"show", "definitely-not-real"}, false); rc != 1 {
		t.Fatalf("expected exit=1 for unknown tool, got %d", rc)
	}
}

func TestRunTool_ShowWithoutNameExits2(t *testing.T) {
	eng := newCLITestEngine(t)
	if rc := runTool(context.Background(), eng, []string{"show"}, false); rc != 2 {
		t.Fatalf("expected exit=2 when tool show has no arg, got %d", rc)
	}
}

func TestRunTool_ListIncludesSummariesInTextMode(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if rc := runTool(context.Background(), eng, nil, false); rc != 0 {
			t.Fatalf("tool list exit=%d", rc)
		}
	})
	// Every shipped tool has a summary in builtin_specs.go; regression
	// here would mean the text mode dropped back to the old name-only
	// behavior.
	if !strings.Contains(out, "read_file") {
		t.Fatalf("expected read_file in output, got:\n%s", out)
	}
	// The read_file spec summary starts with "Read a segment" — checking
	// the word segments avoids coupling to the exact summary wording.
	lines := strings.Split(out, "\n")
	sawSummarized := false
	for _, line := range lines {
		if strings.HasPrefix(line, "read_file") && len(strings.Fields(line)) > 1 {
			sawSummarized = true
			break
		}
	}
	if !sawSummarized {
		t.Fatalf("expected read_file line to include a summary, got:\n%s", out)
	}
}

func TestRunTool_ShowAliases(t *testing.T) {
	eng := newCLITestEngine(t)
	for _, alias := range []string{"describe", "inspect"} {
		if rc := runTool(context.Background(), eng, []string{alias, "read_file"}, true); rc != 0 {
			t.Fatalf("%s read_file should exit 0, got %d", alias, rc)
		}
	}
}
