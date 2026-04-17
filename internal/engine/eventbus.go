package engine

import (
	"sync"
	"time"
)

type Event struct {
	Type      string    `json:"type"`
	Source    string    `json:"source,omitempty"`
	Payload   any       `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type EventBus struct {
	subscribers map[string][]chan Event
	mu          sync.RWMutex
	bufferSize  int
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: map[string][]chan Event{},
		bufferSize:  64,
	}
}

// Publish is nil-receiver-safe so callers with a best-effort pattern
// (`if eb != nil { eb.Publish(...) }`) don't need the guard, and a
// partially-initialized Engine (e.g. during a failed Init rollback)
// can't panic when emitting a shutdown event. The check-then-use
// pattern at call sites was also racy during shutdown — inlining the
// guard here eliminates the race without forcing every caller to
// hold the engine mutex.
func (eb *EventBus) Publish(event Event) {
	if eb == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, ch := range eb.subscribers[event.Type] {
		select {
		case ch <- event:
		default:
		}
	}

	for _, ch := range eb.subscribers["*"] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (eb *EventBus) Subscribe(eventType string) chan Event {
	if eb == nil {
		// Return a closed channel so subscribers' range loops exit
		// cleanly instead of blocking forever on a nil chan.
		ch := make(chan Event)
		close(ch)
		return ch
	}
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan Event, eb.bufferSize)
	eb.subscribers[eventType] = append(eb.subscribers[eventType], ch)
	return ch
}

func (eb *EventBus) Unsubscribe(eventType string, ch chan Event) {
	if eb == nil {
		return
	}
	eb.mu.Lock()
	defer eb.mu.Unlock()

	subs := eb.subscribers[eventType]
	for i := range subs {
		if subs[i] == ch {
			eb.subscribers[eventType] = append(subs[:i], subs[i+1:]...)
			close(ch)
			return
		}
	}
}
