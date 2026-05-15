package ast

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type ParseError struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

// ParseResult holds the extracted symbols and metadata for a single parsed file.
type ParseResult struct {
	Path     string         `json:"path"`
	Language string         `json:"language"`
	Symbols  []types.Symbol `json:"symbols"`
	Imports  []string       `json:"imports"`
	// ImportAliases is the per-binding alias table: every entry pairs
	// the source module + imported symbol with the local identifier
	// the surrounding code uses to reach it. Imports is a flat list of
	// just the module paths (deduped); ImportAliases keeps the
	// (Module, Symbol, Local) link so callers can resolve
	// `p.join(...)` back to `os.path.join` when the source said
	// `from os import path as p`. Nil for languages without alias
	// extraction; see internal/ast/imports_aliases.go.
	ImportAliases []ImportAlias `json:"import_aliases,omitempty"`
	Errors        []ParseError  `json:"errors,omitempty"`
	Hash          uint64        `json:"hash"`
	ParsedAt      time.Time     `json:"parsed_at"`
	Duration      time.Duration `json:"duration"`
	// Backend records which extractor produced the symbols: "tree-sitter"
	// when the CGO bindings parsed the file cleanly, or "regex" when we
	// fell back (CGO disabled, parse failed, or language stub). Callers
	// surface this so consumers know when results are best-effort.
	Backend string `json:"backend,omitempty"`
}

// Engine provides caching AST parsing with tree-sitter and regex fallback.
// Thread-safe for concurrent use.
type Engine struct {
	extToLang map[string]string
	cache     *parseCache
	metrics   *parseMetricsTracker
}

// defaultParseCacheSize is the LRU capacity used when the caller does
// not explicitly size the cache. ~10K entries fits a medium-large
// monorepo's hot working set without unbounded growth on a long-running
// `dfmc serve` process. Tunable per-engine via NewWithCacheSize and
// at runtime via the AST cache config knob (config.AST.CacheSize).
const defaultParseCacheSize = 10000

// New constructs an AST engine with the default parse-cache capacity.
func New() *Engine {
	return NewWithCacheSize(defaultParseCacheSize)
}

// NewWithCacheSize constructs an AST engine with an explicit LRU capacity.
func NewWithCacheSize(cacheSize int) *Engine {
	if cacheSize <= 0 {
		cacheSize = defaultParseCacheSize
	}
	return &Engine{
		extToLang: extensionLanguageMap(),
		cache:     newParseCache(cacheSize),
		metrics:   newParseMetricsTracker(),
	}
}

// Close releases cache-held memory.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	if e.cache != nil {
		e.cache.Clear()
	}
	if e.metrics != nil {
		e.metrics.reset()
	}
	return nil
}

func (e *Engine) ParseFile(ctx context.Context, path string) (*ParseResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	return e.ParseContent(ctx, path, content)
}

func (e *Engine) ParseContent(ctx context.Context, path string, content []byte) (*ParseResult, error) {
	start := time.Now()
	e.metrics.recordRequest()

	select {
	case <-ctx.Done():
		e.metrics.recordError("", "")
		return nil, ctx.Err()
	default:
	}

	lang := e.detectLanguage(path, content)
	if lang == "" {
		e.metrics.recordUnsupported(path)
		return nil, fmt.Errorf("unsupported language: %s", path)
	}

	hash := hashContent(content)
	if cached := e.cache.Get(path, hash); cached != nil {
		e.metrics.recordCacheHit(lang)
		return cached, nil
	}
	e.metrics.recordCacheMiss(lang)

	symbols, imports, parseErrors, backend, err := extractWithPreferredBackend(ctx, path, lang, content)
	if err != nil {
		e.metrics.recordError(lang, backend)
		return nil, err
	}

	res := &ParseResult{
		Path:          path,
		Language:      lang,
		Symbols:       symbols,
		Imports:       imports,
		ImportAliases: extractImportAliases(lang, content),
		Errors:        parseErrors,
		Hash:          hash,
		ParsedAt:      time.Now(),
		Duration:      time.Since(start),
		Backend:       backend,
	}

	e.cache.Set(path, res)
	e.metrics.recordParse(lang, backend, res.Duration)
	return res, nil
}

func (e *Engine) detectLanguage(path string, content []byte) string {
	if lang := detectLanguage(path); lang != "" {
		return lang
	}
	return detectLanguageFromContent(content)
}

func hashContent(content []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(content)
	return h.Sum64()
}

// extensionLanguageMap returns the extension → language tag map used
// both for initial extension-based detection and for Engine extToLang fields.
