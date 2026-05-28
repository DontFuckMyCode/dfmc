// Pins the wiring between SubagentRequest.AllowedTools and the
// runtime allowlist that executeToolWithLifecycle consults. Without
// this test, a refactor that drops the `withSubagentAllowlist(ctx,
// ...)` call in runSubagentProfiles would silently revert the
// VULN-035 enforcement to prompt-only "guidance" — the model would
// once again be free to call any tool the parent engine knows
// about, and the existing checkSubagentAllowlist unit tests would
// keep passing because they exercise the gate in isolation.
//
// The test fans a scripted provider that asks for `run_command`
// (wrapped in `tool_call`) and asserts the dispatch is refused at
// the lifecycle gate.

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

func TestRunSubagent_AllowedToolsRefusedAtRuntime(t *testing.T) {
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

	// Round 1: model asks for `run_command` via the meta tool_call
	// wrapper. The allowlist is {read_file}, so this MUST be refused
	// at the lifecycle funnel before approval/hooks/Execute.
	// Round 2: model "gives up" with a final answer — exit the loop.
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
							"name": "run_command",
							"args": map[string]any{"command": "echo nope"},
						}),
					},
				},
			},
			{Text: "Could not satisfy the request — the requested tool is not allowed."},
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
		Tools:        tools.NewFromConfig(cfg),
		Conversation: conversation.New(nil),
	}
	eng.setState(StateReady)

	_, err = eng.RunSubagent(context.Background(), tools.SubagentRequest{
		Task:         "do anything",
		Role:         "researcher",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("RunSubagent: %v", err)
	}

	// Drain events and look for tool:denied with the offending name.
	saw := false
	saweason := ""
drain:
	for {
		select {
		case ev := <-eventsCh:
			if ev.Type != "tool:denied" {
				continue
			}
			payload, _ := ev.Payload.(map[string]any)
			name, _ := payload["name"].(string)
			reason, _ := payload["reason"].(string)
			// The outer call name is `tool_call`; the inner name
			// (run_command) appears in the reason text.
			if strings.Contains(reason, "run_command") || name == "run_command" {
				saw = true
				saweason = reason
				break drain
			}
		default:
			break drain
		}
	}
	if !saw {
		t.Fatalf("expected a tool:denied event naming run_command (allowlist={read_file}); reason=%q", saweason)
	}
}
