package ast

import (
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
}

type Engine struct {
	extToLang map[string]string
	cache     *parseCache
}

func New() *Engine {
	return &Engine{
		extToLang: extensionLanguageMap(),
		cache:     newParseCache(10000),
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

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	hash := hashContent(content)
	if cached := e.cache.Get(path, hash); cached != nil {
		return cached, nil
	}

	lang := e.detectLanguage(path, content)
	if lang == "" {
		return nil, fmt.Errorf("unsupported language: %s", path)
	}

	symbols := extractSymbols(path, lang, content)
	imports := extractImports(lang, content)

	res := &ParseResult{
		Path:     path,
		Language: lang,
		Symbols:  symbols,
		Imports:  imports,
		Hash:     hash,
		ParsedAt: time.Now(),
		Duration: time.Since(start),
	}

	e.cache.Set(path, res)
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
	case "go":
		reFunc := regexp.MustCompile(`^\s*func\s+([A-Za-z_]\w*)\s*\(`)
		reMethod := regexp.MustCompile(`^\s*func\s*\([^)]*\)\s*([A-Za-z_]\w*)\s*\(`)
		reType := regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)\s+`)
		reConst := regexp.MustCompile(`^\s*const\s+([A-Za-z_]\w*)\b`)
		reVar := regexp.MustCompile(`^\s*var\s+([A-Za-z_]\w*)\b`)
		for i, line := range lines {
			if m := reMethod.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolMethod, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := reFunc.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := reType.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := reConst.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolConstant, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := reVar.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolVariable, m[1], i+1, strings.TrimSpace(line))
				continue
			}
		}
	case "typescript", "tsx", "javascript", "jsx":
		reFunc := regexp.MustCompile(`^\s*(?:export\s+)?function\s+([A-Za-z_]\w*)\s*\(`)
		reClass := regexp.MustCompile(`^\s*(?:export\s+)?class\s+([A-Za-z_]\w*)\b`)
		reInterface := regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_]\w*)\b`)
		reType := regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_]\w*)\b`)
		reConstArrow := regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_]\w*)\s*=\s*(?:async\s*)?\(`)
		for i, line := range lines {
			switch {
			case reFunc.MatchString(line):
				m := reFunc.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			case reClass.MatchString(line):
				m := reClass.FindStringSubmatch(line)
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
			case reInterface.MatchString(line):
				m := reInterface.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			case reType.MatchString(line):
				m := reType.FindStringSubmatch(line)
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
			case reConstArrow.MatchString(line):
				m := reConstArrow.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			}
		}
	case "python":
		reFunc := regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)\s*\(`)
		reClass := regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)\s*[:(]`)
		for i, line := range lines {
			if m := reClass.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := reFunc.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
				continue
			}
		}
	case "rust":
		reFunc := regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+([A-Za-z_]\w*)\s*\(`)
		reStruct := regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)\b`)
		reEnum := regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)\b`)
		reTrait := regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)\b`)
		for i, line := range lines {
			switch {
			case reFunc.MatchString(line):
				m := reFunc.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			case reStruct.MatchString(line):
				m := reStruct.FindStringSubmatch(line)
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
			case reEnum.MatchString(line):
				m := reEnum.FindStringSubmatch(line)
				add(types.SymbolEnum, m[1], i+1, strings.TrimSpace(line))
			case reTrait.MatchString(line):
				m := reTrait.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			}
		}
	}

	return symbols
}

func extractImports(lang string, content []byte) []string {
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
	case "go":
		reImport := regexp.MustCompile(`^\s*import\s+(?:"([^"]+)"|([A-Za-z_]\w*\s+)?"([^"]+)")`)
		for _, line := range lines {
			if m := reImport.FindStringSubmatch(line); len(m) > 0 {
				for i := 1; i < len(m); i++ {
					add(m[i])
				}
			}
		}
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
	order   []string
}

type cacheEntry struct {
	result *ParseResult
	hash   uint64
}

func newParseCache(maxSize int) *parseCache {
	return &parseCache{
		maxSize: maxSize,
		entries: map[string]*cacheEntry{},
		order:   make([]string, 0, maxSize),
	}
}

func (c *parseCache) Get(path string, hash uint64) *ParseResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[path]
	if !ok || entry.hash != hash {
		return nil
	}
	return entry.result
}

func (c *parseCache) Set(path string, res *ParseResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.entries[path]; !ok {
		c.order = append(c.order, path)
	}
	c.entries[path] = &cacheEntry{result: res, hash: res.Hash}

	if len(c.entries) > c.maxSize {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}
