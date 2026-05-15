package session

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SharedAttention is a short-lived event bus for agent-to-agent awareness.
// Events are NOT auto-injected into any agent's context. Interested agents
// explicitly subscribe to specific peers or topics and choose whether to consume
// an event they receive.
//
// Events expire after ttl (default 30 min) or at session end.
//
// Bridge to existing code: None yet. Agents call attention.Publish themselves.
type SharedAttention struct {
	mu     sync.RWMutex
	events []AttentionEvent

	// Subscriptions: agent wants events from these sources.
	// key = subscriber AgentID, value = set of source AgentIDs (empty = all).
	subscriptions map[AgentID]map[AgentID]bool

	ttl time.Duration
}

// NewSharedAttention creates a new attention bus.
func NewSharedAttention() *SharedAttention {
	return &SharedAttention{
		subscriptions: make(map[AgentID]map[AgentID]bool),
		ttl:           30 * time.Minute,
	}
}

// Publish emits an event. It is delivered to all agents subscribed to From's events.
// This is goroutine-safe.
func (sa *SharedAttention) Publish(event AttentionEvent) {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.ReadBy == nil {
		event.ReadBy = make(map[AgentID]bool)
	}

	sa.mu.Lock()
	defer sa.mu.Unlock()

	sa.events = append(sa.events, event)

	// Deliver to subscribers.
	for subscriber, sources := range sa.subscriptions {
		if subscriber == event.From {
			continue // don't deliver to self
		}
		// Empty sources map = subscribed to all.
		if len(sources) == 0 || sources[event.From] {
			// The agent's Run loop will pick this up via its attention channel.
			// We store the event; the agent reads it on its next tick.
			_ = subscriber // routing is noted; delivery happens via agent's attention channel
		}
	}
}

// Subscribe registers an agent's interest in events from a source.
// If sources is empty, the agent receives all events.
func (sa *SharedAttention) Subscribe(agent AgentID, sources ...AgentID) {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	if sa.subscriptions[agent] == nil {
		sa.subscriptions[agent] = make(map[AgentID]bool)
	}
	for _, src := range sources {
		sa.subscriptions[agent][src] = true
	}
}

// Unsubscribe removes an agent's subscription.
func (sa *SharedAttention) Unsubscribe(agent AgentID) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	delete(sa.subscriptions, agent)
}

// EventsFor returns all events addressed to a given agent that have not been
// marked as read. The agent should call MarkRead after consuming them.
func (sa *SharedAttention) EventsFor(agent AgentID) []AttentionEvent {
	sa.mu.RLock()
	defer sa.mu.RUnlock()

	var out []AttentionEvent
	cutoff := time.Now().Add(-sa.ttl)

	for i, e := range sa.events {
		if e.Timestamp.Before(cutoff) {
			continue // expired
		}
		if e.ReadBy[agent] {
			continue // already consumed
		}
		// Check subscription.
		srcMap := sa.subscriptions[agent]
		if len(srcMap) > 0 && !srcMap[e.From] {
			continue // not subscribed to this source
		}
		out = append(out, sa.events[i])
	}
	return out
}

// MarkRead records that an agent has consumed an event.
func (sa *SharedAttention) MarkRead(agent AgentID, eventID uuid.UUID) {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	for i := range sa.events {
		if sa.events[i].ID == eventID {
			if sa.events[i].ReadBy == nil {
				sa.events[i].ReadBy = make(map[AgentID]bool)
			}
			sa.events[i].ReadBy[agent] = true
			break
		}
	}
}

// Sweep removes all expired events. Called periodically or at session end.
func (sa *SharedAttention) Sweep() {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	cutoff := time.Now().Add(-sa.ttl)
	var kept []AttentionEvent
	for _, e := range sa.events {
		if e.Timestamp.After(cutoff) {
			kept = append(kept, e)
		}
	}
	sa.events = kept
}

// PublishToolResult is a helper that publishes a tool result event.
func PublishToolResult(sa *SharedAttention, from AgentID, toolName, output string) {
	if sa == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"tool":   toolName,
		"output": output,
	})
	sa.Publish(AttentionEvent{
		From:    from,
		Type:    AttentionToolResult,
		Payload: payload,
	})
}

