package context

// manager.go — Manager type, BuildOptions / PromptRuntime types, New, and
// Invalidate. Public entry points split into siblings:
//
//   manager_build.go       — Build / BuildWithOptions retrieval pipeline.
//   manager_prompt.go      — BuildSystemPrompt / BuildSystemPromptWithRuntime
//                            / BuildSystemPromptBundle rendering surface.
//
// Stateless helpers also split into siblings:
//
//   chunk_helpers.go       — tokenizeQuery, extractSnippet,
//                            summarizeContextFiles, compactPath.
//   language_detector.go   — detectLanguageFromPath (file ext lookup).
//   ranking_heuristics.go  — refactorBoost, isLikelyEntryPoint,
//                            findImportCycles (codemap-driven scoring).
//   skill_aggregator.go    — appendSkillSections /
//                            appendSkillInventorySection /
//                            summarizeActiveSkills /
//                            summarizeSkillInventory.
//   budget_trimmer.go      — trimBundleToBudget (cache-aware token cap).

import (
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

// Manager coordinates context building, retrieval, and symbol-aware indexing.
type Manager struct {
	mu      sync.RWMutex
	codemap *codemap.Engine
	prompts *promptlib.Library
}

type RetrievalStrategy string

const (
	StrategyGeneral  RetrievalStrategy = "general"
	StrategySecurity RetrievalStrategy = "security"
	StrategyDebug    RetrievalStrategy = "debug"
	StrategyReview   RetrievalStrategy = "review"
	StrategyRefactor RetrievalStrategy = "refactor"
)

type BuildOptions struct {
	MaxFiles         int
	MaxTokensTotal   int
	MaxTokensPerFile int
	Compression      string
	IncludeTests     bool
	IncludeDocs      bool
	// SymbolAware enables codemap-driven retrieval: query identifiers
	// are resolved against symbol nodes, and matching files pull their
	// import-graph neighbors as context. Disable to force the pure
	// text-matching path (useful for reproducibility in tests).
	SymbolAware bool
	// GraphDepth bounds how many hops out from a resolved seed file we
	// walk through the import graph. Zero disables expansion even when
	// SymbolAware is set. Practical range: 1-2; larger values produce
	// diminishing returns at real cost to the budget.
	GraphDepth int
	// Strategy tunes retrieval for specific task types: security uses
	// deep cross-reference mining, debug focuses on call-sites, review
	// prioritizes hotspots and changed files, refactor walks both
	// import and export directions. Defaults to StrategyGeneral.
	Strategy RetrievalStrategy
	// ExcludeStaleFilters files recently modified by write_file/edit_file/apply_patch
	// from context retrieval so stale cached chunks are not served. The map
	// is keyed by absolute path; values are modification timestamps. Files
	// newer than the window are excluded from scoring/retrieval.
	ExcludeStaleFilters map[string]time.Time
	// SeenFiles tracks files that have already been provided via read_file
	// in this session (extracted from conversation history). These are
	// excluded from context retrieval to avoid sending the same content twice
	// via different channels — the model already has the file contents in
	// the conversation context.
	SeenFiles map[string]struct{}
}

type PromptRuntime struct {
	LearnedPatterns string // serialized learned patterns for context injection
	Provider        string
	Model           string
	ToolStyle       string
	DefaultMode     string
	Cache           bool
	LowLatency      bool
	MaxContext      int
	BestFor         []string
	// ActiveSkills carries the resolved skill list from BuildSystemPromptBundle
	// into the agent loop so executeToolWithLifecycle can consult Preferred/Allowed
	// tool lists when enforcing skill-scoped tool policy.
	ActiveSkills []string
}

func New(cm *codemap.Engine) *Manager {
	return &Manager{
		codemap: cm,
		prompts: promptlib.New(),
	}
}

// Invalidate forwards a file-modified signal to the codemap so the next
// BuildWithOptions call sees fresh symbol data. No-op on nil receiver or
// when the codemap isn't wired.
func (m *Manager) Invalidate(path string) {
	if m == nil || path == "" || m.codemap == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codemap.InvalidateFile(path)
}
