// engine_memory_llm_update.go — LLM-driven post-turn memory update.
//
// memoryUpdateAfterTurn asks the LLM after each turn whether anything
// worth remembering emerged from that exchange. Entries the LLM proposes
// are added to the episodic tier. Best-effort: errors are logged and
// swallowed so this never blocks the agent loop or ask flow.
//
// The gate is MemoryConfig.LLMUpdateEnabled (true by default). Provider
// and model default to the engine's current provider when the config
// fields are empty.

package engine

import (
	"context"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const memoryLLMUpdateTimeout = 30 * time.Second

func (e *Engine) memoryUpdateAfterTurn(question, answer string) {
	if e == nil || e.Memory == nil || e.Config == nil {
		return
	}
	if !e.Config.Memory.LLMUpdateEnabled {
		return
	}

	cfg := e.Config.Memory
	threshold := cfg.LLMUpdateThreshold
	if threshold <= 0 {
		threshold = 0.6
	}

	updater := &engineLLMUpdater{engine: e}

	ctx, cancel := context.WithTimeout(context.Background(), memoryLLMUpdateTimeout)
	defer cancel()

	entries, err := memory.CallWithPrompt(ctx, updater, question, answer, threshold)
	if err != nil || len(entries) == 0 {
		if err != nil {
			e.AppLog.Error("memory:llm_update failed", err)
		}
		return
	}

	var added int
	for _, entry := range entries {
		tier := types.MemoryEpisodic
		if entry.Tier == "semantic" {
			tier = types.MemorySemantic
		}
		memEntry := types.MemoryEntry{
			Project:    e.ProjectRoot,
			Tier:       tier,
			Category:   entry.Category,
			Key:        entry.Key,
			Value:      entry.Value,
			Confidence: entry.Confidence,
		}
		if err := e.Memory.Add(memEntry); err != nil {
			e.AppLog.Error("memory:llm_update add failed", err)
			continue
		}
		added++
	}

	e.EventBus.Publish(Event{
		Type:   "memory:llm_update",
		Source: "engine",
		Payload: map[string]any{
			"suggested": len(entries),
			"added":     added,
		},
	})
}

// engineLLMUpdater bridges the memory.LLMUpdater interface to the
// Engine's provider layer. It is best-effort: Call returns "" on failure.
type engineLLMUpdater struct {
	engine *Engine
}

func (u *engineLLMUpdater) Call(ctx context.Context, providerHint, modelHint, prompt string) (string, error) {
	if u.engine == nil {
		return "", nil
	}
	req := provider.CompletionRequest{
		Provider: providerHint,
		Model:    modelHint,
		Messages: []provider.Message{
			{Role: types.RoleUser, Content: prompt},
		},
	}

	resp, _, err := u.engine.Providers.Complete(ctx, req)
	if err != nil || resp == nil {
		return "", nil
	}
	return resp.Text, nil
}