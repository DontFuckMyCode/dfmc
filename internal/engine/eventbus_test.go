package engine

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEventBusSubscribePublishWildcard(t *testing.T) {
	eb := NewEventBus()

	specific := eb.Subscribe("engine:ready")
	wild := eb.Subscribe("*")
	defer eb.Unsubscribe("engine:ready", specific)
	defer eb.Unsubscribe("*", wild)

	eb.Publish(Event{Type: "engine:ready", Source: "test"})

	select {
	case ev := <-specific:
		if ev.Type != "engine:ready" {
			t.Fatalf("unexpected type on specific channel: %s", ev.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting specific event")
	}

	select {
	case ev := <-wild:
		if ev.Type != "engine:ready" {
			t.Fatalf("unexpected type on wildcard channel: %s", ev.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting wildcard event")
	}
}

func TestEventBusSubscribeFunc_RecoversAndContinues(t *testing.T) {
	eb := NewEventBus()
	var calls atomic.Int32
	done := make(chan struct{}, 1)

	unsubscribe := eb.SubscribeFunc("probe", func(ev Event) {
		n := calls.Add(1)
		if n == 1 {
			panic("boom")
		}
		select {
		case done <- struct{}{}:
		default:
		}
	})
	defer unsubscribe()

	eb.Publish(Event{Type: "probe"})
	eb.Publish(Event{Type: "probe"})

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("callback subscriber did not continue after panic")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected callback to be invoked twice, got %d", got)
	}
}

func TestEventBusSubscribeFunc_UnsubscribeStopsDelivery(t *testing.T) {
	eb := NewEventBus()
	var calls atomic.Int32
	unsubscribe := eb.SubscribeFunc("probe", func(ev Event) {
		calls.Add(1)
	})

	eb.Publish(Event{Type: "probe"})
	time.Sleep(20 * time.Millisecond)
	unsubscribe()
	unsubscribe()
	eb.Publish(Event{Type: "probe"})
	time.Sleep(20 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly one callback before unsubscribe, got %d", got)
	}
}
