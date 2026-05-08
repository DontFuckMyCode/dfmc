package web

// server_types.go — JSON request bodies for the HTTP API. Sibling to
// server.go (Server struct + construction + routes), server_origin.go
// (origin / host helpers), and the per-domain handler siblings
// (server_chat.go, server_context.go, server_tools_skills.go,
// server_conversation.go, server_workspace.go, server_files.go,
// server_admin.go, server_drive.go, server_task.go).

type ChatRequest struct {
	Message string `json:"message"`
}

// AskRequest is the body of POST /api/v1/ask — a single-turn,
// non-streaming completion. Race mode fans the same prompt out to multiple
// providers in parallel and returns the first success; the winner's name
// comes back in the response so the caller can log or display it.
type AskRequest struct {
	Message       string   `json:"message"`
	Race          bool     `json:"race,omitempty"`
	RaceProviders []string `json:"race_providers,omitempty"`
}

type AnalyzeRequest struct {
	Path       string `json:"path"`
	Full       bool   `json:"full"`
	Security   bool   `json:"security"`
	Complexity bool   `json:"complexity"`
	DeadCode   bool   `json:"dead_code"`
	MagicDoc   bool   `json:"magicdoc"`

	MagicDocPath     string `json:"magicdoc_path"`
	MagicDocTitle    string `json:"magicdoc_title"`
	MagicDocHotspots int    `json:"magicdoc_hotspots"`
	MagicDocDeps     int    `json:"magicdoc_deps"`
	MagicDocRecent   int    `json:"magicdoc_recent"`
}

type ToolExecRequest struct {
	Params map[string]any `json:"params"`
}

type SkillExecRequest struct {
	Input   string `json:"input"`
	Message string `json:"message"`
}

type ConversationLoadRequest struct {
	ID string `json:"id"`
}

type ConversationBranchRequest struct {
	Name string `json:"name"`
}

type WorkspaceApplyRequest struct {
	Patch     string `json:"patch"`
	Source    string `json:"source"`
	CheckOnly bool   `json:"check_only"`
}

type PromptRenderRequest struct {
	Type              string            `json:"type"`
	Task              string            `json:"task"`
	Language          string            `json:"language"`
	Profile           string            `json:"profile"`
	Role              string            `json:"role"`
	Query             string            `json:"query"`
	ContextFiles      string            `json:"context_files"`
	Vars              map[string]string `json:"vars"`
	RuntimeProvider   string            `json:"runtime_provider"`
	RuntimeModel      string            `json:"runtime_model"`
	RuntimeToolStyle  string            `json:"runtime_tool_style"`
	RuntimeMaxContext int               `json:"runtime_max_context"`
}

type MagicDocUpdateRequest struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Hotspots int    `json:"hotspots"`
	Deps     int    `json:"deps"`
	Recent   int    `json:"recent"`
}
