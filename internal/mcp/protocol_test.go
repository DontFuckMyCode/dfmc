package mcp

import (
	"encoding/json"
	"testing"
)

func TestRequest_IsNotification(t *testing.T) {
	cases := []struct {
		name string
		id   json.RawMessage
		want bool
	}{
		{"no ID (notification)", nil, true},
		{"null ID (notification)", json.RawMessage("null"), true},
		{"non-empty array (request)", json.RawMessage("[1]"), false},
		{"number ID (request)", json.RawMessage("1"), false},
		{"string ID (request)", json.RawMessage(`"abc"`), false},
		{"object ID (request)", json.RawMessage(`{}`), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Request{JSONRPC: "2.0", ID: tc.id, Method: "test"}
			if got := r.IsNotification(); got != tc.want {
				t.Errorf("IsNotification() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewResponse(t *testing.T) {
	id := json.RawMessage("42")
	res := NewResponse(id, map[string]string{"key": "value"})
	if res.JSONRPC != "2.0" {
		t.Fatalf("JSONRPC = %q, want 2.0", res.JSONRPC)
	}
	if string(res.ID) != "42" {
		t.Fatalf("ID = %s, want 42", string(res.ID))
	}
	if res.Error != nil {
		t.Fatalf("Error = %+v, want nil", res.Error)
	}
	if res.Result == nil {
		t.Fatal("Result = nil, want non-nil")
	}
}

func TestNewErrorResponse(t *testing.T) {
	id := json.RawMessage("7")
	res := NewErrorResponse(id, ErrInvalidParams, "missing name", map[string]any{"field": "name"})
	if res.JSONRPC != "2.0" {
		t.Fatalf("JSONRPC = %q, want 2.0", res.JSONRPC)
	}
	if string(res.ID) != "7" {
		t.Fatalf("ID = %s, want 7", string(res.ID))
	}
	if res.Result != nil {
		t.Fatalf("Result = %+v, want nil", res.Result)
	}
	if res.Error == nil {
		t.Fatal("Error = nil, want non-nil")
	}
	if res.Error.Code != ErrInvalidParams {
		t.Fatalf("Error.Code = %d, want %d", res.Error.Code, ErrInvalidParams)
	}
	if res.Error.Message != "missing name" {
		t.Fatalf("Error.Message = %q, want %q", res.Error.Message, "missing name")
	}
	if res.Error.Data == nil {
		t.Fatal("Error.Data = nil, want non-nil")
	}
}

func TestNewErrorResponse_NilIDForParseErrors(t *testing.T) {
	// Parse errors use nil ID per JSON-RPC 2.0 §4.1.
	res := NewErrorResponse(nil, ErrParseError, "bad json", nil)
	// Marshal and check that ID serialises as JSON null.
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	// buf must contain `"id":null` (not absent, not other value).
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if idVal, ok := m["id"]; !ok {
		t.Fatal("id field missing in response JSON")
	} else if idVal != nil {
		t.Fatalf("id = %v, want null", idVal)
	}
}

func TestTextContent(t *testing.T) {
	block := TextContent("hello world")
	if block.Type != "text" {
		t.Fatalf("Type = %q, want text", block.Type)
	}
	if block.Text != "hello world" {
		t.Fatalf("Text = %q, want %q", block.Text, "hello world")
	}
}

func TestProtocolVersionConstant(t *testing.T) {
	if ProtocolVersion != "2024-11-05" {
		t.Fatalf("ProtocolVersion = %q, want 2024-11-05", ProtocolVersion)
	}
}

func TestErrorCodes(t *testing.T) {
	if ErrParseError != -32700 {
		t.Fatalf("ErrParseError = %d, want -32700", ErrParseError)
	}
	if ErrInvalidRequest != -32600 {
		t.Fatalf("ErrInvalidRequest = %d, want -32600", ErrInvalidRequest)
	}
	if ErrMethodNotFound != -32601 {
		t.Fatalf("ErrMethodNotFound = %d, want -32601", ErrMethodNotFound)
	}
	if ErrInvalidParams != -32602 {
		t.Fatalf("ErrInvalidParams = %d, want -32602", ErrInvalidParams)
	}
	if ErrInternalError != -32603 {
		t.Fatalf("ErrInternalError = %d, want -32603", ErrInternalError)
	}
}

func TestContentBlock_Empty(t *testing.T) {
	block := ContentBlock{Type: "text"}
	if block.Type != "text" {
		t.Fatalf("Type = %q, want text", block.Type)
	}
	if block.Text != "" {
		t.Fatalf("Text = %q, want empty", block.Text)
	}
}

func TestInitializeParams_AllFields(t *testing.T) {
	raw := `{
		"protocolVersion": "2024-11-05",
		"clientInfo": {"name": "test", "version": "1.0"},
		"capabilities": {}
	}`
	var p InitializeParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.ProtocolVersion != "2024-11-05" {
		t.Fatalf("ProtocolVersion = %q", p.ProtocolVersion)
	}
	if p.ClientInfo.Name != "test" {
		t.Fatalf("ClientInfo.Name = %q", p.ClientInfo.Name)
	}
	if p.ClientInfo.Version != "1.0" {
		t.Fatalf("ClientInfo.Version = %q", p.ClientInfo.Version)
	}
}

func TestCallToolParams_ArgumentsPreserved(t *testing.T) {
	raw := `{"name": "echo", "arguments": {"msg": "hello"}}`
	var p CallToolParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "echo" {
		t.Fatalf("Name = %q, want echo", p.Name)
	}
	var args map[string]any
	if err := json.Unmarshal(p.Arguments, &args); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if args["msg"] != "hello" {
		t.Fatalf("arguments msg = %v, want hello", args["msg"])
	}
}

func TestToolDescriptor_InputSchema(t *testing.T) {
	raw := `{
		"name": "read_file",
		"description": "read a file",
		"inputSchema": {"type": "object", "properties": {"path": {"type": "string"}}}
	}`
	var td ToolDescriptor
	if err := json.Unmarshal([]byte(raw), &td); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if td.Name != "read_file" {
		t.Fatalf("Name = %q", td.Name)
	}
	if td.Description != "read a file" {
		t.Fatalf("Description = %q", td.Description)
	}
	schema, ok := td.InputSchema["type"]
	if !ok || schema != "object" {
		t.Fatalf("InputSchema[type] = %v", schema)
	}
}

func TestServerCapabilities_ToolsCapability(t *testing.T) {
	cap := ServerCapabilities{
		Tools: &ToolsCapability{ListChanged: false},
	}
	buf, err := json.Marshal(cap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(buf) == "" {
		t.Fatal("marshaled capability is empty")
	}
}

func TestRPCError_AllFields(t *testing.T) {
	rpcErr := RPCError{Code: -32602, Message: "bad params", Data: map[string]int{"field": 1}}
	buf, err := json.Marshal(rpcErr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var e RPCError
	if err := json.Unmarshal(buf, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Code != -32602 {
		t.Fatalf("Code = %d", e.Code)
	}
	if e.Message != "bad params" {
		t.Fatalf("Message = %q", e.Message)
	}
}

func TestRPCError_NoData(t *testing.T) {
	rpcErr := RPCError{Code: -32603, Message: "internal"}
	buf, err := json.Marshal(rpcErr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var e map[string]any
	if err := json.Unmarshal(buf, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, has := e["data"]; has {
		t.Fatal("data field should be omitted when nil")
	}
}
