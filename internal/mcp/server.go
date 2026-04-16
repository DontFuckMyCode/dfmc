package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Server is a line-delimited JSON-RPC 2.0 server over any reader/writer
// pair. In production the caller hooks it to stdin/stdout; tests pass
// in-memory pipes. The server is single-session: one Serve call handles one
// client until the reader returns EOF or ctx is cancelled.
type Server struct {
	in     io.Reader
	out    io.Writer
	bridge ToolBridge

	writeMu sync.Mutex

	info     ServerInfo
	initOnce sync.Once
	inited   bool
	initMu   sync.Mutex
}

// NewServer builds a Server. `info` is advertised back to the client during
// initialize (name + version of the DFMC build).
func NewServer(in io.Reader, out io.Writer, bridge ToolBridge, info ServerInfo) *Server {
	return &Server{in: in, out: out, bridge: bridge, info: info}
}

// Serve runs the JSON-RPC loop until the reader returns EOF or ctx is
// cancelled. Non-nil errors are transport failures; clean EOF returns nil.
func (s *Server) Serve(ctx context.Context) error {
	dec := json.NewDecoder(bufio.NewReader(s.in))
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			s.writeResponse(NewErrorResponse(nil, ErrParseError, "parse error: "+err.Error(), nil))
			return err
		}
		s.handleRaw(ctx, raw)
	}
}

// handleRaw decodes one frame and dispatches. Malformed frames yield a
// parse-error response with null ID, per JSON-RPC 2.0.
func (s *Server) handleRaw(ctx context.Context, raw json.RawMessage) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeResponse(NewErrorResponse(nil, ErrParseError, "invalid frame: "+err.Error(), nil))
		return
	}
	if req.JSONRPC != "2.0" {
		if !req.IsNotification() {
			s.writeResponse(NewErrorResponse(req.ID, ErrInvalidRequest, "jsonrpc must be \"2.0\"", nil))
		}
		return
	}
	resp := s.dispatch(ctx, &req)
	if req.IsNotification() {
		// Notifications never get a response, even on error. JSON-RPC 2.0 §4.1.
		return
	}
	if resp == nil {
		resp = NewErrorResponse(req.ID, ErrInternalError, "handler returned nil response", nil)
	}
	s.writeResponse(resp)
}

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

// writeResponse serialises `resp` and writes one frame. Writes are mutex-
// serialised so concurrent handlers (future streaming work) can't
// interleave bytes on the transport.
func (s *Server) writeResponse(resp *Response) {
	if resp == nil {
		return
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		fallback, _ := json.Marshal(NewErrorResponse(resp.ID, ErrInternalError, "encode response: "+err.Error(), nil))
		buf = fallback
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(buf)
	_, _ = s.out.Write([]byte{'\n'})
}
