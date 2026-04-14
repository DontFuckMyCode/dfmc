package types

import "time"

type SymbolKind string

const (
	SymbolFunction  SymbolKind = "function"
	SymbolMethod    SymbolKind = "method"
	SymbolClass     SymbolKind = "class"
	SymbolInterface SymbolKind = "interface"
	SymbolType      SymbolKind = "type"
	SymbolVariable  SymbolKind = "variable"
	SymbolConstant  SymbolKind = "constant"
	SymbolEnum      SymbolKind = "enum"
)

type Symbol struct {
	Name       string            `json:"name"`
	Kind       SymbolKind        `json:"kind"`
	Path       string            `json:"path"`
	Line       int               `json:"line"`
	Column     int               `json:"column"`
	Language   string            `json:"language"`
	Signature  string            `json:"signature,omitempty"`
	Doc        string            `json:"doc,omitempty"`
	Visibility string            `json:"visibility,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Tool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Enabled     bool              `json:"enabled"`
	Schema      map[string]any    `json:"schema,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type PluginType string

const (
	PluginGo     PluginType = "go"
	PluginScript PluginType = "script"
	PluginWASM   PluginType = "wasm"
)

type Plugin struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Type         PluginType        `json:"type"`
	Entry        string            `json:"entry"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Permissions  []string          `json:"permissions,omitempty"`
	Enabled      bool              `json:"enabled"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type Hook struct {
	Name      string            `json:"name"`
	Event     string            `json:"event"`
	Priority  int               `json:"priority"`
	Condition string            `json:"condition,omitempty"`
	Command   string            `json:"command,omitempty"`
	Enabled   bool              `json:"enabled"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type Skill struct {
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Description string            `json:"description"`
	Command     string            `json:"command,omitempty"`
	Pattern     string            `json:"pattern,omitempty"`
	Enabled     bool              `json:"enabled"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Provider struct {
	Name         string            `json:"name"`
	Model        string            `json:"model"`
	APIKey       string            `json:"api_key,omitempty"`
	BaseURL      string            `json:"base_url,omitempty"`
	MaxTokens    int               `json:"max_tokens,omitempty"`
	MaxContext   int               `json:"max_context,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type ContextChunk struct {
	Path        string  `json:"path"`
	Language    string  `json:"language"`
	Content     string  `json:"content"`
	LineStart   int     `json:"line_start"`
	LineEnd     int     `json:"line_end"`
	TokenCount  int     `json:"token_count"`
	Score       float64 `json:"score"`
	Compression string  `json:"compression"`
}

type MemoryTier string

const (
	MemoryWorking  MemoryTier = "working"
	MemoryEpisodic MemoryTier = "episodic"
	MemorySemantic MemoryTier = "semantic"
)

type MemoryEntry struct {
	ID         string            `json:"id"`
	Project    string            `json:"project"`
	Tier       MemoryTier        `json:"tier"`
	Category   string            `json:"category"`
	Key        string            `json:"key"`
	Value      string            `json:"value"`
	Confidence float64           `json:"confidence"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	LastUsedAt time.Time         `json:"last_used_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

type Message struct {
	Role      MessageRole        `json:"role"`
	Content   string             `json:"content"`
	Timestamp time.Time          `json:"timestamp"`
	TokenCnt  int                `json:"token_count,omitempty"`
	Metadata  map[string]string  `json:"metadata,omitempty"`
	ToolCalls []ToolCallRecord   `json:"tool_calls,omitempty"`
	Results   []ToolResultRecord `json:"tool_results,omitempty"`
}

type ToolCallRecord struct {
	Name      string            `json:"name"`
	Params    map[string]any    `json:"params"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type ToolResultRecord struct {
	Name      string            `json:"name"`
	Output    string            `json:"output"`
	Success   bool              `json:"success"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}
