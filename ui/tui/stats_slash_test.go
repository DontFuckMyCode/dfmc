package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func newStatsTestModel(t *testing.T, mutate func(*Model)) Model {
	t.Helper()
	cfg := config.DefaultConfig()
	eng := &engine.Engine{Config: cfg, EventBus: engine.NewEventBus()}
	m := NewModel(context.Background(), eng)
	// Session clock back-dated so elapsed-time line is non-zero.
	m.sessionStart = time.Now().Add(-90 * time.Second)
	if mutate != nil {
		mutate(&m)
	}
	return m
}

func TestSlashStats_BaseCardFields(t *testing.T) {
	m := newStatsTestModel(t, nil)
	next, _, handled := m.executeChatCommand("/stats")
	if !handled {
		t.Fatalf("/stats must be handled")
	}
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	for _, needle := range []string{"Session stats", "elapsed:", "messages:", "context in:", "agent:", "rtk savings:"} {
		if !strings.Contains(last, needle) {
			t.Fatalf("stats card missing %q, got:\n%s", needle, last)
		}
	}
}

func TestSlashStats_TokensAndCostAreAliases(t *testing.T) {
	for _, alias := range []string{"/tokens", "/cost"} {
		m := newStatsTestModel(t, nil)
		next, _, handled := m.executeChatCommand(alias)
		if !handled {
			t.Fatalf("%s must route to /stats", alias)
		}
		nm := next.(Model)
		last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
		if !strings.Contains(last, "Session stats") {
			t.Fatalf("%s should share the stats card output, got:\n%s", alias, last)
		}
	}
}

func TestSlashStats_SurfacesCompressionWhenPresent(t *testing.T) {
	m := newStatsTestModel(t, func(mm *Model) {
		mm.telemetry.compressionRawChars = 10_000
		mm.telemetry.compressionSavedChars = 7_500
	})
	next, _, _ := m.executeChatCommand("/stats")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "75%") {
		t.Fatalf("compression ratio should be 75%%, got:\n%s", last)
	}
	if !strings.Contains(last, "7,500") {
		t.Fatalf("savings should render with thousands separator, got:\n%s", last)
	}
}

func TestSlashStats_ShowsAgentProgressWhenActive(t *testing.T) {
	m := newStatsTestModel(t, func(mm *Model) {
		mm.agentLoop.step = 3
		mm.agentLoop.maxToolStep = 10
		mm.agentLoop.toolRounds = 3
		mm.agentLoop.phase = "tools"
		mm.agentLoop.lastTool = "read_file"
		mm.agentLoop.lastStatus = "ok"
		mm.agentLoop.lastDuration = 142
	})
	next, _, _ := m.executeChatCommand("/stats")
	nm := next.(Model)
	last := nm.chat.transcript[len(nm.chat.transcript)-1].Content
	if !strings.Contains(last, "step 3/10") {
		t.Fatalf("agent progress line should show step 3/10, got:\n%s", last)
	}
	if !strings.Contains(last, "read_file") {
		t.Fatalf("last tool line should include read_file, got:\n%s", last)
	}
}

func TestFormatThousands(t *testing.T) {
	cases := map[int]string{
		0:          "0",
		7:          "7",
		999:        "999",
		1_000:      "1,000",
		1_234_567:  "1,234,567",
		-12_345:    "-12,345",
		10_000_000: "10,000,000",
	}
	for in, want := range cases {
		if got := formatThousands(in); got != want {
			t.Errorf("formatThousands(%d) = %q, want %q", in, got, want)
		}
	}
}
