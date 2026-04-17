// Passthrough / delegation methods for the Engine. Extracted from
// engine.go. Small wrappers around Memory, Conversation, provider
// status, and runtime-config operations. These are intentionally
// thin: the engine just decides "is the subsystem available?" and
// forwards.

package engine

import (
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
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
	}
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
	providers, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		return err
	}
	newTools := tools.New(*cfg)

	e.mu.Lock()
	e.Config = cfg
	e.Providers = providers
	e.Tools = newTools
	e.mu.Unlock()
	return nil
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
