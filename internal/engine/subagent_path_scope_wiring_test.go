// Pins the wiring between SubagentRequest.AllowedPaths and the
// runtime path-scope gate that executeToolWithLifecycle consults.
// Without this, a refactor that drops the
// `withSubagentPathScope(ctx, req.AllowedPaths)` call in
// runSubagentProfiles would silently revert path enforcement to
// "no constraint" — a sub-agent constrained to internal/parsers
// could once again write to internal/auth.

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestRunSubagent_AllowedPathsRefusedAtRuntime(t *testing.T) {
	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	// Round 1: model asks for write_file outside the allowed_paths
	// scope. The gate must refuse before approval/hooks/Execute.
	// Round 2: model "gives up".
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{
				Text: "",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_1",
						Name: "tool_call",
						Input: toolCallInput(map[string]any{
							"name": "write_file",
							"args": map[string]any{
								"path":    "internal/auth/forbidden.go",
								"content": "package auth\n",
							},
						}),
					},
				},
			},
			{Text: "Could not satisfy — target path is outside scope."},
		},
	}
	router.Register(stub)

	bus := NewEventBus()
	eventsCh := bus.Subscribe("*")
	defer bus.Unsubscribe("*", eventsCh)

	eng := &Engine{
		Config:       cfg,
		EventBus:     bus,
		ProjectRoot:  tmp,
		Providers:    router,
		Tools:        tools.New(*cfg),
		Conversation: conversation.New(nil),
	}
	eng.setState(StateReady)

	_, err = eng.RunSubagent(context.Background(), tools.SubagentRequest{
		Task:         "edit something",
		Role:         "coder",
		AllowedPaths: []string{"internal/parsers"},
	})
	if err != nil {
		t.Fatalf("RunSubagent: %v", err)
	}

	// Drain events and look for tool:denied with scope=path naming the
	// forbidden file.
	saw := false
drain:
	for {
		select {
		case ev := <-eventsCh:
			if ev.Type != "tool:denied" {
				continue
			}
			payload, _ := ev.Payload.(map[string]any)
			scope, _ := payload["scope"].(string)
			reason, _ := payload["reason"].(string)
			if scope == "path" && strings.Contains(reason, "internal/auth/forbidden.go") {
				saw = true
				break drain
			}
		default:
			break drain
		}
	}
	if !saw {
		t.Fatalf("expected a tool:denied event with scope=path naming internal/auth/forbidden.go")
	}
}
