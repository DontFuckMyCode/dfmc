package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/provider"
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

	msgs := eng.buildRequestMessages("new question")
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
	for i := 0; i < 20; i++ {
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

	msgs := eng.trimmedConversationMessages()
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
		total += estimateTokens(m.Content)
	}
	budget := eng.conversationHistoryBudget()
	if total > budget {
		t.Fatalf("expected history tokens <= %d, got %d", budget, total)
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
