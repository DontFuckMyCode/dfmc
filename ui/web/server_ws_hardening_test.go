package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialTestWS turns the httptest URL into a ws:// URL and dials it
// through gorilla. Origin header is set to the loopback so the
// allowlist accepts the upgrade.
func dialTestWS(t *testing.T, ts *httptest.Server) (*websocket.Conn, *http.Response) {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test url: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/api/v1/ws"
	hdr := http.Header{}
	hdr.Set("Origin", "http://"+u.Host)
	c, resp, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err != nil {
		t.Fatalf("ws dial %s: %v", u.String(), err)
	}
	return c, resp
}

// TestWS_OversizedFrameClosesConn pins VULN-019: a single frame larger
// than wsReadLimit must trigger an abnormal close on the next read,
// so a hostile client can't push a 100MB JSON payload into the buffer
// and OOM the host.
func TestWS_OversizedFrameClosesConn(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	srv.SetAllowedOrigins([]string{"*"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c, _ := dialTestWS(t, ts)
	defer c.Close()

	// Write a frame just over the configured limit.
	huge := strings.Repeat("a", int(wsReadLimit)+1024)
	if err := c.WriteMessage(websocket.TextMessage, []byte(huge)); err != nil {
		t.Fatalf("write huge frame: %v", err)
	}
	// The server's ReadMessage hits the limit and closes the conn.
	// Our subsequent ReadMessage returns an error within a short
	// deadline.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := c.ReadMessage()
	if err == nil {
		t.Fatalf("expected read error after oversized frame, got nil")
	}
}

// TestWS_RateLimitDoesNotKill confirms the per-connection limiter
// shapes traffic without immediately killing the connection on
// natural bursts. Sends wsBurst+1 cheap pings as fast as possible
// and asserts the conn is still alive after they all return.
func TestWS_RateLimitDoesNotKill(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	srv.SetAllowedOrigins([]string{"*"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c, _ := dialTestWS(t, ts)
	defer c.Close()

	for i := 0; i < wsBurst+1; i++ {
		if err := c.WriteJSON(map[string]any{"id": i + 1, "method": "ping"}); err != nil {
			t.Fatalf("ping %d: %v", i, err)
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < wsBurst+1; i++ {
		var resp map[string]any
		if err := c.ReadJSON(&resp); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if resp["error"] != nil {
			t.Fatalf("ping %d returned error: %v", i, resp["error"])
		}
	}
}

// TestWS_PingPongHeartbeat asserts the server-initiated ping/pong
// keepalive is wired. We don't override wsPingInterval (a constant)
// so this only verifies the SetPongHandler is installed by sending
// our own ping and confirming the server responds with a pong.
func TestWS_PingPongHeartbeat(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	srv.SetAllowedOrigins([]string{"*"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c, _ := dialTestWS(t, ts)
	defer c.Close()

	gotPong := make(chan struct{}, 1)
	c.SetPongHandler(func(string) error {
		select {
		case gotPong <- struct{}{}:
		default:
		}
		return nil
	})

	if err := c.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	// Drive the read pump so control frames are processed.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	go func() {
		for {
			if _, _, err := c.NextReader(); err != nil {
				return
			}
		}
	}()
	select {
	case <-gotPong:
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not respond to ping with pong")
	}
}

// TestWS_ClientCloseUnwindsServer pins VULN-022 / VULN-023:
// when the client drops the connection, the server's readLoop ends,
// cleanup() runs, connCtx cancels, and writeLoop ends. Without the
// sync.Once guard, the two goroutines racing into cleanup used to
// panic on close-of-closed-channel; without connCtx propagation,
// a parked Ask() on the conn would keep burning provider tokens.
func TestWS_ClientCloseUnwindsServer(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	srv.SetAllowedOrigins([]string{"*"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c, _ := dialTestWS(t, ts)
	// Slam the conn shut without a graceful close — exercises the
	// failure path where both goroutines can race into cleanup.
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Give the server a moment to unwind both goroutines. If cleanup
	// double-closed the (now-removed) sendCh channel or the conn,
	// the test process would have panicked by now.
	time.Sleep(100 * time.Millisecond)
}

// VULN-021: the wsConnLimiter must refuse Acquire once the per-IP
// cap is reached, then re-allow once a slot is released. Same shape
// as the Drive limiter but a separate type so the policy lives near
// its constants.
func TestWSConnLimiter_PerIPCap(t *testing.T) {
	l := newWSConnLimiter(10, 2)

	r1, msg1 := l.Acquire("1.2.3.4")
	if msg1 != "" || r1 == nil {
		t.Fatalf("first acquire should succeed: %s", msg1)
	}
	r2, msg2 := l.Acquire("1.2.3.4")
	if msg2 != "" || r2 == nil {
		t.Fatalf("second acquire should succeed: %s", msg2)
	}
	if _, msg := l.Acquire("1.2.3.4"); msg == "" {
		t.Fatalf("third acquire from same IP should be refused")
	} else if !strings.Contains(msg, "per-IP") {
		t.Fatalf("error message should mention per-IP cap, got %q", msg)
	}
	// Different IP still gets through.
	if _, msg := l.Acquire("5.6.7.8"); msg != "" {
		t.Fatalf("acquire from different IP should succeed: %s", msg)
	}
	// Releasing one slot allows the original IP to acquire again.
	r1()
	if _, msg := l.Acquire("1.2.3.4"); msg != "" {
		t.Fatalf("acquire after release should succeed: %s", msg)
	}
}

// VULN-021: the global cap fires before per-IP when many IPs each
// take a slot.
func TestWSConnLimiter_GlobalCap(t *testing.T) {
	l := newWSConnLimiter(2, 5)
	if _, msg := l.Acquire("1.2.3.4"); msg != "" {
		t.Fatalf("first global slot: %s", msg)
	}
	if _, msg := l.Acquire("5.6.7.8"); msg != "" {
		t.Fatalf("second global slot: %s", msg)
	}
	if _, msg := l.Acquire("9.9.9.9"); msg == "" {
		t.Fatalf("third global slot should be refused")
	} else if !strings.Contains(msg, "global") {
		t.Fatalf("error message should mention global cap, got %q", msg)
	}
}

// VULN-021: a real WS upgrade past the cap returns 429 — the
// integration shape that callers actually see.
func TestWS_UpgradeRefusedAtCap(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	// Override cap to 1 so we don't have to open many real conns.
	srv.wsConnLimiter = newWSConnLimiter(2, 1)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// First conn succeeds.
	c1, _ := dialTestWS(t, ts)
	defer c1.Close()

	// Second from the same IP must be refused with 429.
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/api/v1/ws"
	hdr := http.Header{}
	hdr.Set("Origin", "http://"+u.Host)
	_, resp, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err == nil {
		t.Fatalf("second upgrade from same IP should have failed")
	}
	if resp == nil {
		t.Fatalf("expected response with 429 status")
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}

	// After closing the first connection the slot is released and
	// a new upgrade succeeds again.
	c1.Close()
	time.Sleep(150 * time.Millisecond)
	c2, _ := dialTestWS(t, ts)
	defer c2.Close()
}
