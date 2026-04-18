package engine

import (
	"sync"
	"sync/atomic"
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

	// dropped counts events the bus discarded because a subscriber's
	// channel was full. Visible via DroppedCount() and surfaced in
	// Engine.Status() so an operator notices when the TUI / web client
	// is falling behind. Atomic so Publish stays under RLock without a
	// separate write lock just to bump a counter.
	dropped uint64
}

// defaultEventBusBuffer is the per-subscriber channel depth. Bursty
// agent loops (a tool batch firing 8 calls within ms, each with a
// reasoning event + start + end + chunk) can push 30+ events through
// the bus per second; the previous 64-slot buffer overflowed during
// long Drive runs and the TUI silently lost activity-feed entries.
// 1024 absorbs any realistic burst while still bounding memory at
// ~16KB per subscriber for the channel header (events themselves stay
// references on the publishers' stack until consumed).
const defaultEventBusBuffer = 1024

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: map[string][]chan Event{},
		bufferSize:  defaultEventBusBuffer,
	}
}

// DroppedCount returns the cumulative number of events the bus has
// dropped because a subscriber's channel was full. Monotonically
// increasing; never reset. Surfaced in Engine.Status() so a non-zero
// value is visible to operators investigating "the TUI seems to be
// missing events".
func (eb *EventBus) DroppedCount() uint64 {
	if eb == nil {
		return 0
	}
	return atomic.LoadUint64(&eb.dropped)
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
			atomic.AddUint64(&eb.dropped, 1)
		}
	}

	for _, ch := range eb.subscribers["*"] {
		select {
		case ch <- event:
		default:
			atomic.AddUint64(&eb.dropped, 1)
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

// Unsubscribe removes ch from the bus and closes it. Two ways callers
// historically broke this:
//
//  1. Subscribed with eventType "*" (wildcard) but unsubscribed with a
//     specific event type — the channel stayed registered as a wildcard
//     subscriber forever, leaking goroutines and (after the close on a
//     freshly-created stub channel) sending on a closed channel during
//     the next Publish. Now we look up the channel in BOTH the typed
//     bucket and the "*" bucket and remove it from whichever one
//     actually holds it.
//
//  2. Calling Unsubscribe twice on the same channel — the second close
//     panicked with "close of closed channel". Now we only close when we
//     actually removed the channel from a bucket; a no-op call is safe.
func (eb *EventBus) Unsubscribe(eventType string, ch chan Event) {
	if eb == nil || ch == nil {
		return
	}
	eb.mu.Lock()
	defer eb.mu.Unlock()

	tryRemove := func(key string) bool {
		subs := eb.subscribers[key]
		for i := range subs {
			if subs[i] == ch {
				eb.subscribers[key] = append(subs[:i], subs[i+1:]...)
				return true
			}
		}
		return false
	}

	// Caller-declared bucket first; fall back to the other so a "*" /
	// specific-type mismatch still cleans up.
	removed := tryRemove(eventType)
	if !removed && eventType != "*" {
		removed = tryRemove("*")
	}
	if !removed && eventType == "*" {
		// Caller passed wildcard but the channel might be in a typed
		// bucket; sweep all buckets as a last resort. Removing from
		// every bucket the channel happens to appear in is correct —
		// duplicates would only exist if Subscribe was called twice
		// with the same channel, which we don't support.
		for key := range eb.subscribers {
			if tryRemove(key) {
				removed = true
			}
		}
	}

	if removed {
		// Defensive close-once: even with the bucket sweep above, a
		// well-meaning caller could double-Unsubscribe (typed THEN
		// wildcard). The first call removes + closes; the second finds
		// nothing in any bucket so removed==false and we skip close.
		// Still wrap in recover so a buggy caller that closed the
		// channel themselves before unsubscribing doesn't take the
		// process down.
		defer func() { _ = recover() }()
		close(ch)
	}
}
