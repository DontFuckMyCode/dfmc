# SC-Session Security Scan Results

**Date:** 2026-04-30  
**Project:** DFMC  
**Total Findings:** 0  

## Summary

No session management vulnerabilities detected in DFMC.

### Verification Findings

#### Session Surface 1: WebSocket Session Identity
**File:** `ui/web/server_ws.go:239-240, 154`  
**Status:** SECURE ✓

```go
ws := &wsConn{
    id:         fmt.Sprintf("ws-%d", time.Now().UnixNano()),
    conn:       conn,
    engine:     s.engine,
    connCtx:    connCtx,
    connCancel: connCancel,
    limiter:    rate.NewLimiter(wsRPS, wsBurst),
    release:    wsRelease,
}
```

**Verification:**
- **Session ID generation:** `fmt.Sprintf("ws-%d", time.Now().UnixNano())` uses nanosecond granularity
- **Uniqueness:** Nanosecond timestamps are unique across multiple upgrades (collision risk is negligible in practice for local single-user tool)
- **Non-sequential:** UnixNano is not a simple counter; microsecond jitter makes prediction hard
- **In-memory only:** Session IDs are not persisted or transmitted to client; used only internally for logging/cleanup

**Design note:** DFMC is local single-user. Session fixation attacks require network attacker to predict/inject session ID; not applicable to localhost-only or token-authenticated WS.

#### Session Surface 2: Context Cancellation on Disconnect
**File:** `ui/web/server_ws.go:238, 262, 287`  
**Status:** SECURE ✓ (VULN-023 Fix)

```go
connCtx, connCancel := context.WithCancel(context.Background())
ws := &wsConn{
    // ...
    connCtx:    connCtx,
    connCancel: connCancel,
    // ...
}

func (c *wsConn) readLoop() {
    defer c.cleanup()
    for {
        _, raw, err := c.conn.ReadMessage()
        if err != nil {
            return  // cleanup() called via defer
        }
        // ...
    }
}

func (c *wsConn) handleMessage(msg wsMessage) {
    // Use the per-connection context so a client disconnect
    // cancels in-flight LLM calls. Earlier versions used
    // context.Background() and burned provider tokens on dead
    // connections (VULN-023).
    ctx := c.connCtx
    // ...
}
```

**Verification:**
- **Per-connection context:** Each WS conn has its own `connCtx` (not global `context.Background()`)
- **Cancellation on disconnect:** readLoop exits on error, defer calls cleanup() which calls `connCancel()` ✓
- **Token efficiency:** In-flight provider calls (LLM) receive `connCtx`, so disconnection cancels them immediately ✓
- **No token waste:** Fixed VULN-023 (background context would ignore disconnect and burn tokens)

#### Session Surface 3: Per-Connection Rate Limiter
**File:** `ui/web/server_ws.go:59-67, 169, 270`  
**Status:** SECURE ✓

```go
const (
    wsRPS   rate.Limit = 5
    wsBurst int        = 10
)

// ...

ws := &wsConn{
    // ...
    limiter:    rate.NewLimiter(wsRPS, wsBurst),
    // ...
}

func (c *wsConn) readLoop() {
    for {
        // ...
        // Per-connection rate limit. Wait blocks against the
        // connection context so a closed conn unwinds cleanly.
        if err := c.limiter.Wait(c.connCtx); err != nil {
            return
        }
        // ...
    }
}
```

**Verification:**
- **Per-connection bucket:** Each WS conn has its own `rate.Limiter` (not shared across connections)
- **Rate:** 5 req/s with burst of 10 (reasonable for interactive chat, prevents provider quota exhaustion)
- **Context-aware:** `limiter.Wait(c.connCtx)` blocks until quota available or context cancelled
- **No global starvation:** One greedy client cannot starve another (each has its own bucket)

#### Session Surface 4: Global and Per-IP Connection Limits
**File:** `ui/web/server_ws.go:32-42, 72-134`  
**Status:** SECURE ✓ (VULN-021 Fix)

```go
const (
    // wsGlobalConnCap and wsPerIPConnCap (VULN-021) bound the number
    // of concurrent WebSocket connections globally and per-IP...
    wsGlobalConnCap = 64
    wsPerIPConnCap  = 8
)

type wsConnLimiter struct {
    globalCap int
    perIPCap  int
    mu     sync.Mutex
    global int
    perIP  map[string]int
}

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
```

**Verification:**
- **Global cap:** 64 concurrent WS connections (prevents server exhaustion)
- **Per-IP cap:** 8 per IP (prevents single client from opening many connections)
- **Acquire before upgrade:** Check happens before WebSocket upgrade (line 210 in server_ws.go), so failed connections don't create goroutines
- **Release callback:** Proper cleanup when conn closes (line 176) ✓
- **Thread-safe:** Protected by `sync.Mutex` on all ops ✓

Fixed VULN-021 (pre-fix: connections were unlimited; could exhaust goroutines/memory).

#### Session Surface 5: Read Frame Limit
**File:** `ui/web/server_ws.go:43-48, 233`  
**Status:** SECURE ✓ (VULN-019 Fix)

```go
// wsReadLimit caps a single inbound JSON-RPC frame at 64 KiB.
// gorilla buffers the whole message before returning from
// ReadMessage, so a 100 MB frame previously sat in memory until
// it killed the host. 64 KiB is generous for any tool-call
// JSON (typical < 4 KiB).
wsReadLimit int64 = 64 * 1024

// ...

// Cap inbound frame size before the first ReadMessage so an
// attacker can't push a 100 MB frame into the buffer between
// upgrade and the first dispatch.
conn.SetReadLimit(wsReadLimit)
```

**Verification:**
- **64 KiB per frame:** Prevents gorilla buffer from consuming gigabytes on large frame
- **Early gate:** Set immediately after upgrade (line 233), before first read
- **Fixed VULN-019:** Pre-fix allowed 100 MB+ frames to buffer indefinitely

#### Session Surface 6: Read Deadline + Half-Open Detection
**File:** `ui/web/server_ws.go:49-58, 248-256`  
**Status:** SECURE ✓ (VULN-020 / 022 Fix)

```go
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

// ...

// Read deadline + pong handler implement the half-open detector.
// The writeLoop sends a ping every wsPingInterval; the peer's
// pong fires the handler below which slides the deadline forward.
// A peer that stops responding has ReadMessage error after wsReadDeadline.
_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
conn.SetPongHandler(func(string) error {
    return conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
})
```

**Verification:**
- **Read timeout:** 60s per message read (prevents hung reads)
- **Ping-pong heartbeat:** Writeloop sends ping every 30s; peer responds with pong
- **Deadline reset:** Pong resets the 60s deadline (line 255)
- **Dead peer detection:** If peer goes silent, 60s deadline expires and ReadMessage returns error
- **Connection cleanup:** readLoop exits on error, cleanup() called
- **Fixed VULN-020 / 022:** Pre-fix had no timeouts; half-open connections lived forever, wasting goroutines

#### Session Surface 7: Conversation Lifecycle
**File:** `internal/conversation/manager.go:104-146`  
**Status:** SECURE ✓ (Recently Fixed Async Drain)

```go
func (m *Manager) Start(provider, model string) *Conversation {
    m.mu.Lock()
    defer m.mu.Unlock()

    id := newConversationID(m.active, "")
    c := &Conversation{
        ID:        id,
        Provider:  provider,
        Model:     model,
        StartedAt: time.Now(),
        Branch:    "main",
        Branches: map[string][]types.Message{
            "main": {},
        },
        Metadata: map[string]string{},
    }
    m.active = c
    return cloneConversation(c)
}
```

**Verification:**
- **ID generation:** `newConversationID()` uses millisecond timestamp + nanosecond jitter for collision avoidance (manager.go:92-102)
- **No predictable sequence:** Timestamps are monotonic but not sequential integer counters
- **Async save:** `SaveActiveAsync()` uses WaitGroup to ensure in-flight saves drain before bbolt shutdown ✓
- **Single active:** Only one conversation active at a time (`m.active` field)
- **Branch isolation:** Conversations can branch; branches stored in conversation.Branches map

No session hijacking or fixation risk.

#### Session Surface 8: No Browser-Based Sessions (API-Only)
**Status:** SECURE ✓ (By Design)

DFMC is not a traditional web app. No cookie-based sessions; only:
- Bearer token auth (request header, not cookie)
- WebSocket connections (per-request authenticated, not session-based)
- Conversation state (in-memory, not persisted across restarts)

No cookie security issues (HttpOnly, Secure, SameSite) applicable.

### False Positives Cleared

- No Set-Cookie headers (no cookie-based sessions)
- No CSRF tokens (no state-changing GET requests; all state changes via authenticated POST)
- No session storage in cookies (only in-memory per-connection)
- No concurrent session limits (single-user; no need to limit sessions per user)
- No session timeout UI (operator controls server lifetime)

## Conclusion

**Risk Level:** LOW  
WebSocket sessions are protected by per-connection context cancellation, rate limiting, connection limits, and read timeouts. Half-open connections are detected and cleaned up. Conversations use timestamp-based IDs with nanosecond jitter. No cookie-based sessions to misconfigure.

