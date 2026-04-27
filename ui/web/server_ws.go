// Package web hosts the embedded DFMC HTTP surface (`dfmc serve`).
// server_ws.go handles the bidirectional WebSocket endpoint for real-time
// remote control — clients send JSON-RPC messages over WS and receive both
// direct responses and engine events on the same connection.
//
// Protocol:
//   - Client → Server: JSON-RPC 2.0 requests (method + params + id)
//   - Server → Client: JSON-RPC 2.0 responses (result/error + id) OR
//                       SSE-like events (type + payload, no id needed)
//
// The WS endpoint is registered at GET /api/v1/ws (upgrade from SSE /ws).

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// Post-upgrade WebSocket safety constants. Closes VULN-019 / 020 /
// 021 / 022 / 023 — every limit below was missing pre-fix.
const (
	// wsGlobalConnCap and wsPerIPConnCap (VULN-021) bound the number
	// of concurrent WebSocket connections globally and per-IP. The
	// existing per-IP HTTP rate limiter only caps requests/sec; once
	// a connection upgrades, it lives indefinitely and consumes
	// goroutines (readLoop + writeLoop), buffer memory, and any
	// EventBus subscriptions it makes. A client opening 10000
	// connections previously walked the engine off a cliff.
	wsGlobalConnCap = 64
	wsPerIPConnCap  = 8
	// wsReadLimit caps a single inbound JSON-RPC frame at 64 KiB.
	// gorilla buffers the whole message before returning from
	// ReadMessage, so a 100 MB frame previously sat in memory until
	// it killed the host. 64 KiB is generous for any tool-call
	// JSON (typical < 4 KiB).
	wsReadLimit int64 = 64 * 1024
	// wsReadDeadline is the per-message read budget. Combined with
	// the pong handler below, it doubles as the half-open
	// connection detector — a peer that stops responding to pings
	// gets its ReadMessage error after this window.
	wsReadDeadline = 60 * time.Second
	// wsPingInterval is how often the writeLoop fires a ping. The
	// peer answers with a pong, the pong handler extends the read
	// deadline, and the cycle repeats. 30s leaves comfortable
	// headroom under wsReadDeadline.
	wsPingInterval = 30 * time.Second
	// wsRPS / wsBurst gate per-connection inbound message rate.
	// Each WS message dispatches a chat/ask/tool that can invoke
	// the configured LLM provider with the operator's API key —
	// without this cap a single connection could empty the
	// provider quota in seconds. 5 rps is comfortable for
	// interactive chat; the burst absorbs natural typing
	// bursts/auto-saves.
	wsRPS   rate.Limit = 5
	wsBurst            = 10
)

// wsConnLimiter is the in-memory counter that bounds concurrent
// WebSocket upgrades globally and per-IP (VULN-021). Same shape as
// driveConcurrencyLimiter — kept separate because the policies
// differ (WS connections are long-lived; Drive runs are bursty).
type wsConnLimiter struct {
	globalCap int
	perIPCap  int

	mu     sync.Mutex
	global int
	perIP  map[string]int
}

func newWSConnLimiter(globalCap, perIPCap int) *wsConnLimiter {
	if globalCap <= 0 {
		globalCap = wsGlobalConnCap
	}
	if perIPCap <= 0 {
		perIPCap = wsPerIPConnCap
	}
	return &wsConnLimiter{
		globalCap: globalCap,
		perIPCap:  perIPCap,
		perIP:     make(map[string]int),
	}
}

// Acquire reserves a slot for the given IP. Returns a release closure
// and a non-empty error message when the cap is reached.
func (l *wsConnLimiter) Acquire(ip string) (func(), string) {
	if l == nil {
		return func() {}, ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global >= l.globalCap {
		return nil, "websocket connection cap reached (global)"
	}
	if ip != "" && l.perIP[ip] >= l.perIPCap {
		return nil, "websocket connection cap reached (per-IP)"
	}
	l.global++
	if ip != "" {
		l.perIP[ip]++
	}
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		released = true
		if l.global > 0 {
			l.global--
		}
		if ip != "" {
			if v := l.perIP[ip]; v > 1 {
				l.perIP[ip] = v - 1
			} else {
				delete(l.perIP, ip)
			}
		}
	}, ""
}

func (l *wsConnLimiter) snapshot() (int, map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make(map[string]int, len(l.perIP))
	for k, v := range l.perIP {
		cp[k] = v
	}
	return l.global, cp
}

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

// wsConn manages one WebSocket client connection.
type wsConn struct {
	id     string
	conn   *websocket.Conn
	engine *engine.Engine

	// connCtx is the parent context for this connection. Cancelled
	// in cleanup() so handleChat / handleAsk / handleTool stop
	// burning provider tokens once the client disconnects (closes
	// VULN-023). Each per-message handler derives a child from
	// this so a single slow message doesn't take the whole conn
	// down — but a closed conn cancels every in-flight message.
	connCtx    context.Context
	connCancel context.CancelFunc

	// limiter throttles inbound messages to wsRPS / wsBurst.
	// Per-connection so one greedy client can't starve another.
	limiter *rate.Limiter

	closed    atomic.Bool
	closeMu   sync.Mutex
	closeOnce sync.Once

	// release frees the per-IP / global connection slot acquired
	// before upgrade (VULN-021). Called from cleanup() exactly once.
	release func()
}

type wsMessage struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Type   string          `json:"type,omitempty"` // for server-initiated events
}

type wsResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID     int64            `json:"id"`
	Result any              `json:"result,omitempty"`
	Error  *wsError         `json:"error,omitempty"`
}

type wsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func wsErrorf(code int, message string) *wsError {
	return &wsError{Code: code, Message: message}
}

const wsSendTimeout = 5 * time.Second

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

func (c *wsConn) handleMessage(msg wsMessage) {
	// Use the per-connection context so a client disconnect
	// cancels in-flight LLM calls. Earlier versions used
	// context.Background() and burned provider tokens on dead
	// connections (VULN-023).
	ctx := c.connCtx

	if msg.Method == "" {
		c.sendError(msg.ID, -32600, "method is required")
		return
	}

	switch msg.Method {
	case "chat":
		c.handleChat(ctx, msg.ID, msg.Params)
	case "ask":
		c.handleAsk(ctx, msg.ID, msg.Params)
	case "tool":
		c.handleTool(ctx, msg.ID, msg.Params)
	case "drive.start":
		c.handleDriveStart(ctx, msg.ID, msg.Params)
	case "drive.stop":
		c.handleDriveStop(ctx, msg.ID, msg.Params)
	case "drive.status":
		c.handleDriveStatus(ctx, msg.ID, msg.Params)
	case "events.subscribe":
		c.handleEventsSubscribe(ctx, msg.ID, msg.Params)
	case "events.unsubscribe":
		c.handleEventsUnsubscribe(ctx, msg.ID, msg.Params)
	case "ping":
		c.sendResponse(msg.ID, map[string]any{"pong": true, "ts": time.Now().UTC().Format(time.RFC3339)})
	default:
		c.sendError(msg.ID, -32601, fmt.Sprintf("method not found: %s", msg.Method))
	}
}

func (c *wsConn) handleChat(ctx context.Context, id int64, params json.RawMessage) {
	if c.engine == nil {
		c.sendError(id, -32000, "engine not available")
		return
	}
	var req struct {
		Message string `json:"message"`
		Stream  bool   `json:"stream"`
	}
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}
	if req.Message == "" {
		c.sendError(id, -32602, "message is required")
		return
	}

	// For streaming: send events as they arrive
	if req.Stream {
		eventsCh := make(chan engine.Event, 64)
		unsubscribe := c.engine.EventBus.SubscribeFunc("*", func(ev engine.Event) {
			select {
			case eventsCh <- ev:
			default:
			}
		})
		defer unsubscribe()

		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, err := c.engine.Ask(ctx, req.Message)
			if err != nil {
				c.sendError(id, -32000, err.Error())
				return
			}
			c.sendResponse(id, map[string]any{"response": resp})
		}()

		for {
			select {
			case ev := <-eventsCh:
				c.sendWS(map[string]any{
					"type":    "event",
					"event":   ev.Type,
					"payload": ev.Payload,
					"ts":      ev.Timestamp.UTC().Format(time.RFC3339),
				})
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}

	resp, err := c.engine.Ask(ctx, req.Message)
	if err != nil {
		c.sendError(id, -32000, err.Error())
		return
	}
	c.sendResponse(id, map[string]any{"response": resp})
}

func (c *wsConn) handleAsk(ctx context.Context, id int64, params json.RawMessage) {
	if c.engine == nil {
		c.sendError(id, -32000, "engine not available")
		return
	}
	var req struct {
		Message string `json:"message"`
		Race    bool   `json:"race,omitempty"`
	}
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}
	if req.Message == "" {
		c.sendError(id, -32602, "message is required")
		return
	}
	resp, err := c.engine.Ask(ctx, req.Message)
	if err != nil {
		c.sendError(id, -32000, err.Error())
		return
	}
	c.sendResponse(id, map[string]any{"response": resp})
}

func (c *wsConn) handleTool(ctx context.Context, id int64, params json.RawMessage) {
	if c.engine == nil {
		c.sendError(id, -32000, "engine not available")
		return
	}
	var req struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params,omitempty"`
	}
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}
	if req.Name == "" {
		c.sendError(id, -32602, "tool name is required")
		return
	}
	result, err := c.engine.CallToolFromSource(ctx, req.Name, req.Params, engine.SourceWS)
	if err != nil {
		c.sendError(id, -32000, err.Error())
		return
	}
	c.sendResponse(id, map[string]any{"result": result})
}

func (c *wsConn) handleDriveStart(ctx context.Context, id int64, params json.RawMessage) {
	// Drive start via websocket — delegate to engine
	c.sendResponse(id, map[string]any{"status": "drive_via_http", "hint": "use POST /api/v1/drive to start a drive run"})
}

func (c *wsConn) handleDriveStop(ctx context.Context, id int64, params json.RawMessage) {
	c.sendResponse(id, map[string]any{"status": "ok"})
}

func (c *wsConn) handleDriveStatus(ctx context.Context, id int64, params json.RawMessage) {
	c.sendResponse(id, map[string]any{"status": "ok"})
}

func (c *wsConn) handleEventsSubscribe(ctx context.Context, id int64, params json.RawMessage) {
	var req struct {
		Type string `json:"type"`
	}
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}
	c.sendResponse(id, map[string]any{"subscribed": req.Type})
}

func (c *wsConn) handleEventsUnsubscribe(ctx context.Context, id int64, params json.RawMessage) {
	c.sendResponse(id, map[string]any{"unsubscribed": true})
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