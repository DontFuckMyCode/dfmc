// Package web hosts the embedded DFMC HTTP surface (`dfmc serve`).
// server_ws.go owns the connection-level WebSocket plumbing: the
// pre-upgrade gate (global + per-IP cap), the upgrade handler, the
// per-connection rate limiter, the half-open detector (ping/pong +
// read deadlines), readLoop / writeLoop, the cleanup unwind that
// cancels in-flight handlers, and the small send helpers.
//
// Per-method JSON-RPC dispatchers (chat / ask / tool / drive.* /
// events.*) live in server_ws_handlers.go. The WS endpoint is
// registered at GET /api/v1/ws (upgrade from SSE /ws).
//
// Protocol:
//   - Client → Server: JSON-RPC 2.0 requests (method + params + id)
//   - Server → Client: JSON-RPC 2.0 responses (result/error + id) OR
//                       SSE-like events (type + payload, no id needed)

package web

import (
	"context"
	"encoding/json"
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
	wsBurst int        = 10
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
	JSONRPC string   `json:"jsonrpc"`
	ID      int64    `json:"id"`
	Result  any      `json:"result,omitempty"`
	Error   *wsError `json:"error,omitempty"`
}

type wsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func wsErrorf(code int, message string) *wsError {
	return &wsError{Code: code, Message: message}
}

const wsSendTimeout = 5 * time.Second

// wsUpgraderFor + handleWebSocketUpgrade + readLoop + writeLoop +
// cleanup + sendResponse/sendError/sendWS live in server_ws_conn.go.
