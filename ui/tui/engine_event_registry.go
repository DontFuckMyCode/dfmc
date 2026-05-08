package tui

// engine_event_registry.go — typed dispatcher for engine.EventBus events.
//
// Replaces the monolithic switch in handleEngineEvent. Each domain
// (agent loop, tool, drive, subagent, provider, context, coach, system)
// owns its own engine_events_*.go that calls register*EventHandlers on
// the package-level registry at init.
//
// Adding a new event type:
//   1. Pick the right domain file (or create one).
//   2. Write a func handle<Domain><Event>(m Model, ...) (Model, string).
//   3. Add it to the relevant register*EventHandlers via r.register
//      or r.registerMany.
//
// Unknown event types are silently dropped — same behavior the old
// switch's default branch had. If we ever want telemetry on
// drift, dispatch is the single place to add it.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// engineEventHandler processes a single engine.EventBus event into a
// new Model state plus an optional one-line activity summary. An empty
// returned line means "skip activity append / notice update / transcript
// mirror" — the handler did all the side-effects it cares about itself.
type engineEventHandler func(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string)

// engineEventRegistry maps a normalized (lowercased, trimmed) event
// type string to the handler that processes it.
type engineEventRegistry struct {
	handlers map[string]engineEventHandler
}

func newEngineEventRegistry() *engineEventRegistry {
	r := &engineEventRegistry{handlers: make(map[string]engineEventHandler, 96)}
	registerAgentLoopEventHandlers(r)
	registerToolEventHandlers(r)
	registerSubagentEventHandlers(r)
	registerDriveEventHandlers(r)
	registerProviderEventHandlers(r)
	registerContextEventHandlers(r)
	registerCoachEventHandlers(r)
	registerSystemEventHandlers(r)
	return r
}

func (r *engineEventRegistry) register(eventType string, h engineEventHandler) {
	r.handlers[strings.TrimSpace(strings.ToLower(eventType))] = h
}

func (r *engineEventRegistry) registerMany(types []string, h engineEventHandler) {
	for _, t := range types {
		r.register(t, h)
	}
}

func (r *engineEventRegistry) dispatch(m Model, event engine.Event, payload map[string]any) (Model, string, bool) {
	eventType := strings.TrimSpace(strings.ToLower(event.Type))
	if eventType == "" {
		return m, "", false
	}
	h, ok := r.handlers[eventType]
	if !ok {
		return m, "", false
	}
	m, line := h(m, eventType, event, payload)
	return m, line, true
}

// types returns every registered event-type string, sorted. Used by the
// registry's coverage test to pin the set of events the TUI knows about.
func (r *engineEventRegistry) types() []string {
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	return out
}

// has reports whether an event type has a registered handler.
func (r *engineEventRegistry) has(eventType string) bool {
	_, ok := r.handlers[strings.TrimSpace(strings.ToLower(eventType))]
	return ok
}

// engineEvents is the package-level singleton built once at init. The
// registry is read-only after newEngineEventRegistry returns, so
// concurrent dispatch calls from the bubbletea event loop are safe.
var engineEvents = newEngineEventRegistry()
