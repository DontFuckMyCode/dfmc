// Package mcp implements the Model Context Protocol server surface for DFMC.
//
// MCP is the open protocol IDE hosts (Claude Desktop, Cursor, VSCode) use to
// talk to tool-providing servers over JSON-RPC 2.0. This file defines the
// wire types. `server.go` owns the stdio loop; `bridge.go` adapts the DFMC
// tool registry onto the protocol.
package mcp

import "encoding/json"

// ProtocolVersion is the MCP spec revision we advertise to clients. Clients
// that request a different version receive this value back — per spec the
// client decides whether the mismatch is acceptable.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 standard error codes. Reserved range is -32768..-32000; codes
// outside it are free for server-defined errors.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// Request is an inbound JSON-RPC call. A nil ID means notification (no
// response expected). Params is kept as RawMessage so handlers can decode
// into their own shapes without a double-parse.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response mirrors a JSON-RPC 2.0 response object. Exactly one of Result or
// Error is populated. We keep both as pointers so encoding/json omits the
// unused field.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the body of a JSON-RPC error response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// NewResponse builds a success response carrying `result` for request `id`.
func NewResponse(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Result: result}
}

// NewErrorResponse builds a failure response. Use ErrParseError / ErrInvalidRequest
// / ErrMethodNotFound / ErrInvalidParams / ErrInternalError for `code`.
func NewErrorResponse(id json.RawMessage, code int, message string, data any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: message, Data: data}}
}

// IsNotification reports whether the request is a notification (no ID, no
// response expected). JSON-RPC 2.0 §4.1.
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// InitializeParams is the client's initial handshake payload. We only care
// about the declared protocol version; client info is logged for diagnostics
// but not acted on.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ClientInfo      ClientInfo      `json:"clientInfo"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
}

// ClientInfo identifies the caller (IDE host or tool).
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is what we send back. `capabilities.tools` advertises our
// tool surface; everything else (prompts, resources, sampling) is unused.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

// ServerInfo identifies DFMC on the wire.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities is the capabilities object sent in InitializeResult. We
// only implement the tools surface; `listChanged` stays false until we grow
// dynamic tool registration.
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability declares tool support. `listChanged:false` tells clients
// the registry is static for this session.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ListToolsResult is the response to `tools/list`. Tools ship their
// JSON-Schema in InputSchema so the client can present them with typed
// argument pickers.
type ListToolsResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

// ToolDescriptor is the MCP shape for one tool. Distinct from DFMC's
// ToolSpec: MCP's wire protocol wants just {name, description, inputSchema}.
type ToolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CallToolParams is the `tools/call` request body.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is what we return for `tools/call`. `IsError:true` tells
// the host the call completed but the tool reported a failure — distinct
// from an RPC-level error.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is one piece of tool output. MCP allows text/image/resource;
// DFMC only emits text today.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextContent is a convenience for the common case.
func TextContent(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}
