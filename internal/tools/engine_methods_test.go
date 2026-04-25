package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
)

// mockToolBridge implements mcp.ToolBridge for testing.
type mockToolBridge struct {
	list  []mcp.ToolDescriptor
	callF func(ctx context.Context, name string, args []byte) (mcp.CallToolResult, error)
}

func (m *mockToolBridge) List() []mcp.ToolDescriptor {
	return m.list
}

func (m *mockToolBridge) Call(ctx context.Context, name string, args []byte) (mcp.CallToolResult, error) {
	if m.callF != nil {
		return m.callF(ctx, name, args)
	}
	return mcp.CallToolResult{}, errors.New("not implemented")
}

func TestMCPAdapter_Name(t *testing.T) {
	adapter := &mcpToolAdapter{name: "my-mcp-tool", bridge: &mockToolBridge{}}
	if adapter.Name() != "my-mcp-tool" {
		t.Errorf("Name(): got %q", adapter.Name())
	}
}

func TestMCPAdapter_Description(t *testing.T) {
	adapter := &mcpToolAdapter{name: "my-mcp-tool", bridge: &mockToolBridge{}}
	if adapter.Description() == "" {
		t.Error("Description() is empty")
	}
	if adapter.Description() != "MCP tool: my-mcp-tool" {
		t.Errorf("Description(): got %q", adapter.Description())
	}
}

func TestMCPAdapter_Spec(t *testing.T) {
	adapter := &mcpToolAdapter{name: "my-mcp-tool", bridge: &mockToolBridge{}}
	spec := adapter.Spec()
	if spec.Name != "my-mcp-tool" {
		t.Errorf("Spec().Name: got %q", spec.Name)
	}
	if spec.Risk != RiskExecute {
		t.Errorf("Spec().Risk: got %v", spec.Risk)
	}
}

func TestMCPAdapter_Execute_Success(t *testing.T) {
	bridge := &mockToolBridge{
		list: []mcp.ToolDescriptor{{Name: "test-tool"}},
		callF: func(ctx context.Context, name string, args []byte) (mcp.CallToolResult, error) {
			return mcp.CallToolResult{
				Content: []mcp.ContentBlock{mcp.TextContent("success output")},
				IsError: false,
			}, nil
		},
	}
	adapter := &mcpToolAdapter{name: "test-tool", bridge: bridge}
	result, err := adapter.Execute(context.Background(), Request{Params: map[string]any{"arg": "value"}})
	if err != nil {
		t.Errorf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Expected Success=true")
	}
	if result.Output != "success output" {
		t.Errorf("Output: got %q", result.Output)
	}
}

func TestMCPAdapter_Execute_Error(t *testing.T) {
	bridge := &mockToolBridge{
		list: []mcp.ToolDescriptor{{Name: "failing-tool"}},
		callF: func(ctx context.Context, name string, args []byte) (mcp.CallToolResult, error) {
			return mcp.CallToolResult{}, errors.New("tool failed")
		},
	}
	adapter := &mcpToolAdapter{name: "failing-tool", bridge: bridge}
	_, err := adapter.Execute(context.Background(), Request{})
	if err == nil {
		t.Error("Expected error")
	}
}

func TestMCPAdapter_Execute_ErrorResult(t *testing.T) {
	bridge := &mockToolBridge{
		list: []mcp.ToolDescriptor{{Name: "error-tool"}},
		callF: func(ctx context.Context, name string, args []byte) (mcp.CallToolResult, error) {
			return mcp.CallToolResult{
				Content: []mcp.ContentBlock{mcp.TextContent("error output")},
				IsError: true,
			}, nil
		},
	}
	adapter := &mcpToolAdapter{name: "error-tool", bridge: bridge}
	result, err := adapter.Execute(context.Background(), Request{})
	if err != nil {
		t.Errorf("Execute error: %v", err)
	}
	if result.Success {
		t.Error("Expected Success=false for IsError=true")
	}
}

// Engine method tests

func TestEngine_TaskStore_Nil(t *testing.T) {
	e := New(*config.DefaultConfig())
	if e.TaskStore() != nil {
		t.Error("TaskStore() on fresh engine should be nil")
	}
}

func TestEngine_SetMCPBridge(t *testing.T) {
	e := New(*config.DefaultConfig())
	bridge := &mockToolBridge{
		list: []mcp.ToolDescriptor{{Name: "mcp-tool-a"}, {Name: "mcp-tool-b"}},
	}
	e.SetMCPBridge(bridge)
	if e.TaskStore() != nil {
		t.Error("TaskStore should still be nil")
	}
}

func TestEngine_List(t *testing.T) {
	e := New(*config.DefaultConfig())
	list := e.List()
	if len(list) == 0 {
		t.Error("List() on fresh engine should not be empty")
	}
}

func TestEngine_List_Sorted(t *testing.T) {
	e := New(*config.DefaultConfig())
	list := e.List()
	for i := 1; i < len(list); i++ {
		if list[i-1] > list[i] {
			t.Errorf("List() not sorted: %v", list)
			break
		}
	}
}

func TestEngine_Get(t *testing.T) {
	e := New(*config.DefaultConfig())
	tool, ok := e.Get("read_file")
	if !ok {
		t.Error("Get(read_file) should return true")
	}
	if tool == nil {
		t.Error("Get(read_file) should return non-nil tool")
	}
}

func TestEngine_Get_NotFound(t *testing.T) {
	e := New(*config.DefaultConfig())
	_, ok := e.Get("nonexistent-tool-xyz")
	if ok {
		t.Error("Get(nonexistent) should return false")
	}
}
