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

// MaxFrameBytes caps a single JSON-RPC frame the server is willing to
// decode. The MCP spec requires newline-delimited frames with no embedded
// newlines, so per-frame ≈ per-line; 16 MiB is generous for any
// legitimate tools/list response or tool-call result while keeping a
// buggy or hostile peer from OOM-ing DFMC by streaming an unbounded
// document. Hit this cap and Serve writes a parse-error response back
// (with null id, per JSON-RPC 2.0 §5.1) and returns — the connection
// is unrecoverable because the rest of the stream is anyone's guess.
const MaxFrameBytes = 16 * 1024 * 1024

// Server is a line-delimited JSON-RPC 2.0 server over any reader/writer
// pair. In production the caller hooks it to stdin/stdout; tests pass
// in-memory pipes. The server is single-session: one Serve call handles one
// client until the reader returns EOF or ctx is cancelled.
type Server struct {
	in     io.Reader
	out    io.Writer
	bridge ToolBridge

	writeMu sync.Mutex

	info   ServerInfo
	inited bool
	initMu sync.Mutex

	// maxFrameBytes is the per-instance cap, defaulting to MaxFrameBytes.
	// Exposed for tests via a small white-box override; production code
	// should not lower this — 16 MiB is already generous and the protocol
	// is unrecoverable past a truncation.
	maxFrameBytes int
}

// NewServer builds a Server. `info` is advertised back to the client during
// initialize (name + version of the DFMC build).
func NewServer(in io.Reader, out io.Writer, bridge ToolBridge, info ServerInfo) *Server {
	return &Server{in: in, out: out, bridge: bridge, info: info, maxFrameBytes: MaxFrameBytes}
}

// Serve runs the JSON-RPC loop until the reader returns EOF or ctx is
// cancelled. Non-nil errors are transport failures; clean EOF returns nil.
//
// Framing is strict newline-delimited per MCP spec — bufio.Scanner with
// the default ScanLines splitter and a MaxFrameBytes cap. A peer that
// emits a frame larger than the cap gets one parse-error response and
// then the connection terminates: there's no way to re-sync mid-stream
// once we've truncated a frame. json.Decoder would have happily allocated
// up to RAM-exhaustion on a single multi-GB document, which is the
// failure mode this guards against — a buggy or hostile MCP server
// can no longer take down DFMC by mis-emitting JSON.
func (s *Server) Serve(ctx context.Context) error {
	frameCap := s.maxFrameBytes
	if frameCap <= 0 {
		frameCap = MaxFrameBytes
	}
	// Initial buffer is the smaller of 64 KiB (avoids realloc for common
	// frame sizes) and frameCap. bufio.Scanner.Buffer's effective cap is
	// max(maxArg, cap(initBuf)), so an initial buffer larger than frameCap
	// would silently raise the limit.
	initSize := 64 * 1024
	if initSize > frameCap {
		initSize = frameCap
	}
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 0, initSize), frameCap)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil
		}
		// Copy the line — Scanner reuses its internal buffer between
		// Scan calls, so handing the slice straight to handleRaw would
		// race the next iteration's read.
		line := append([]byte(nil), sc.Bytes()...)
		if len(line) == 0 {
			continue
		}
		s.handleRaw(ctx, line)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, bufio.ErrTooLong) {
			s.writeResponse(NewErrorResponse(nil, ErrParseError, fmt.Sprintf("frame exceeded %d bytes; connection terminated", frameCap), nil))
			return err
		}
		s.writeResponse(NewErrorResponse(nil, ErrParseError, "parse error: "+err.Error(), nil))
		return err
	}
	return nil
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
