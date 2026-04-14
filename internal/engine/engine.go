package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type EngineState int

const (
	StateCreated EngineState = iota
	StateInitializing
	StateReady
	StateServing
	StateShuttingDown
	StateStopped
)

type Engine struct {
	Config       *config.Config
	Storage      *storage.Store
	EventBus     *EventBus
	ProjectRoot  string
	AST          *ast.Engine
	CodeMap      *codemap.Engine
	Context      *ctxmgr.Manager
	Providers    *provider.Router
	Tools        *tools.Engine
	Memory       *memory.Store
	Conversation *conversation.Manager
	Security     *security.Scanner

	providerOverride string
	modelOverride    string
	verbose          bool

	mu    sync.RWMutex
	state EngineState
}

type Status struct {
	State       EngineState `json:"state"`
	ProjectRoot string      `json:"project_root"`
	Provider    string      `json:"provider"`
	Model       string      `json:"model"`
}

type AnalyzeReport struct {
	ProjectRoot string            `json:"project_root"`
	Files       int               `json:"files"`
	Nodes       int               `json:"nodes"`
	Edges       int               `json:"edges"`
	Cycles      int               `json:"cycles"`
	HotSpots    []codemap.Node    `json:"hotspots"`
	Security    *security.Report  `json:"security,omitempty"`
	DeadCode    []DeadCodeItem    `json:"dead_code,omitempty"`
	Complexity  *ComplexityReport `json:"complexity,omitempty"`
}

type AnalyzeOptions struct {
	Path       string
	Full       bool
	Security   bool
	DeadCode   bool
	Complexity bool
}

type DeadCodeItem struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Occurrences int    `json:"occurrences"`
}

type FunctionComplexity struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Score int    `json:"score"`
}

type ComplexityReport struct {
	Files         int                  `json:"files"`
	Average       float64              `json:"average"`
	Max           int                  `json:"max"`
	TopFunctions  []FunctionComplexity `json:"top_functions,omitempty"`
	TopFiles      []FunctionComplexity `json:"top_files,omitempty"`
	TotalSymbols  int                  `json:"total_symbols"`
	ScannedSymbol int                  `json:"scanned_symbols"`
}

func New(cfg *config.Config) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		state:    StateCreated,
	}, nil
}

func (e *Engine) Init(ctx context.Context) error {
	e.setState(StateInitializing)
	e.EventBus.Publish(Event{Type: "engine:initializing", Source: "engine"})

	store, err := storage.Open(e.Config.DataDir())
	if err != nil {
		return fmt.Errorf("storage init failed: %w", err)
	}
	e.Storage = store
	e.AST = ast.New()
	e.CodeMap = codemap.New(e.AST)
	e.Context = ctxmgr.New(e.CodeMap)
	e.Tools = tools.New(*e.Config)
	e.Memory = memory.New(e.Storage)
	_ = e.Memory.Load()
	e.Conversation = conversation.New(e.Storage)
	e.Security = security.New()

	e.Providers, err = provider.NewRouter(e.Config.Providers)
	if err != nil {
		return fmt.Errorf("provider router init failed: %w", err)
	}

	e.ProjectRoot = config.FindProjectRoot("")
	if e.ProjectRoot != "" {
		go e.indexCodebase(ctx)
	}

	e.setState(StateReady)
	e.EventBus.Publish(Event{Type: "engine:ready", Source: "engine"})
	return nil
}

func (e *Engine) ListTools() []string {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.List()
}

func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (tools.Result, error) {
	if e.Tools == nil {
		return tools.Result{}, fmt.Errorf("tool engine is not initialized")
	}
	res, err := e.Tools.Execute(ctx, name, tools.Request{
		ProjectRoot: e.ProjectRoot,
		Params:      params,
	})
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "tool:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return res, err
	}
	e.EventBus.Publish(Event{
		Type:   "tool:complete",
		Source: "engine",
		Payload: map[string]any{
			"name":       name,
			"durationMs": res.DurationMs,
		},
	})
	return res, nil
}

func (e *Engine) StartServing() {
	e.setState(StateServing)
	e.EventBus.Publish(Event{Type: "engine:serving", Source: "engine"})
}

func (e *Engine) Shutdown() {
	e.setState(StateShuttingDown)
	e.EventBus.Publish(Event{Type: "engine:shutdown", Source: "engine"})

	if e.Conversation != nil {
		_ = e.Conversation.SaveActive()
	}
	if e.Memory != nil {
		_ = e.Memory.Persist()
	}
	if e.Storage != nil {
		_ = e.Storage.Close()
	}

	e.setState(StateStopped)
	e.EventBus.Publish(Event{Type: "engine:stopped", Source: "engine"})
}

func (e *Engine) State() EngineState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return Status{
		State:       e.state,
		ProjectRoot: e.ProjectRoot,
		Provider:    e.provider(),
		Model:       e.model(),
	}
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

func (e *Engine) setState(state EngineState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
}

func (e *Engine) indexCodebase(ctx context.Context) {
	start := time.Now()
	e.EventBus.Publish(Event{Type: "index:start", Source: "engine", Payload: e.ProjectRoot})
	paths, err := e.collectSourceFiles(e.ProjectRoot)
	if err != nil {
		e.EventBus.Publish(Event{Type: "index:error", Source: "engine", Payload: err.Error()})
		return
	}

	if e.CodeMap != nil {
		if err := e.CodeMap.BuildFromFiles(ctx, paths); err != nil {
			e.EventBus.Publish(Event{Type: "index:error", Source: "engine", Payload: err.Error()})
			return
		}
	}

	select {
	case <-ctx.Done():
		e.EventBus.Publish(Event{Type: "index:cancelled", Source: "engine"})
		return
	default:
	}
	e.EventBus.Publish(Event{
		Type:   "index:done",
		Source: "engine",
		Payload: map[string]any{
			"duration_ms": time.Since(start).Milliseconds(),
			"files":       len(paths),
		},
	})
}

func (e *Engine) Ask(ctx context.Context, question string) (string, error) {
	return e.AskWithMetadata(ctx, question)
}

func (e *Engine) AskWithMetadata(ctx context.Context, question string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if question == "" {
		return "", fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return "", fmt.Errorf("provider router is not initialized")
	}
	e.ensureIndexed(ctx)

	var chunks []types.ContextChunk
	if e.Context != nil {
		var err error
		chunks, err = e.Context.Build(question, 8)
		if err != nil {
			e.EventBus.Publish(Event{
				Type:    "context:error",
				Source:  "engine",
				Payload: err.Error(),
			})
		}
	}

	req := provider.CompletionRequest{
		Provider: e.provider(),
		Model:    e.model(),
		Messages: []provider.Message{
			{Role: types.RoleUser, Content: question},
		},
		Context: chunks,
	}
	if e.Context != nil {
		req.System = e.Context.BuildSystemPrompt(e.ProjectRoot)
	}

	resp, usedProvider, err := e.Providers.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	e.recordInteraction(question, resp.Text, usedProvider, resp.Model, resp.Usage.TotalTokens, chunks)
	e.EventBus.Publish(Event{
		Type:   "provider:complete",
		Source: "engine",
		Payload: map[string]any{
			"provider": usedProvider,
			"model":    resp.Model,
			"tokens":   resp.Usage.TotalTokens,
		},
	})
	return resp.Text, nil
}

func (e *Engine) StreamAsk(ctx context.Context, question string) (<-chan provider.StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return nil, fmt.Errorf("provider router is not initialized")
	}
	e.ensureIndexed(ctx)

	var chunks []types.ContextChunk
	if e.Context != nil {
		var err error
		chunks, err = e.Context.Build(question, 8)
		if err != nil {
			e.EventBus.Publish(Event{
				Type:    "context:error",
				Source:  "engine",
				Payload: err.Error(),
			})
		}
	}

	req := provider.CompletionRequest{
		Provider: e.provider(),
		Model:    e.model(),
		Messages: []provider.Message{
			{Role: types.RoleUser, Content: question},
		},
		Context: chunks,
	}
	if e.Context != nil {
		req.System = e.Context.BuildSystemPrompt(e.ProjectRoot)
	}

	stream, usedProvider, err := e.Providers.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan provider.StreamEvent, 32)
	go func() {
		defer close(out)
		var acc strings.Builder
		for ev := range stream {
			if ev.Type == provider.StreamDelta {
				acc.WriteString(ev.Delta)
			}
			out <- ev
			if ev.Type == provider.StreamError {
				return
			}
			if ev.Type == provider.StreamDone {
				answer := acc.String()
				if strings.TrimSpace(answer) != "" {
					tokenEstimate := estimateTokens(question) + estimateTokens(answer)
					e.recordInteraction(question, answer, usedProvider, req.Model, tokenEstimate, chunks)
					e.EventBus.Publish(Event{
						Type:   "provider:complete",
						Source: "engine",
						Payload: map[string]any{
							"provider": usedProvider,
							"model":    req.Model,
							"tokens":   tokenEstimate,
						},
					})
				}
				return
			}
		}
	}()
	return out, nil
}

func (e *Engine) recordInteraction(question, answer, providerName, model string, tokenCount int, chunks []types.ContextChunk) {
	if e.Conversation != nil {
		e.Conversation.AddMessage(providerName, model, types.Message{
			Role:      types.RoleUser,
			Content:   question,
			Timestamp: time.Now(),
		})
		e.Conversation.AddMessage(providerName, model, types.Message{
			Role:      types.RoleAssistant,
			Content:   answer,
			Timestamp: time.Now(),
			TokenCnt:  tokenCount,
			Metadata: map[string]string{
				"provider": providerName,
				"model":    model,
			},
		})
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, answer)
		for _, ch := range chunks {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, answer, 0.7)
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

func (e *Engine) ensureIndexed(ctx context.Context) {
	if e.CodeMap == nil || e.CodeMap.Graph() == nil {
		return
	}
	if len(e.CodeMap.Graph().Nodes()) > 0 {
		return
	}
	paths, err := e.collectSourceFiles(e.ProjectRoot)
	if err != nil || len(paths) == 0 {
		return
	}
	_ = e.CodeMap.BuildFromFiles(ctx, paths)
}

func (e *Engine) Analyze(ctx context.Context, path string) (AnalyzeReport, error) {
	return e.AnalyzeWithOptions(ctx, AnalyzeOptions{Path: path})
}

func (e *Engine) AnalyzeWithOptions(ctx context.Context, opts AnalyzeOptions) (AnalyzeReport, error) {
	root := e.ProjectRoot
	if strings.TrimSpace(opts.Path) != "" {
		root = opts.Path
	}
	paths, err := e.collectSourceFiles(root)
	if err != nil {
		return AnalyzeReport{}, err
	}
	if e.CodeMap != nil {
		_ = e.CodeMap.BuildFromFiles(ctx, paths)
	}
	report := AnalyzeReport{
		ProjectRoot: root,
		Files:       len(paths),
	}
	if e.CodeMap != nil && e.CodeMap.Graph() != nil {
		graph := e.CodeMap.Graph()
		report.Nodes = len(graph.Nodes())
		report.Edges = len(graph.Edges())
		report.Cycles = len(graph.Cycles())
		report.HotSpots = graph.HotSpots(10)
	}

	runSecurity := opts.Full || opts.Security
	runDeadCode := opts.Full || opts.DeadCode
	runComplexity := opts.Full || opts.Complexity

	if runSecurity && e.Security != nil {
		secReport, err := e.Security.ScanPaths(paths)
		if err != nil {
			return report, err
		}
		report.Security = &secReport
	}
	if runDeadCode {
		items, err := e.detectDeadCode(ctx, paths)
		if err != nil {
			return report, err
		}
		report.DeadCode = items
	}
	if runComplexity {
		cx, err := e.computeComplexity(ctx, paths)
		if err != nil {
			return report, err
		}
		report.Complexity = &cx
	}

	return report, nil
}

func (e *Engine) collectSourceFiles(root string) ([]string, error) {
	var out []string
	if strings.TrimSpace(root) == "" {
		return out, nil
	}

	skipDirs := map[string]struct{}{
		".git":         {},
		".dfmc":        {},
		"vendor":       {},
		"node_modules": {},
		"dist":         {},
		"build":        {},
		"bin":          {},
	}
	allowed := map[string]struct{}{
		".go": {}, ".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {},
		".py": {}, ".rs": {}, ".java": {}, ".cs": {}, ".php": {},
		".rb": {}, ".c": {}, ".h": {}, ".cpp": {}, ".cc": {}, ".hpp": {},
		".swift": {}, ".kt": {}, ".kts": {}, ".scala": {}, ".sql": {}, ".lua": {},
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, ok := skipDirs[d.Name()]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := allowed[ext]; ok || d.Name() == "Dockerfile" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (e *Engine) detectDeadCode(ctx context.Context, paths []string) ([]DeadCodeItem, error) {
	type symbolRef struct {
		File string
		Line int
		Kind string
	}
	symbols := map[string]symbolRef{}
	contents := map[string]string{}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		contents[path] = text
		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		for _, sym := range res.Symbols {
			key := sym.Name
			if key == "" {
				continue
			}
			if _, exists := symbols[key]; !exists {
				symbols[key] = symbolRef{
					File: path,
					Line: sym.Line,
					Kind: string(sym.Kind),
				}
			}
		}
	}

	wordCount := map[string]int{}
	for name := range symbols {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		total := 0
		for _, content := range contents {
			total += len(re.FindAllStringIndex(content, -1))
		}
		wordCount[name] = total
	}

	out := make([]DeadCodeItem, 0)
	for name, meta := range symbols {
		n := wordCount[name]
		if n <= 1 && !looksEntrypoint(name, meta.File) {
			out = append(out, DeadCodeItem{
				Name:        name,
				Kind:        meta.Kind,
				File:        filepath.ToSlash(meta.File),
				Line:        meta.Line,
				Occurrences: n,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurrences == out[j].Occurrences {
			if out[i].File == out[j].File {
				return out[i].Line < out[j].Line
			}
			return out[i].File < out[j].File
		}
		return out[i].Occurrences < out[j].Occurrences
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}

func (e *Engine) computeComplexity(ctx context.Context, paths []string) (ComplexityReport, error) {
	report := ComplexityReport{Files: len(paths)}
	functions := make([]FunctionComplexity, 0, 128)
	fileScores := make([]FunctionComplexity, 0, len(paths))
	totalScore := 0
	maxScore := 0
	totalSymbols := 0
	scannedSymbols := 0

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		fileScore := complexityScore(text)
		fileScores = append(fileScores, FunctionComplexity{
			Name:  filepath.Base(path),
			File:  filepath.ToSlash(path),
			Line:  1,
			Score: fileScore,
		})
		totalScore += fileScore
		if fileScore > maxScore {
			maxScore = fileScore
		}

		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		totalSymbols += len(res.Symbols)
		lines := strings.Split(text, "\n")
		for i, sym := range res.Symbols {
			kind := strings.ToLower(string(sym.Kind))
			if kind != "function" && kind != "method" {
				continue
			}
			scannedSymbols++
			start := sym.Line - 1
			if start < 0 || start >= len(lines) {
				continue
			}
			end := len(lines)
			for j := i + 1; j < len(res.Symbols); j++ {
				if res.Symbols[j].Line > sym.Line {
					end = res.Symbols[j].Line - 1
					break
				}
			}
			if end <= start {
				end = start + 1
			}
			segment := strings.Join(lines[start:minInt(end, len(lines))], "\n")
			score := complexityScore(segment)
			functions = append(functions, FunctionComplexity{
				Name:  sym.Name,
				File:  filepath.ToSlash(path),
				Line:  sym.Line,
				Score: score,
			})
		}
	}

	report.Max = maxScore
	if len(fileScores) > 0 {
		report.Average = math.Round((float64(totalScore)/float64(len(fileScores)))*100) / 100
	}
	report.TotalSymbols = totalSymbols
	report.ScannedSymbol = scannedSymbols

	sort.Slice(functions, func(i, j int) bool { return functions[i].Score > functions[j].Score })
	sort.Slice(fileScores, func(i, j int) bool { return fileScores[i].Score > fileScores[j].Score })
	if len(functions) > 20 {
		functions = functions[:20]
	}
	if len(fileScores) > 10 {
		fileScores = fileScores[:10]
	}
	report.TopFunctions = functions
	report.TopFiles = fileScores
	return report, nil
}

func complexityScore(text string) int {
	score := 1
	score += strings.Count(text, " if ")
	score += strings.Count(text, " for ")
	score += strings.Count(text, " switch ")
	score += strings.Count(text, " case ")
	score += strings.Count(text, " && ")
	score += strings.Count(text, " || ")
	score += strings.Count(text, " else if ")
	score += strings.Count(text, " catch ")
	score += strings.Count(text, " except ")
	score += strings.Count(text, " ? ")
	return score
}

func looksEntrypoint(name, file string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "main" || n == "init" {
		return true
	}
	if strings.HasPrefix(n, "test") {
		return true
	}
	base := strings.ToLower(filepath.Base(file))
	return strings.HasSuffix(base, "_test.go")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func estimateTokens(text string) int {
	return len(strings.Fields(text))
}
