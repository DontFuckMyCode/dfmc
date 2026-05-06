package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestBuildRequestMessages_AppendsCurrentQuestion(t *testing.T) {
	cfg := config.DefaultConfig()
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{
		Config:       cfg,
		Providers:    router,
		Conversation: conversation.New(nil),
	}
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "older user message",
		Timestamp: time.Now(),
	})
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "older assistant response",
		Timestamp: time.Now(),
	})

	msgs := eng.buildRequestMessages("new question", nil, "")
	if len(msgs) < 1 {
		t.Fatal("expected at least one request message")
	}
	last := msgs[len(msgs)-1]
	if last.Role != types.RoleUser || last.Content != "new question" {
		t.Fatalf("expected last message to be current user question, got role=%s content=%q", last.Role, last.Content)
	}
}

func TestTrimmedConversationMessages_RespectsBudgetAndRoleFilter(t *testing.T) {
	cfg := config.DefaultConfig()
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{
		Config:           cfg,
		Providers:        router,
		providerOverride: "offline",
		Conversation:     conversation.New(nil),
	}
	now := time.Now()
	// Generate enough conversation to exceed the post-2025-bump
	// defaults (budget=4096 tokens, maxMsgs=60). Each pair below is
	// ~360 chars / ~90 tokens; 80 pairs = 160 messages / ~14400 tokens
	// which should clearly trip both ceilings and produce omitted
	// entries. Old defaults (1200/12) would have tripped on round 6;
	// the new generous floors mean we need a bigger fixture to assert
	// "trimming actually happens for long sessions".
	for i := 0; i < 80; i++ {
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleUser,
			Content:   strings.Repeat("u ", 90) + "msg",
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleAssistant,
			Content:   strings.Repeat("a ", 90) + "msg",
			Timestamp: now.Add(time.Duration(i)*time.Second + time.Millisecond),
		})
	}
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleSystem,
		Content:   "system should be ignored",
		Timestamp: now,
	})
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleTool,
		Content:   "tool should be ignored",
		Timestamp: now,
	})

	budget := eng.conversationHistoryBudget()
	msgs, omitted := eng.trimmedConversationMessages(budget)
	if len(msgs) == 0 {
		t.Fatal("expected trimmed messages")
	}
	if len(msgs) > maxHistoryMessages {
		t.Fatalf("expected max %d messages, got %d", maxHistoryMessages, len(msgs))
	}
	total := 0
	for _, m := range msgs {
		if m.Role != types.RoleUser && m.Role != types.RoleAssistant {
			t.Fatalf("unexpected role in trimmed history: %s", m.Role)
		}
		total += tokens.Estimate(m.Content)
	}
	if total > budget {
		t.Fatalf("expected history tokens <= %d, got %d", budget, total)
	}
	if len(omitted) == 0 {
		t.Fatal("expected omitted history entries for long conversation")
	}
}

func TestConversationHistoryBudget_UsesConfigOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxHistoryTokens = 300
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{
		Config:       cfg,
		Providers:    router,
		Conversation: conversation.New(nil),
	}

	if got := eng.conversationHistoryBudget(); got != 300 {
		t.Fatalf("expected history budget override 300, got %d", got)
	}
}

// TestConversationHistoryBudget_UserSetBypassesAutoCap pins the new
// behavior: users on big-context models can crank Context.MaxHistory
// Tokens above the auto-compute safety ceiling. Without the bypass a
// 1M-window Opus user setting MaxHistoryTokens=200000 would still get
// trimmed to ~32k regardless of intent.
func TestConversationHistoryBudget_UserSetBypassesAutoCap(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxHistoryTokens = 200_000 // far above maxHistoryBudgetTokens cap
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{
		Config:       cfg,
		Providers:    router,
		Conversation: conversation.New(nil),
	}
	if got := eng.conversationHistoryBudget(); got != 200_000 {
		t.Errorf("user-set value should bypass auto-cap, got %d (expected 200000)", got)
	}
}

// TestConversationHistoryMaxMessages_HonorsConfigOverride verifies the
// new MaxHistoryMessages knob takes precedence over the compiled-in
// floor. Without the override, the trim window stays at 60 messages
// even on a 1M-window model where the user might want 200+.
func TestConversationHistoryMaxMessages_HonorsConfigOverride(t *testing.T) {
	cases := []struct {
		name string
		set  int
		want int
	}{
		{name: "zero falls to default", set: 0, want: maxHistoryMessages},
		{name: "user value used", set: 200, want: 200},
		{name: "small value passes through", set: 6, want: 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Context.MaxHistoryMessages = tc.set
			eng := &Engine{Config: cfg}
			if got := eng.conversationHistoryMaxMessages(); got != tc.want {
				t.Errorf("set=%d: got %d, want %d", tc.set, got, tc.want)
			}
		})
	}
}

func TestHistoryBudgetForRequest_ShrinksWhenContextIsLarge(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxHistoryTokens = 1200
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{
		Config:           cfg,
		Providers:        router,
		providerOverride: "offline",
		Conversation:     conversation.New(nil),
	}

	smallBudget := eng.historyBudgetForRequest("short question", nil, "")
	chunks := []types.ContextChunk{
		{TokenCount: 4500},
		{TokenCount: 3500},
	}
	largeBudget := eng.historyBudgetForRequest("short question", chunks, strings.Repeat("system ", 400))

	if largeBudget >= smallBudget {
		t.Fatalf("expected history budget to shrink with large request payload, small=%d large=%d", smallBudget, largeBudget)
	}
	if largeBudget < 0 {
		t.Fatalf("history budget cannot be negative, got %d", largeBudget)
	}
}

func TestBuildRequestMessages_IncludesSummaryWhenOmitted(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxHistoryTokens = 180
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{
		Config:       cfg,
		Providers:    router,
		Conversation: conversation.New(nil),
	}

	now := time.Now()
	for i := 0; i < 16; i++ {
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleUser,
			Content:   strings.Repeat("investigate auth token middleware ", 4),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleAssistant,
			Content:   strings.Repeat("analysis details and findings ", 4),
			Timestamp: now.Add(time.Duration(i)*time.Second + time.Millisecond),
		})
	}

	msgs := eng.buildRequestMessages("final question", nil, "")
	if len(msgs) < 2 {
		t.Fatalf("expected history + user question, got %d messages", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != types.RoleUser || last.Content != "final question" {
		t.Fatalf("expected last message to be current question, got role=%s content=%q", last.Role, last.Content)
	}

	hasSummary := false
	totalHistoryTokens := 0
	for _, m := range msgs[:len(msgs)-1] {
		totalHistoryTokens += tokens.Estimate(m.Content)
		if strings.Contains(m.Content, "[History summary]") {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Fatal("expected summary message when older history is omitted")
	}
	allowed := eng.historyBudgetForRequest("final question", nil, "")
	if totalHistoryTokens > allowed {
		t.Fatalf("expected history tokens <= %d, got %d", allowed, totalHistoryTokens)
	}
}

func TestBuildHistorySummary_ContainsStructuredSignal(t *testing.T) {
	omitted := []types.Message{
		{Role: types.RoleUser, Content: "Please inspect internal/auth/middleware.go and auth.go for token issues?"},
		{Role: types.RoleAssistant, Content: "I reviewed auth.go and found edge-case handling gaps around refresh path."},
		{Role: types.RoleUser, Content: "Can you patch middleware.go and add tests?"},
	}
	summary := buildHistorySummary(omitted, 120)
	if !strings.Contains(summary, "[History summary]") {
		t.Fatalf("expected summary prefix, got: %s", summary)
	}
	for _, marker := range []string{"Scope=", "Primary=", "Progress=", "Topics=", "Files=", "Open="} {
		if !strings.Contains(summary, marker) {
			t.Fatalf("expected marker %q in summary, got: %s", marker, summary)
		}
	}
}

func TestBuildHistorySummary_RespectsTokenLimit(t *testing.T) {
	omitted := []types.Message{
		{Role: types.RoleUser, Content: strings.Repeat("user asks about auth middleware and tokens ", 20) + "?"},
		{Role: types.RoleAssistant, Content: strings.Repeat("assistant reports progress and findings on patches ", 20)},
	}
	const budget = 22
	summary := buildHistorySummary(omitted, budget)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if got := len(strings.Fields(summary)); got > budget {
		t.Fatalf("expected summary token count <= %d, got %d (%q)", budget, got, summary)
	}
}
