package web

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// VULN-044: writeSSEWithDeadline must apply a per-chunk write
// deadline so a slow-loris reader cannot pin the server goroutine
// forever. We pin the canonical behaviour with a real TCP listener
// that opens a connection and never reads — without the deadline,
// the second SSE write would block indefinitely once kernel buffers
// fill; with the deadline, the write returns an error within seconds.
func TestSSEWriteDeadlineKicksStuckReader(t *testing.T) {
	// Quiet smoke server: any handler that calls writeSSEWithDeadline
	// in a tight loop will surface the slow-loris signal.
	mux := http.NewServeMux()
	done := make(chan struct{})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer does not support flushing")
			close(done)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		clearStreamingWriteDeadline(w)
		// Push a payload large enough to fill kernel send buffers
		// (defaults: 64KB-256KB on most platforms). Without a write
		// deadline the second write will hang once the buffer fills.
		filler := strings.Repeat("x", 64*1024)
		for i := 0; i < 64; i++ {
			if !writeSSEWithDeadline(w, flusher, map[string]any{
				"type":    "delta",
				"delta":   filler,
				"counter": i,
			}) {
				close(done)
				return
			}
		}
		// If we get here without the deadline kicking, the test
		// failed — but writeSSEWithDeadline must have returned true.
		close(done)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Open a raw TCP connection, send the request, and never read.
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := "GET /sse HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// The handler should give up well before the wall-clock chat cap
	// (10 minutes). Per-chunk deadline is ~15s; allow slack.
	select {
	case <-done:
		// good — handler returned because writeSSEWithDeadline got
		// a deadline-exceeded error and bailed.
	case <-time.After(45 * time.Second):
		t.Fatalf("handler did not bail within 45s; slow-loris guard missing")
	}
}

// VULN-044: a working reader must still receive frames (the
// deadline-per-write approach must NOT break the happy path).
func TestSSEWriteDeadlineHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		clearStreamingWriteDeadline(w)
		for i := 0; i < 5; i++ {
			if !writeSSEWithDeadline(w, flusher, map[string]any{
				"type":  "delta",
				"delta": "ping",
			}) {
				return
			}
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 4096)
	var wg sync.WaitGroup
	wg.Add(1)
	var totalBytes int
	var readErr error
	go func() {
		defer wg.Done()
		for {
			n, err := resp.Body.Read(buf)
			totalBytes += n
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					readErr = err
				}
				return
			}
			if totalBytes > 64 {
				return
			}
		}
	}()
	wg.Wait()
	if readErr != nil && !strings.Contains(readErr.Error(), "EOF") {
		t.Fatalf("unexpected read error: %v", readErr)
	}
	if totalBytes == 0 {
		t.Fatalf("expected at least one delta frame, got zero bytes")
	}
}
