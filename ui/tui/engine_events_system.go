package tui

// engine_events_system.go — handlers for system-health events:
// config hot-reload outcomes, engine shutdown errors, runtime panics,
// tool panics, security warnings, memory degradation, hook outcomes,
// conversation save errors, indexer failures, and the queued-note
// signal. These are mostly "the user must see this" warnings/errors.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func registerSystemEventHandlers(r *engineEventRegistry) {
	r.register("config:reload:auto", handleConfigReloadAuto)
	r.register("config:reload:auto_failed", handleConfigReloadAutoFailed)
	r.register("engine:shutdown_error", handleEngineShutdownError)
	r.register("runtime:panic", handleRuntimePanic)
	r.register("tool:panicked", handleToolPanicked)
	r.register("security:config_permissions", handleSecurityConfigPermissions)
	r.register("memory:degraded", handleMemoryDegraded)
	r.register("hook:run", handleHookRun)
	r.register("conversation:save:error", handleConversationSaveError)
	r.register("index:error", handleIndexError)
	r.register("agent:note:queued", handleAgentNoteQueued)
}

func handleConfigReloadAuto(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	path := payloadString(payload, "path", "")
	display := path
	if idx := strings.LastIndexAny(display, "/\\"); idx >= 0 && idx+1 < len(display) {
		display = display[idx+1:]
	}
	if strings.TrimSpace(display) == "" {
		display = "config"
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "config:reload:auto",
		Kind:   "system",
		Status: "ok",
		Title:  "config reloaded",
		Detail: display + " · providers/tools/limits refreshed",
	})
	line := "Config auto-reloaded."
	if path != "" {
		line = fmt.Sprintf("Config auto-reloaded from %s.", truncateSingleLine(path, 96))
	}
	return m, line
}

func handleConfigReloadAutoFailed(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	path := payloadString(payload, "path", "")
	display := path
	if idx := strings.LastIndexAny(display, "/\\"); idx >= 0 && idx+1 < len(display) {
		display = display[idx+1:]
	}
	if strings.TrimSpace(display) == "" {
		display = "config"
	}
	errText := strings.TrimSpace(payloadString(payload, "error", "reload failed"))
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "config:reload:auto_failed",
		Kind:   "system",
		Status: "warn",
		Title:  "config reload failed",
		Detail: fmt.Sprintf("%s · still on previous config · %s", display, truncateSingleLine(errText, 100)),
	})
	line := "Config auto-reload failed."
	if errText != "" {
		line = fmt.Sprintf("Config auto-reload failed: %s", truncateSingleLine(errText, 120))
	}
	return m, line
}

func handleEngineShutdownError(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	stage := payloadString(payload, "stage", "shutdown")
	errText := strings.TrimSpace(payloadString(payload, "error", "shutdown failed"))
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "engine:shutdown_error:" + stage,
		Kind:   "system",
		Status: "error",
		Title:  "engine shutdown error",
		Detail: fmt.Sprintf("%s · %s", stage, truncateSingleLine(errText, 120)),
	})
	return m, fmt.Sprintf("Engine shutdown error [%s]: %s", stage, truncateSingleLine(errText, 160))
}

func handleRuntimePanic(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	name := payloadString(payload, "name", "goroutine")
	panicText := strings.TrimSpace(payloadString(payload, "panic", "panic"))
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "runtime:panic:" + name,
		Kind:   "system",
		Status: "error",
		Title:  "runtime panic recovered",
		Detail: fmt.Sprintf("%s · %s", name, truncateSingleLine(panicText, 120)),
	})
	return m, fmt.Sprintf("Runtime panic recovered [%s]: %s", name, truncateSingleLine(panicText, 160))
}

func handleToolPanicked(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	toolName := payloadString(payload, "name", "tool")
	panicText := strings.TrimSpace(payloadString(payload, "panic", "panic"))
	m.upsertStreamingChatEvent(chatEventLine{
		Key:      "tool:panicked:" + toolName,
		Kind:     "tool",
		Status:   "error",
		Title:    toolName + " (panicked)",
		Detail:   truncateSingleLine(panicText, 120),
		ToolName: toolName,
	})
	return m, fmt.Sprintf("Tool panicked: %s — %s", toolName, truncateSingleLine(panicText, 160))
}

func handleSecurityConfigPermissions(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	path := payloadString(payload, "path", "")
	msg := strings.TrimSpace(payloadString(payload, "msg", "config permissions warning"))
	detail := msg
	if path != "" {
		detail = fmt.Sprintf("%s · %s", path, msg)
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "security:config_permissions",
		Kind:   "system",
		Status: "warn",
		Title:  "config permissions warning",
		Detail: truncateSingleLine(detail, 160),
	})
	return m, "Security warning: " + truncateSingleLine(detail, 160)
}

func handleMemoryDegraded(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	reason := strings.TrimSpace(payloadString(payload, "reason", "memory store unavailable"))
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "memory:degraded",
		Kind:   "system",
		Status: "warn",
		Title:  "memory degraded",
		Detail: truncateSingleLine(reason, 160),
	})
	return m, "Memory degraded — episodic/semantic recall disabled: " + truncateSingleLine(reason, 140)
}

func handleHookRun(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	hookEvent := payloadString(payload, "event", "")
	hookName := payloadString(payload, "name", "")
	exitCode := payloadInt(payload, "exit_code", 0)
	durMs := payloadInt(payload, "duration_ms", 0)
	errText := strings.TrimSpace(payloadString(payload, "err", ""))
	failed := exitCode != 0 || errText != ""
	title := "hook ok"
	status := "ok"
	if failed {
		title = "hook failed"
		status = "error"
	}
	display := strings.TrimSpace(hookName)
	if display == "" {
		display = "hook"
	}
	detail := fmt.Sprintf("%s · %s (%dms, exit=%d)", display, hookEvent, durMs, exitCode)
	if errText != "" {
		detail += " · " + truncateSingleLine(errText, 80)
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "hook:run:" + display,
		Kind:   "system",
		Status: status,
		Title:  title,
		Detail: detail,
	})
	line := ""
	if failed {
		line = fmt.Sprintf("Hook failed [%s/%s]: exit=%d %s", hookEvent, display, exitCode, truncateSingleLine(errText, 120))
	}
	return m, line
}

func handleConversationSaveError(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	stage := payloadString(payload, "stage", "save")
	errText := payloadString(payload, "error", "save failed")
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "conversation:save:error",
		Kind:   "context",
		Status: "error",
		Title:  "conversation save failed",
		Detail: fmt.Sprintf("%s: %s", stage, truncateSingleLine(errText, 120)),
	})
	return m, fmt.Sprintf("Conversation save failed [%s]: %s", stage, truncateSingleLine(errText, 160))
}

func handleIndexError(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	// Payload here is a plain string (err.Error()), not a map.
	errText := ""
	if s, ok := event.Payload.(string); ok {
		errText = strings.TrimSpace(s)
	}
	if errText == "" {
		errText = payloadString(payload, "error", "index failed")
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "index:error",
		Kind:   "context",
		Status: "warn",
		Title:  "workspace index failed",
		Detail: truncateSingleLine(errText, 160),
	})
	return m, "Workspace index failed (codemap may be stale): " + truncateSingleLine(errText, 140)
}

func handleAgentNoteQueued(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	note := strings.TrimSpace(payloadString(payload, "note", ""))
	queue := payloadInt(payload, "queue", 0)
	detail := truncateSingleLine(note, 120)
	if queue > 1 {
		detail = fmt.Sprintf("queue depth %d · %s", queue, detail)
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    fmt.Sprintf("agent:note:queued:%d", queue),
		Kind:   "context",
		Status: "ok",
		Title:  "note queued for agent",
		Detail: detail,
	})
	return m, "Note queued for agent: " + truncateSingleLine(note, 140)
}
