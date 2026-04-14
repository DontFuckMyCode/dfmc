package engine

import (
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
