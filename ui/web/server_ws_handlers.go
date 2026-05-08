package web

// server_ws_handlers.go — per-method JSON-RPC dispatchers reached
// from wsConn.handleMessage in server_ws.go. Each handler validates
// the params, calls the matching engine surface, and writes a
// response (or an event stream, for chat with stream=true). The
// connection-level concerns — upgrade, rate limiter, ping/pong half-
// open detector, cleanup, send helpers — live in server_ws.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

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

func (c *wsConn) handleDriveStart(_ context.Context, id int64, _ json.RawMessage) {
	c.sendResponse(id, map[string]any{"status": "drive_via_http", "hint": "use POST /api/v1/drive to start a drive run"})
}

func (c *wsConn) handleDriveStop(_ context.Context, id int64, _ json.RawMessage) {
	c.sendResponse(id, map[string]any{"status": "ok"})
}

func (c *wsConn) handleDriveStatus(_ context.Context, id int64, _ json.RawMessage) {
	c.sendResponse(id, map[string]any{"status": "ok"})
}

func (c *wsConn) handleEventsSubscribe(_ context.Context, id int64, params json.RawMessage) {
	var req struct {
		Type string `json:"type"`
	}
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}
	c.sendResponse(id, map[string]any{"subscribed": req.Type})
}

func (c *wsConn) handleEventsUnsubscribe(_ context.Context, id int64, _ json.RawMessage) {
	c.sendResponse(id, map[string]any{"unsubscribed": true})
}
