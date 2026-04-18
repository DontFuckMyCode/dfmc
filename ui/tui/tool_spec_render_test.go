package tui

import (
	"context"
	"strings"
	"testing"
)

func TestSlashTool_ShowDescribesReadFileSpec(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	for _, verb := range []string{"/tool show read_file", "/tool describe read_file", "/tool inspect read_file", "/tool help read_file"} {
		next, _, handled := m.executeChatCommand(verb)
		if !handled {
			t.Fatalf("%q should be handled", verb)
		}
		nm := next.(Model)
		last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
		for _, needle := range []string{"read_file", "risk:", "args:"} {
			if !strings.Contains(last, needle) {
				t.Fatalf("%q missing %q, got:\n%s", verb, needle, last)
			}
		}
	}
}

func TestSlashTool_ShowWithoutNameShowsUsage(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	next, _, handled := m.executeChatCommand("/tool show")
	if !handled {
		t.Fatal("/tool show should always be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "usage") {
		t.Fatalf("expected usage hint, got:\n%s", last)
	}
}

func TestSlashTool_ShowUnknownToolReportsIt(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	next, _, handled := m.executeChatCommand("/tool show definitely-not-real")
	if !handled {
		t.Fatal("/tool show should always be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "unknown tool") {
		t.Fatalf("expected 'Unknown tool' message, got:\n%s", last)
	}
}
