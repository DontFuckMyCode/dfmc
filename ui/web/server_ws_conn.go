package web

// server_ws_conn.go — per-connection lifecycle for the WebSocket
// surface: handleWebSocketUpgrade (pre-upgrade gate → upgrade → seed
// the wsConn), wsUpgraderFor (per-Server allowlist Upgrader factory),
// readLoop / writeLoop (with the half-open detector via ping/pong +
// read deadline + per-conn rate limiter), cleanup (sync.Once unwind
// that cancels in-flight handlers and frees the connection slot), and
// the small sendResponse / sendError / sendWS helpers.
//
// Sibling of server_ws.go which keeps the safety-cap constants
// (wsGlobalConnCap / wsPerIPConnCap / wsReadLimit / wsReadDeadline /
// wsPingInterval / wsRPS / wsBurst) and the wsConnLimiter
// global-and-per-IP slot reservation type, the wsConn struct
// definition, the wire types (wsMessage / wsResponse / wsError) and
// wsSendTimeout. Per-method JSON-RPC dispatchers live in
// server_ws_handlers.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
)

// wsUpgraderFor returns a Upgrader bound to this Server's
// allowedOrigins. The CheckOrigin closure consults the per-Server
// allowlist so cross-origin browser tabs are rejected; native WS
// clients (no Origin header) are still accepted.
//
// Allocating a fresh Upgrader per upgrade is cheap (it's a struct of
// ints + one func) and avoids the global-mutable-state trap of trying
// to thread the Server through a package-level var.
func (s *Server) wsUpgraderFor() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     s.checkWebSocketOrigin,
	}
}

func (s *Server) handleWebSocketUpgrade(w http.ResponseWriter, r *http.Request) {
	// VULN-021: gate BEFORE upgrade so a flood of upgrade attempts
	// doesn't pin readLoop/writeLoop goroutines in the cap-exceeded
	// case. The release closure runs from cleanup() so a dropped
	// connection always frees its slot.
	wsRelease, gateMsg := s.wsConnLimiter.Acquire(clientIPKey(r, s.trustedProxies))
	if gateMsg != "" {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": gateMsg,
			"hint":  "wait for an existing WebSocket to close",
		})
		return
	}

	upgrader := s.wsUpgraderFor()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsRelease()
		// gorilla returns 403 for CheckOrigin failures by default and
		// other upgrade-protocol errors come through with their own
		// status; surface a generic JSON error here so callers can
		// distinguish auth-class failures from network-class failures.
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "websocket upgrade failed: " + err.Error()})
		return
	}
	// Cap inbound frame size before the first ReadMessage so an
	// attacker can't push a 100 MB frame into the buffer between
	// upgrade and the first dispatch.
	conn.SetReadLimit(wsReadLimit)
	// Parent on context.Background() — the gorilla upgrade hijacks
	// the underlying TCP conn, so its lifetime extends past the
	// HTTP handler's return. Using r.Context() would cancel
	// immediately and tear down every WS connection on dispatch.
	connCtx, connCancel := context.WithCancel(context.Background())
	ws := &wsConn{
		id:         fmt.Sprintf("ws-%d", time.Now().UnixNano()),
		conn:       conn,
		engine:     s.engine,
		connCtx:    connCtx,
		connCancel: connCancel,
		limiter:    rate.NewLimiter(wsRPS, wsBurst),
		release:    wsRelease,
	}
	// Read deadline + pong handler implement the half-open detector.
	// The writeLoop sends a ping every wsPingInterval; the peer's
	// pong fires the handler below which slides the deadline forward.
	// A peer that stops responding has ReadMessage return an error
	// after wsReadDeadline.
	_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	})
	go ws.writeLoop()
	go ws.readLoop()
}

func (c *wsConn) readLoop() {
	defer c.cleanup()
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		// Per-connection rate limit. Wait blocks against the
		// connection context so a closed conn unwinds cleanly.
		if err := c.limiter.Wait(c.connCtx); err != nil {
			return
		}
		var msg wsMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendError(0, -32700, "parse error")
			continue
		}
		c.handleMessage(msg)
	}
}

func (c *wsConn) writeLoop() {
	// Recover from any panic inside the write path so a single
	// misbehaving message doesn't terminate the goroutine without
	// unwinding the connection. Without this, a bad WriteJSON
	// (closed conn, invalid type) used to take the goroutine down
	// silently and the readLoop would hang on the next ReadMessage
	// (VULN-022 second-half).
	defer func() {
		if r := recover(); r != nil {
			_ = debug.Stack() // discard — engine event bus is unavailable here
			_ = r
		}
		c.cleanup()
	}()

	pingTicker := time.NewTicker(wsPingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-pingTicker.C:
			// Heartbeat. The pong handler installed in
			// handleWebSocketUpgrade slides the read deadline
			// forward; a peer that doesn't pong within
			// wsReadDeadline gets evicted by the next ReadMessage
			// error.
			c.closeMu.Lock()
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsSendTimeout))
			_ = c.conn.WriteMessage(websocket.PingMessage, nil)
			c.closeMu.Unlock()
		case <-c.connCtx.Done():
			return
		}
	}
}

// cleanup is wrapped in sync.Once so concurrent reads/writes that
// each detect failure can call it without panicking on close-of-
// closed-channel (VULN-022 first-half). Cancelling connCtx unwinds
// every per-message handler that's still running so they don't
// continue billing the LLM provider after the conn is gone (VULN-023).
func (c *wsConn) cleanup() {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.connCancel != nil {
			c.connCancel()
		}
		c.closeMu.Lock()
		_ = c.conn.Close()
		c.closeMu.Unlock()
		// Free the per-IP / global slot last so the connection
		// counter only drops once the goroutines are torn down
		// (VULN-021).
		if c.release != nil {
			c.release()
			c.release = nil
		}
	})
}

func (c *wsConn) sendResponse(id int64, result any) {
	c.sendWS(wsResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (c *wsConn) sendError(id int64, code int, message string) {
	c.sendWS(wsResponse{JSONRPC: "2.0", ID: id, Error: wsErrorf(code, message)})
}

func (c *wsConn) sendWS(v any) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if !c.closed.Load() {
		_ = c.conn.SetWriteDeadline(time.Now().Add(wsSendTimeout))
		_ = c.conn.WriteJSON(v)
	}
}
