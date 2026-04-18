package ast

import (
	"container/list"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type ParseError struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

type ParseResult struct {
	Path     string         `json:"path"`
	Language string         `json:"language"`
	Symbols  []types.Symbol `json:"symbols"`
	Imports  []string       `json:"imports"`
	Errors   []ParseError   `json:"errors,omitempty"`
	Hash     uint64         `json:"hash"`
	ParsedAt time.Time      `json:"parsed_at"`
	Duration time.Duration  `json:"duration"`
	// Backend records which extractor produced the symbols: "tree-sitter"
	// when the CGO bindings parsed the file cleanly, or "regex" when we
	// fell back (CGO disabled, parse failed, or language stub). Callers
	// surface this so consumers know when results are best-effort.
	Backend string `json:"backend,omitempty"`
}

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

// Pre-compiled regex patterns for extractSymbols â hoisted from function
// scope to package level so they are compiled exactly once, not on every call.
var (
	// JavaScript / TypeScript patterns
	reJSFunc       = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?function\s+([A-Za-z_]\w*)\s*\(`)
	reJSClass      = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_]\w*)\b`)
	reJSInterface  = regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_]\w*)\b`)
	reJSType       = regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_]\w*)\b`)
	reJSEnum       = regexp.MustCompile(`^\s*(?:export\s+)?const\s+enum\s+([A-Za-z_]\w*)\b|^\s*(?:export\s+)?enum\s+([A-Za-z_]\w*)\b`)
	reJSConstArrow = regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_]\w*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_]\w*)\s*=>`)

	// Python patterns
	rePyAsyncFunc = regexp.MustCompile(`^\s*async\s+def\s+([A-Za-z_]\w*)\s*\(`)
	rePyFunc      = regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)\s*\(`)
	rePyClass     = regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)\s*[:(]`)

	// Rust patterns
	reRustFunc   = regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+([A-Za-z_]\w*)\s*\(`)
	reRustStruct = regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)\b`)
	reRustEnum   = regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)\b`)
	reRustTrait  = regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)\b`)
)

// New constructs an AST engine with the default parse-cache capacity.
// Most call sites (tests, ad-hoc tools) want this; the long-running
// engine wires NewWithCacheSize so operators can override the cap from
// config without rebuilding.
func New() *Engine {
	return NewWithCacheSize(defaultParseCacheSize)
}

// NewWithCacheSize constructs an AST engine with an explicit LRU
// capacity. A non-positive value falls back to defaultParseCacheSize so
// a misconfigured `ast.cache_size: 0` doesn't disable parse caching
// (which would silently 100x the AST CPU cost on every codemap rebuild).
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
		Path:     path,
		Language: lang,
		Symbols:  symbols,
		Imports:  imports,
		Errors:   parseErrors,
		Hash:     hash,
		ParsedAt: time.Now(),
		Duration: time.Since(start),
		Backend:  backend,
	}

	e.cache.Set(path, res)
	e.metrics.recordParse(lang, backend, time.Since(start))
	return res, nil
}

func (e *Engine) detectLanguage(path string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	base := filepath.Base(path)

	if lang, ok := e.extToLang[ext]; ok {
		return lang
	}
	if lang, ok := e.extToLang[base]; ok {
		return lang
	}

	if len(content) > 2 && content[0] == '#' && content[1] == '!' {
		firstLine := string(content)
		if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
			firstLine = firstLine[:idx]
		}
		switch {
		case strings.Contains(firstLine, "python"):
			return "python"
		case strings.Contains(firstLine, "node"):
			return "javascript"
		case strings.Contains(firstLine, "bash"), strings.Contains(firstLine, "/sh"):
			return "bash"
		}
	}

	return ""
}

func extensionLanguageMap() map[string]string {
	return map[string]string{
		".go":           "go",
		".ts":           "typescript",
		".tsx":          "tsx",
		".js":           "javascript",
		".jsx":          "jsx",
		".mjs":          "javascript",
		".cjs":          "javascript",
		".py":           "python",
		".rs":           "rust",
		".java":         "java",
		".cs":           "csharp",
		".php":          "php",
		".rb":           "ruby",
		".c":            "c",
		".h":            "c",
		".cpp":          "cpp",
		".cc":           "cpp",
		".hpp":          "cpp",
		".swift":        "swift",
		".kt":           "kotlin",
		".kts":          "kotlin",
		".scala":        "scala",
		".sh":           "bash",
		".bash":         "bash",
		".zsh":          "bash",
		".html":         "html",
		".css":          "css",
		".yaml":         "yaml",
		".yml":          "yaml",
		".toml":         "toml",
		".sql":          "sql",
		".lua":          "lua",
		".hcl":          "hcl",
		".tf":           "hcl",
		"Dockerfile":    "dockerfile",
		"dockerfile":    "dockerfile",
		"Containerfile": "dockerfile",
	}
}

func hashContent(content []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(content)
	return h.Sum64()
}

func extractSymbols(path, lang string, content []byte) []types.Symbol {
	if lang == "go" {
		return extractGoSymbols(path, lang, content)
	}

	lines := strings.Split(string(content), "\n")
	var symbols []types.Symbol

	add := func(kind types.SymbolKind, name string, line int, signature string) {
		if strings.TrimSpace(name) == "" {
			return
		}
		symbols = append(symbols, types.Symbol{
			Name:      name,
			Kind:      kind,
			Path:      path,
			Line:      line,
			Column:    1,
			Language:  lang,
			Signature: signature,
		})
	}

	switch lang {
	case "typescript", "tsx", "javascript", "jsx":
		for i, line := range lines {
			switch {
			case reJSFunc.MatchString(line):
				m := reJSFunc.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			case reJSClass.MatchString(line):
				m := reJSClass.FindStringSubmatch(line)
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
			case reJSInterface.MatchString(line):
				m := reJSInterface.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			case reJSType.MatchString(line):
				m := reJSType.FindStringSubmatch(line)
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
			case reJSEnum.MatchString(line):
				m := reJSEnum.FindStringSubmatch(line)
				name := ""
				for _, candidate := range m[1:] {
					if strings.TrimSpace(candidate) != "" {
						name = candidate
						break
					}
				}
				add(types.SymbolEnum, name, i+1, strings.TrimSpace(line))
			case reJSConstArrow.MatchString(line):
				m := reJSConstArrow.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			}
		}
	case "python":
		for i, line := range lines {
			if m := rePyClass.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := rePyAsyncFunc.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := rePyFunc.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
				continue
			}
		}
	case "rust":
		for i, line := range lines {
			switch {
			case reRustFunc.MatchString(line):
				m := reRustFunc.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			case reRustStruct.MatchString(line):
				m := reRustStruct.FindStringSubmatch(line)
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
			case reRustEnum.MatchString(line):
				m := reRustEnum.FindStringSubmatch(line)
				add(types.SymbolEnum, m[1], i+1, strings.TrimSpace(line))
			case reRustTrait.MatchString(line):
				m := reRustTrait.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			}
		}
	}

	return symbols
}

func extractImports(lang string, content []byte) []string {
	if lang == "go" {
		return extractGoImports(content)
	}

	lines := strings.Split(string(content), "\n")
	set := map[string]struct{}{}

	add := func(v string) {
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"`)
		v = strings.Trim(v, `'`)
		if v != "" {
			set[v] = struct{}{}
		}
	}

	switch lang {
	case "typescript", "tsx", "javascript", "jsx":
		reImport := regexp.MustCompile(`^\s*import\s+.*from\s+['"]([^'"]+)['"]`)
		reRequire := regexp.MustCompile(`require\(['"]([^'"]+)['"]\)`)
		for _, line := range lines {
			if m := reImport.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
			if m := reRequire.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	case "python":
		reImport := regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_\.]+)`)
		reFrom := regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_\.]+)\s+import`)
		for _, line := range lines {
			if m := reImport.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
			if m := reFrom.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	case "rust":
		reUse := regexp.MustCompile(`^\s*use\s+([A-Za-z0-9_:]+)`)
		for _, line := range lines {
			if m := reUse.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	}

	imports := make([]string, 0, len(set))
	for k := range set {
		imports = append(imports, k)
	}
	return imports
}

type parseCache struct {
	maxSize int
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	order   *list.List
	index   map[string]*list.Element
}

type cacheEntry struct {
	result *ParseResult
	hash   uint64
}

func newParseCache(maxSize int) *parseCache {
	return &parseCache{
		maxSize: maxSize,
		entries: map[string]*cacheEntry{},
		order:   list.New(),
		index:   make(map[string]*list.Element, maxSize),
	}
}

func (c *parseCache) Get(path string, hash uint64) *ParseResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[path]
	if !ok || entry.hash != hash {
		return nil
	}
	// Move to back (most-recently-used) on hit
	if elem, ok := c.index[path]; ok {
		c.order.MoveToBack(elem)
	}
	return entry.result
}

func (c *parseCache) Set(path string, res *ParseResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.index[path]; ok {
		c.order.MoveToBack(elem)
	} else {
		c.order.PushBack(path)
	}
	c.index[path] = c.order.Back()
	c.entries[path] = &cacheEntry{result: res, hash: res.Hash}

	for len(c.entries) > c.maxSize {
		oldestElem := c.order.Front()
		oldest := oldestElem.Value.(string)
		c.order.Remove(oldestElem)
		delete(c.entries, oldest)
		delete(c.index, oldest)
	}
}
