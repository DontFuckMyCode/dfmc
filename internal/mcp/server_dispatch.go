// server_dispatch.go — per-method handlers + dispatch table for the
// MCP server. Sibling of server.go which keeps the Server struct +
// constructor + Serve frame loop + handleRaw decoder + handleRequest
// validation pipeline + validateRequestID idempotency guard +
// writeResponse mutex-serialised writer.
//
// Splitting the per-method handlers out keeps server.go scoped to
// "how do we read frames safely and route them" while this file owns
// "what does each MCP method actually do." Adding a new MCP method
// means adding a case to dispatch + the handler here; the framing,
// init-gate, and idempotency machinery doesn't need to change.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// dispatch routes one request to its method handler. Method names come from
// the MCP spec; anything else is a method-not-found error.
func (s *Server) dispatch(ctx context.Context, req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized", "notifications/initialized":
		// Notification only — client signals handshake complete. Nothing to
		// reply with; dispatch's caller drops nil responses for notifications.
		s.markInitialized()
		return nil
	case "ping":
		return NewResponse(req.ID, map[string]any{})
	case "tools/list":
		if !s.requireInit(req) {
			return NewErrorResponse(req.ID, ErrInvalidRequest, "server not initialized", nil)
		}
		return s.handleListTools(req)
	case "tools/call":
		if !s.requireInit(req) {
			return NewErrorResponse(req.ID, ErrInvalidRequest, "server not initialized", nil)
		}
		return s.handleCallTool(ctx, req)
	default:
		return NewErrorResponse(req.ID, ErrMethodNotFound, "method not found: "+req.Method, nil)
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	var params InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return NewErrorResponse(req.ID, ErrInvalidParams, "initialize params: "+err.Error(), nil)
		}
	}
	s.markInitialized()
	return NewResponse(req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      s.info,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{ListChanged: false},
		},
	})
}

func (s *Server) handleListTools(req *Request) *Response {
	tools := s.bridge.List()
	if tools == nil {
		tools = []ToolDescriptor{}
	}
	return NewResponse(req.ID, ListToolsResult{Tools: tools})
}

func (s *Server) handleCallTool(ctx context.Context, req *Request) *Response {
	var params CallToolParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return NewErrorResponse(req.ID, ErrInvalidParams, "tools/call params: "+err.Error(), nil)
		}
	}
	if params.Name == "" {
		return NewErrorResponse(req.ID, ErrInvalidParams, "tools/call: name is required", nil)
	}
	res, err := s.bridge.Call(ctx, params.Name, params.Arguments)
	if err != nil {
		// Invocation failure (tool missing, panic). Report as CallToolResult
		// with IsError:true so the host can surface it, while still allowing
		// structured tool-level errors to arrive the same way. Per MCP spec
		// this is preferred over a protocol-level RPC error.
		return NewResponse(req.ID, CallToolResult{
			Content: []ContentBlock{TextContent(fmt.Sprintf("tool %q failed: %v", params.Name, err))},
			IsError: true,
		})
	}
	return NewResponse(req.ID, res)
}

func (s *Server) markInitialized() {
	s.initMu.Lock()
	s.inited = true
	s.initMu.Unlock()
}

func (s *Server) requireInit(_ *Request) bool {
	s.initMu.Lock()
	defer s.initMu.Unlock()
	return s.inited
}
