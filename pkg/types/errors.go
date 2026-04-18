package types

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync/atomic"
)

type ErrorKind int

const (
	ErrConfig ErrorKind = iota
	ErrProvider
	ErrRateLimit
	ErrToolExec
	ErrParse
	ErrPermission
	ErrNotFound
	ErrTimeout
	ErrInternal
)

type DFMCError struct {
	Kind    ErrorKind
	Message string
	Cause   error
	Context map[string]any
}

func (e *DFMCError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *DFMCError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsRateLimit(err error) bool {
	var de *DFMCError
	return errors.As(err, &de) && de.Kind == ErrRateLimit
}

func IsTimeout(err error) bool {
	var de *DFMCError
	return errors.As(err, &de) && de.Kind == ErrTimeout
}

func IsConfig(err error) bool {
	var de *DFMCError
	return errors.As(err, &de) && de.Kind == ErrConfig
}

// SafeGoPanicObserver is the callback shape installed by the higher-
// level runtime (engine) to surface panics caught by SafeGo. The
// stack slice is the live debug.Stack() output — copy if you need to
// retain it past the call. nil-safe: SafeGo skips dispatch when no
// observer is set, so embedded use without an event bus still works.
type SafeGoPanicObserver func(name string, recovered any, stack []byte)

// safeGoPanicObserver holds the active observer atomically so multiple
// goroutines can install / read it without locking. Stored as a value
// (not a pointer to a func) so a Load returning the zero value yields
// nil cleanly.
var safeGoPanicObserver atomic.Value // of SafeGoPanicObserver

// SetSafeGoPanicObserver installs the panic-surface callback. Pass nil
// to clear. The engine wires this during Init to translate caught
// panics into runtime:panic events on its EventBus.
func SetSafeGoPanicObserver(fn SafeGoPanicObserver) {
	if fn == nil {
		safeGoPanicObserver.Store(SafeGoPanicObserver(nil))
		return
	}
	safeGoPanicObserver.Store(fn)
}

// SafeGo runs fn in a goroutine with a panic guard. Recovered panics
// are logged via the default logger AND surfaced through the
// SafeGoPanicObserver (when installed) so observability surfaces
// (TUI activity, web /ws, telemetry counters) can render them
// instead of silently swallowing.
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("[PANIC] %s: %v\n%s", name, r, string(stack))
				if v := safeGoPanicObserver.Load(); v != nil {
					if obs, ok := v.(SafeGoPanicObserver); ok && obs != nil {
						// Defensive: a buggy observer must not bring
						// down the recovered goroutine. Wrap in its own
						// recover so a panic-in-panic-handler is just
						// logged, not propagated.
						defer func() {
							if rr := recover(); rr != nil {
								log.Printf("[PANIC-IN-OBSERVER] %s: %v", name, rr)
							}
						}()
						obs(name, r, stack)
					}
				}
			}
		}()
		fn()
	}()
}
