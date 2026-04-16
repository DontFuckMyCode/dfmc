package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// handoffEngine builds an Engine with a live Conversation manager and a
// lifecycle config tuned so the handoff threshold is easy to trip in tests.
// The compact threshold is kept below the handoff threshold so the
// guard-rail inside maybeAutoHandoff (handoff ratio must exceed compact
// ratio) doesn't short-circuit.
func handoffEngine(t *testing.T, handoffRatio, compactRatio float64, briefMax int) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agent.ContextLifecycle = config.ContextLifecycleConfig{
		Enabled:                   true,
		AutoCompactThresholdRatio: compactRatio,
		KeepRecentRounds:          1,
		HandoffBriefMaxTokens:     briefMax,
		AutoHandoffThresholdRatio: handoffRatio,
	}
	eng := &Engine{
		Config:       cfg,
		EventBus:     NewEventBus(),
		Conversation: conversation.New(nil),
	}
	return eng
}

// seedConversation fills the active conversation with a realistic mix of
// user turns, assistant replies (some with tool activity), and tool results
// so the brief generator has something meaty to summarise.
func seedConversation(t *testing.T, eng *Engine, bloatReps int) {
	t.Helper()
	blob := strings.Repeat("lorem ipsum dolor sit amet ", bloatReps)
	now := time.Now()

	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleUser,
		Content:   "audit the ingest pipeline and surface any stale fixtures",
		Timestamp: now,
	})
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleAssistant,
		Content:   "Starting audit. " + blob,
		Timestamp: now,
		ToolCalls: []types.ToolCallRecord{
			{Name: "grep_codebase", Params: map[string]any{"pattern": "fixtures"}},
		},
		Results: []types.ToolResultRecord{
			{Name: "grep_codebase", Output: "matches: 12 files", Success: true},
		},
	})
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleUser,
		Content:   "focus on the json ones",
		Timestamp: now,
	})
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleAssistant,
		Content:   "Reading flagged JSON. " + blob,
		Timestamp: now,
		ToolCalls: []types.ToolCallRecord{
			{Name: "read_file", Params: map[string]any{"path": "fix.json"}},
			{Name: "read_file", Params: map[string]any{"path": "old.json"}},
		},
		Results: []types.ToolResultRecord{
			{Name: "read_file", Output: blob, Success: true},
			{Name: "read_file", Output: "permission denied", Success: false},
		},
	})
	eng.Conversation.AddMessage("offline", "offline", types.Message{
		Role:      types.RoleAssistant,
		Content:   "Found 3 stale fixtures and 1 unreadable file; recommending cleanup.",
		Timestamp: now,
	})
}

// Tests --------------------------------------------------------------------

// TestMaybeAutoHandoff_FiresAboveThreshold: when the conversation plus the
// next question exceeds the configured handoff ratio, the engine must
// rotate to a fresh conversation seeded with a brief that preserves user
// intent + tool activity summary.
func TestMaybeAutoHandoff_FiresAboveThreshold(t *testing.T) {
	// Handoff threshold very low (0.01 of the 32k default window ≈ 320 tokens)
	// so a modestly seeded conversation trips it.
	eng := handoffEngine(t, 0.01, 0.005, 400)
	seedConversation(t, eng, 200)

	evCh := eng.EventBus.Subscribe("*")
	defer eng.EventBus.Unsubscribe("*", evCh)

	oldID := eng.Conversation.Active().ID

	report := eng.maybeAutoHandoff("next question")
	if report == nil {
		t.Fatalf("expected auto-handoff to fire, got nil report")
	}
	if report.OldConversationID != oldID {
		t.Fatalf("expected old conversation id %q, got %q", oldID, report.OldConversationID)
	}
	if report.NewConversationID == "" || report.NewConversationID == oldID {
		t.Fatalf("expected a new conversation id distinct from %q, got %q", oldID, report.NewConversationID)
	}
	if report.HistoryTokens < report.BriefTokens {
		t.Fatalf("expected history tokens > brief tokens, got history=%d brief=%d", report.HistoryTokens, report.BriefTokens)
	}
	if report.MessagesSealed <= 0 {
		t.Fatalf("expected sealed messages > 0, got %d", report.MessagesSealed)
	}

	// New active conversation must be seeded with exactly one assistant
	// message (the brief) that carries the handoff metadata.
	newActive := eng.Conversation.Active()
	if newActive == nil {
		t.Fatal("expected active conversation after handoff")
	}
	if newActive.ID != report.NewConversationID {
		t.Fatalf("active conversation id %q does not match report %q", newActive.ID, report.NewConversationID)
	}
	msgs := newActive.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 seed message in new conversation, got %d", len(msgs))
	}
	seed := msgs[0]
	if seed.Role != types.RoleAssistant {
		t.Fatalf("seed message must be assistant turn, got %s", seed.Role)
	}
	if !strings.Contains(seed.Content, "handoff brief") {
		t.Fatalf("seed must contain handoff brief header, got: %s", seed.Content)
	}
	if !strings.Contains(seed.Content, "original request:") {
		t.Fatalf("seed must preserve original user intent, got: %s", seed.Content)
	}
	if !strings.Contains(seed.Content, "grep_codebase") || !strings.Contains(seed.Content, "read_file") {
		t.Fatalf("seed must summarise tool activity, got: %s", seed.Content)
	}
	if seed.Metadata["auto_handoff"] != "true" {
		t.Fatalf("seed must carry auto_handoff metadata, got %v", seed.Metadata)
	}
	if seed.Metadata["source_conversation"] != oldID {
		t.Fatalf("seed must cite source conversation %q, got %q", oldID, seed.Metadata["source_conversation"])
	}

	events := collectRecentEvents(evCh, 16, 150*time.Millisecond)
	ev, ok := findEventByType(events, "context:lifecycle:handoff")
	if !ok {
		t.Fatalf("expected context:lifecycle:handoff event, got %v", eventTypes(events))
	}
	payload, _ := ev.Payload.(map[string]any)
	if got, _ := payload["new_conversation"].(string); got != report.NewConversationID {
		t.Fatalf("event payload mismatch: want new_conversation=%q, got %q", report.NewConversationID, got)
	}
	if got, _ := payload["messages_sealed"].(int); got != report.MessagesSealed {
		t.Fatalf("event payload mismatch: want messages_sealed=%d, got %v", report.MessagesSealed, payload["messages_sealed"])
	}
}

// TestMaybeAutoHandoff_DisabledShortCircuits: when lifecycle.enabled=false
// the rotation must be a pure no-op even on an enormous conversation.
func TestMaybeAutoHandoff_DisabledShortCircuits(t *testing.T) {
	eng := handoffEngine(t, 0.01, 0.005, 400)
	eng.Config.Agent.ContextLifecycle.Enabled = false
	seedConversation(t, eng, 400)

	oldID := eng.Conversation.Active().ID
	report := eng.maybeAutoHandoff("next question")
	if report != nil {
		t.Fatalf("expected nil report when lifecycle disabled, got %#v", report)
	}
	if got := eng.Conversation.Active().ID; got != oldID {
		t.Fatalf("expected conversation untouched (%q), got %q", oldID, got)
	}
}

// TestMaybeAutoHandoff_BelowThresholdNoOp: a tiny conversation well under
// the ratio must not trigger rotation.
func TestMaybeAutoHandoff_BelowThresholdNoOp(t *testing.T) {
	eng := handoffEngine(t, 0.95, 0.7, 400)
	seedConversation(t, eng, 2)

	oldID := eng.Conversation.Active().ID
	report := eng.maybeAutoHandoff("tiny follow-up")
	if report != nil {
		t.Fatalf("expected no rotation under threshold, got %#v", report)
	}
	if got := eng.Conversation.Active().ID; got != oldID {
		t.Fatalf("expected conversation preserved (%q), got %q", oldID, got)
	}
}

// TestMaybeAutoHandoff_GuardsAgainstRatioInversion: if the handoff ratio is
// configured at or below the compaction ratio, the rotation must refuse to
// fire — rotating before compaction gets a chance would defeat the whole
// layered lifecycle design.
func TestMaybeAutoHandoff_GuardsAgainstRatioInversion(t *testing.T) {
	eng := handoffEngine(t, 0.5, 0.5, 400) // inverted: handoff == compact
	seedConversation(t, eng, 400)

	oldID := eng.Conversation.Active().ID
	report := eng.maybeAutoHandoff("huge follow-up")
	if report != nil {
		t.Fatalf("expected guard to refuse rotation with inverted ratios, got %#v", report)
	}
	if got := eng.Conversation.Active().ID; got != oldID {
		t.Fatalf("expected conversation untouched on guard, got %q", got)
	}
}

// TestBuildHandoffBrief_Structure: exercises the pure text builder so the
// contract (headers, sections, truncation) is pinned independently of the
// engine wiring.
func TestBuildHandoffBrief_Structure(t *testing.T) {
	history := []types.Message{
		{Role: types.RoleUser, Content: "audit repo"},
		{
			Role:    types.RoleAssistant,
			Content: "Scanning...",
			ToolCalls: []types.ToolCallRecord{
				{Name: "grep_codebase"},
				{Name: "grep_codebase"},
			},
			Results: []types.ToolResultRecord{
				{Name: "grep_codebase", Success: true},
				{Name: "grep_codebase", Success: false},
			},
		},
		{Role: types.RoleUser, Content: "focus on auth"},
		{Role: types.RoleAssistant, Content: "Done. Found 3 issues, all in auth middleware."},
	}

	brief := buildHandoffBrief("conv_xyz", history, 500)
	if brief == "" {
		t.Fatal("expected non-empty brief")
	}
	wantSubstrings := []string{
		"handoff brief",
		"conv_xyz",
		"original request: audit repo",
		"follow-up: focus on auth",
		"grep_codebase×2 ok=1 fail=1",
		"last answer:",
		"3 issues",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q; full brief:\n%s", want, brief)
		}
	}
}

// TestBuildHandoffBrief_TruncatesToBudget: the brief honors the token cap
// (~4 chars per token) by appending a truncation marker.
func TestBuildHandoffBrief_TruncatesToBudget(t *testing.T) {
	longUser := strings.Repeat("alpha ", 800)
	history := []types.Message{
		{Role: types.RoleUser, Content: longUser},
		{Role: types.RoleAssistant, Content: strings.Repeat("beta ", 800)},
	}
	brief := buildHandoffBrief("c1", history, 20) // 20 * 4 = 80 char budget
	if !strings.Contains(brief, "[truncated]") {
		t.Fatalf("expected brief to be truncated under tight budget, got:\n%s", brief)
	}
	if len(brief) > 20*4+32 { // budget + truncation marker slack
		t.Fatalf("brief exceeded expected truncation envelope: len=%d\n%s", len(brief), brief)
	}
}

// TestBuildHandoffBrief_DeterministicToolOrder: the tool-activity line must
// be deterministic regardless of map iteration order.
func TestBuildHandoffBrief_DeterministicToolOrder(t *testing.T) {
	history := []types.Message{
		{Role: types.RoleUser, Content: "go"},
		{
			Role: types.RoleAssistant,
			ToolCalls: []types.ToolCallRecord{
				{Name: "zebra_tool"},
				{Name: "alpha_tool"},
				{Name: "mid_tool"},
			},
			Results: []types.ToolResultRecord{
				{Name: "zebra_tool", Success: true},
				{Name: "alpha_tool", Success: true},
				{Name: "mid_tool", Success: true},
			},
		},
	}
	first := buildHandoffBrief("c1", history, 500)
	for range 5 {
		if got := buildHandoffBrief("c1", history, 500); got != first {
			t.Fatalf("expected deterministic brief; diverged:\nwant: %s\n got: %s", first, got)
		}
	}
	// Alphabetical ordering: alpha comes before mid comes before zebra.
	idxAlpha := strings.Index(first, "alpha_tool")
	idxMid := strings.Index(first, "mid_tool")
	idxZebra := strings.Index(first, "zebra_tool")
	if idxAlpha < 0 || idxMid < 0 || idxZebra < 0 || !(idxAlpha < idxMid && idxMid < idxZebra) {
		t.Fatalf("expected alphabetical tool ordering, got indices: alpha=%d mid=%d zebra=%d\n%s", idxAlpha, idxMid, idxZebra, first)
	}
}
