// Passthrough / delegation methods for the Engine. Extracted from
// engine.go. Small wrappers around Memory, Conversation, provider
// status, and runtime-config operations. These are intentionally
// thin: the engine just decides "is the subsystem available?" and
// forwards.

package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	astBackend := ""
	astReason := ""
	var astLanguages []ast.BackendLanguageStatus
	var astMetrics ast.ParseMetrics
	var codemapMetrics codemap.BuildMetrics
	providerProfile := e.providerProfileStatusLocked()
	modelsDevCache := modelsDevCacheStatus()
	if e.AST != nil {
		bs := e.AST.BackendStatus()
		astBackend = bs.Active
		astReason = bs.Reason
		astLanguages = bs.Languages
		astMetrics = e.AST.ParseMetrics()
	}
	if e.CodeMap != nil {
		codemapMetrics = e.CodeMap.Metrics()
	}
	contextIn := cloneContextInStatus(e.lastContextIn)
	return Status{
		State:           e.state,
		ProjectRoot:     e.ProjectRoot,
		Provider:        e.provider(),
		Model:           e.model(),
		ProviderProfile: providerProfile,
		ModelsDevCache:  modelsDevCache,
		ContextIn:       contextIn,
		ASTBackend:      astBackend,
		ASTReason:       astReason,
		ASTLanguages:    astLanguages,
		ASTMetrics:      astMetrics,
		CodeMap:         codemapMetrics,
		MemoryDegraded:  e.memoryDegraded,
		MemoryLoadErr:   e.memoryLoadErr,
		ActiveDrives:    activeDriveStatuses(),
		EventsDropped:   e.EventBus.DroppedCount(),
	}
}

// activeDriveStatuses asks the drive package for currently-running
// runs and projects them into the status type. Lives here (not in
// drive_adapter.go) because Status() is the canonical aggregator
// and keeping the lookup inline avoids a per-field method indirection.
func activeDriveStatuses() []ActiveDriveStatus {
	active := drive.ListActive()
	if len(active) == 0 {
		return nil
	}
	out := make([]ActiveDriveStatus, 0, len(active))
	for _, a := range active {
		out = append(out, ActiveDriveStatus{RunID: a.RunID, Task: a.Task})
	}
	return out
}

func cloneContextInStatus(src ContextInStatus) *ContextInStatus {
	if strings.TrimSpace(src.Query) == "" && src.FileCount == 0 && src.TokenCount == 0 && len(src.Reasons) == 0 && len(src.Files) == 0 {
		return nil
	}
	copyStatus := src
	if len(src.Reasons) > 0 {
		copyStatus.Reasons = append([]string(nil), src.Reasons...)
	}
	if len(src.Files) > 0 {
		copyStatus.Files = append([]ContextInFileStatus(nil), src.Files...)
	}
	return &copyStatus
}
func (e *Engine) SetProviderModel(provider, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.providerOverride = provider
	e.modelOverride = model
}

func (e *Engine) SetVerbose(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verbose = v
}

func (e *Engine) ReloadConfig(cwd string) error {
	cfg, err := config.LoadWithOptions(config.LoadOptions{CWD: cwd})
	if err != nil {
		return err
	}
	projectRoot := config.FindProjectRoot(cwd)
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = strings.TrimSpace(e.ProjectRoot)
	}
	providers, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		return err
	}
	e.attachProviderObservers(providers)
	newTools := tools.New(*cfg)
	newTools.SetSubagentRunner(e)
	if toolReasoningEnabledForConfig(cfg) {
		newTools.SetReasoningPublisher(func(toolName, reason string) {
			e.EventBus.Publish(Event{
				Type:   "tool:reasoning",
				Source: "engine",
				Payload: map[string]any{
					"tool":   toolName,
					"reason": reason,
				},
			})
		})
	}

	e.mu.Lock()
	oldTools := e.Tools
	e.Config = cfg
	if strings.TrimSpace(projectRoot) != "" {
		e.ProjectRoot = projectRoot
	}
	e.Providers = providers
	e.Tools = newTools
	e.mu.Unlock()
	if oldTools != nil {
		if err := oldTools.Close(); err != nil {
			return fmt.Errorf("close old tools during reload: %w", err)
		}
	}
	e.refreshProjectConfigSnapshot(e.projectConfigPath())
	return nil
}

func (e *Engine) projectConfigPath() string {
	if e == nil {
		return ""
	}
	root := strings.TrimSpace(e.ProjectRoot)
	if root == "" {
		return ""
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml")
}

func (e *Engine) refreshProjectConfigSnapshot(path string) {
	if e == nil {
		return
	}
	path = strings.TrimSpace(path)
	var modTime time.Time
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime()
		}
	}
	e.mu.Lock()
	e.configProjectPath = path
	e.configProjectModTime = modTime
	e.mu.Unlock()
}

func (e *Engine) maybeAutoReloadProjectConfig() error {
	if e == nil {
		return nil
	}
	path := e.projectConfigPath()
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			e.refreshProjectConfigSnapshot(path)
			return nil
		}
		return err
	}

	e.mu.RLock()
	lastPath := e.configProjectPath
	lastModTime := e.configProjectModTime
	e.mu.RUnlock()
	if path == lastPath && !info.ModTime().After(lastModTime) {
		return nil
	}

	if err := e.ReloadConfig(e.ProjectRoot); err != nil {
		if e.EventBus != nil {
			e.EventBus.Publish(Event{
				Type:   "config:reload:auto_failed",
				Source: "engine",
				Payload: map[string]any{
					"path":  path,
					"error": err.Error(),
				},
			})
		}
		return fmt.Errorf("auto-reload config: %w", err)
	}
	if e.EventBus != nil {
		e.EventBus.Publish(Event{
			Type:   "config:reload:auto",
			Source: "engine",
			Payload: map[string]any{
				"path":       path,
				"updated_at": info.ModTime().Unix(),
			},
		})
	}
	return nil
}

// toolReasoningEnabled reports whether the per-tool-call self-narration
// surface (tool:reasoning events + the virtual `_reason` field on every
// tool's JSON schema) is active. Mirrors the AutonomousResume parser:
// "off"/"false"/"no"/"0" disable; any other value (including "" and
// "auto") enables. Centralised here so the publisher wiring at Init
// and any future schema gate read the same source of truth.
func (e *Engine) toolReasoningEnabled() bool {
	if e == nil {
		return true
	}
	return toolReasoningEnabledForConfig(e.Config)
}

func toolReasoningEnabledForConfig(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Agent.ToolReasoning)) {
	case "off", "false", "no", "0", "disabled":
		return false
	default:
		return true
	}
}

func (e *Engine) provider() string {
	if e.providerOverride != "" {
		return e.providerOverride
	}
	return e.Config.Providers.Primary
}

func (e *Engine) model() string {
	if e.modelOverride != "" {
		return e.modelOverride
	}
	profile, ok := e.Config.Providers.Profiles[e.provider()]
	if !ok {
		return ""
	}
	return profile.Model
}

func (e *Engine) providerProfileStatusLocked() ProviderProfileStatus {
	status := ProviderProfileStatus{
		Name: strings.TrimSpace(e.provider()),
	}
	if e.Config == nil {
		status.Model = strings.TrimSpace(e.model())
		return status
	}
	if status.Name == "" {
		status.Name = strings.TrimSpace(e.Config.Providers.Primary)
	}
	if profile, ok := e.Config.Providers.Profiles[status.Name]; ok {
		status.Model = strings.TrimSpace(profile.Model)
		status.Protocol = strings.TrimSpace(profile.Protocol)
		status.BaseURL = strings.TrimSpace(profile.BaseURL)
		status.MaxTokens = profile.MaxTokens
		status.MaxContext = profile.MaxContext
		status.Configured = providerProfileConfigured(status.Name, profile)
		status.Advisories = config.ProviderProfileAdvisories(status.Name, profile)
	}
	if status.Model == "" {
		status.Model = strings.TrimSpace(e.model())
	}
	if override := strings.TrimSpace(e.modelOverride); override != "" {
		status.Model = override
	}
	return status
}

func modelsDevCacheStatus() ModelsDevCacheStatus {
	path := config.ModelsDevCachePath()
	status := ModelsDevCacheStatus{
		Path: strings.TrimSpace(path),
	}
	if status.Path == "" {
		return status
	}
	info, err := os.Stat(status.Path)
	if err != nil {
		return status
	}
	status.Exists = true
	status.UpdatedAt = info.ModTime()
	status.SizeBytes = info.Size()
	return status
}

func providerProfileConfigured(name string, profile config.ModelConfig) bool {
	apiKey := strings.TrimSpace(profile.APIKey)
	baseURL := strings.TrimSpace(profile.BaseURL)

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "generic":
		return baseURL != ""
	default:
		return apiKey != "" || baseURL != ""
	}
}
func (e *Engine) MemoryWorking() memory.WorkingMemory {
	if e.Memory == nil {
		return memory.WorkingMemory{}
	}
	return e.Memory.Working()
}

func (e *Engine) MemoryList(tier types.MemoryTier, limit int) ([]types.MemoryEntry, error) {
	if e.Memory == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.List(tier, limit)
}

func (e *Engine) MemorySearch(query string, tier types.MemoryTier, limit int) ([]types.MemoryEntry, error) {
	if e.Memory == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Search(query, tier, limit)
}

func (e *Engine) MemoryAdd(entry types.MemoryEntry) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Add(entry)
}

func (e *Engine) MemoryClear(tier types.MemoryTier) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Clear(tier)
}

func (e *Engine) ConversationActive() *conversation.Conversation {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.Active()
}

func (e *Engine) ConversationSave() error {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.SaveActive()
}

func (e *Engine) ConversationStart() *conversation.Conversation {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.Start(e.provider(), e.model())
}

func (e *Engine) ConversationLoad(id string) (*conversation.Conversation, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.Load(id)
}

// ConversationLoadReadOnly returns a conversation without making it the
// active one. Used by preview / inspection surfaces (e.g. the TUI
// Conversations tab) where loading a row to peek must not silently
// swap the user's chat session.
func (e *Engine) ConversationLoadReadOnly(id string) (*conversation.Conversation, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.LoadReadOnly(id)
}

func (e *Engine) ConversationList() ([]conversation.Summary, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.List()
}

func (e *Engine) ConversationSearch(query string, limit int) ([]conversation.Summary, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.Search(query, limit)
}

// RecentConversationContext walks the active conversation backwards and
// extracts a compact view of the most recent activity: the last assistant
// message text (truncated to maxAssistantChars) and the names of up to N
// most recent tool calls. Returns zero values when the conversation is
// empty or unavailable. Cheap (one slice scan); safe to call on every
// user submit. Used by the intent layer to give its classifier just
// enough state to disambiguate "fix it" / "do that for the others".
type RecentConversation struct {
	LastAssistant     string   // truncated to maxAssistantChars runes
	LastAssistantRole string   // empty when no assistant turn exists yet
	RecentToolNames   []string // newest first, capped at maxToolNames
	UserTurnCount     int      // total user turns across the active branch
}

func (e *Engine) RecentConversationContext(maxAssistantChars, maxToolNames int) RecentConversation {
	out := RecentConversation{}
	if e == nil || e.Conversation == nil {
		return out
	}
	active := e.Conversation.Active()
	if active == nil {
		return out
	}
	msgs := active.Messages()
	if maxAssistantChars <= 0 {
		maxAssistantChars = 500
	}
	if maxToolNames <= 0 {
		maxToolNames = 5
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == types.RoleUser {
			out.UserTurnCount++
		}
		if out.LastAssistant == "" && m.Role == types.RoleAssistant {
			out.LastAssistantRole = string(m.Role)
			content := strings.TrimSpace(m.Content)
			if r := []rune(content); len(r) > maxAssistantChars {
				content = string(r[:maxAssistantChars]) + "..."
			}
			out.LastAssistant = content
		}
		if len(out.RecentToolNames) < maxToolNames {
			for _, tc := range m.ToolCalls {
				if name := strings.TrimSpace(tc.Name); name != "" {
					out.RecentToolNames = append(out.RecentToolNames, name)
					if len(out.RecentToolNames) >= maxToolNames {
						break
					}
				}
			}
		}
	}
	return out
}

func (e *Engine) ConversationBranchCreate(name string) error {
	if e.Conversation == nil {
		return fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchCreate(name)
}

func (e *Engine) ConversationBranchSwitch(name string) error {
	if e.Conversation == nil {
		return fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchSwitch(name)
}

func (e *Engine) ConversationBranchList() []string {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.BranchList()
}

func (e *Engine) ConversationBranchCompare(a, b string) (conversation.BranchComparison, error) {
	if e.Conversation == nil {
		return conversation.BranchComparison{}, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchCompare(a, b)
}

func (e *Engine) ConversationUndoLast() (int, error) {
	if e.Conversation == nil {
		return 0, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.UndoLast()
}
