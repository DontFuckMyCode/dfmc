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

// TestTrimmedConversationMessages_LeadsWithUser pins the fix for a
// silent-500 bug against Anthropic-family providers: when the history
// budget cuts mid-pair, the backward walk in trimmedConversationMessages
// could leave the oldest kept turn as an ASSISTANT message. Anthropic's
// /messages API (and the Anthropic-compat paths in kimi/zai/minimax)
// hard-reject a request whose messages array starts with an assistant
// turn - the operator sees a generic 400 three frames up and has no way
// to tie it back to the trim decision.
//
// The fix: after the backward walk + reverse, peel any leading assistant
// turns into the omitted slice so the kept window always starts with a
// user turn. The dropped assistants still contribute to the summary
// (they're in omitted), so no information is lost.
func TestTrimmedConversationMessages_LeadsWithUser(t *testing.T) {
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

	// Alternating user/assistant history. Each message is ~30 tokens so a
	// ~100-token budget keeps exactly three messages (the backward walk
	// lands on assistant, user, assistant -> reversed to
	// assistant-first). This was the crashing shape pre-fix.
	now := time.Now()
	line := strings.Repeat("word ", 30)
	for i := 0; i < 3; i++ {
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleUser,
			Content:   "u" + line,
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleAssistant,
			Content:   "a" + line,
			Timestamp: now.Add(time.Duration(i)*time.Second + time.Millisecond),
		})
	}

	msgs, omitted := eng.trimmedConversationMessages(100)
	if len(msgs) == 0 {
		t.Fatalf("expected kept messages, got none (omitted=%d)", len(omitted))
	}
	if msgs[0].Role != types.RoleUser {
		t.Fatalf("kept window must start with a user turn for Anthropic/compat APIs; got role=%q content=%q (full window roles: %v)",
			msgs[0].Role, msgs[0].Content, roleSeq(msgs))
	}
}

// TestBuildRequestMessages_NeverStartsWithAssistant is the end-to-end
// pin: regardless of summary prepending or trim cutoff, the messages
// array handed to a provider must start with a user turn.
func TestBuildRequestMessages_NeverStartsWithAssistant(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxHistoryTokens = 100
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
	line := strings.Repeat("word ", 30)
	for i := 0; i < 4; i++ {
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleUser,
			Content:   "u" + line,
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
		eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
			Role:      types.RoleAssistant,
			Content:   "a" + line,
			Timestamp: now.Add(time.Duration(i)*time.Second + time.Millisecond),
		})
	}

	msgs := eng.buildRequestMessages("current question", nil, "")
	if len(msgs) == 0 {
		t.Fatal("expected messages, got 0")
	}
	if msgs[0].Role != types.RoleUser {
		t.Fatalf("messages[0] must be a user turn (got %q) - full sequence: %v", msgs[0].Role, roleSeq(msgs))
	}
}

func roleSeq(msgs []provider.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role)
	}
	return out
}
