package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

type Request struct {
	ProjectRoot string         `json:"project_root"`
	Params      map[string]any `json:"params,omitempty"`
}

type Result struct {
	Success    bool           `json:"success"`
	Output     string         `json:"output,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
	DurationMs int64          `json:"duration_ms"`
}

type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, req Request) (Result, error)
}

type Engine struct {
	mu             sync.RWMutex
	registry       map[string]Tool
	cfg            config.Config
	failureMu      sync.Mutex
	recentFailures map[string]int
}

func New(cfg config.Config) *Engine {
	e := &Engine{
		registry:       map[string]Tool{},
		cfg:            cfg,
		recentFailures: map[string]int{},
	}
	e.Register(NewReadFileTool())
	e.Register(NewListDirTool())
	e.Register(NewGrepCodebaseTool())
	return e
}

func (e *Engine) Register(tool Tool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.registry[tool.Name()] = tool
}

func (e *Engine) Get(name string) (Tool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.registry[name]
	return t, ok
}

func (e *Engine) List() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.registry))
	for name := range e.registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) Execute(ctx context.Context, name string, req Request) (Result, error) {
	start := time.Now()
	tool, ok := e.Get(name)
	if !ok {
		return Result{}, fmt.Errorf("tool not found: %s", name)
	}

	projectRoot := strings.TrimSpace(req.ProjectRoot)
	if projectRoot == "" {
		cwd, _ := os.Getwd()
		projectRoot = cwd
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project root: %w", err)
	}
	req.ProjectRoot = absRoot
	req.Params = normalizeToolParams(name, req.Params)
	failureKey := toolFailureKey(name, req.Params)

	res, err := tool.Execute(ctx, req)
	res.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		if n := e.trackFailure(failureKey); n >= 3 {
			return res, fmt.Errorf("tool %q failed repeatedly (%d times); change params or strategy", name, n)
		}
		return res, err
	}
	e.clearFailure(failureKey)
	res = e.compressToolOutput(req, res)
	res.Success = true
	return res, nil
}

func (e *Engine) trackFailure(key string) int {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	e.recentFailures[key]++
	return e.recentFailures[key]
}

func (e *Engine) clearFailure(key string) {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	delete(e.recentFailures, key)
}

func normalizeToolParams(name string, params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file":
		start := asInt(params, "line_start", 1)
		if start < 1 {
			start = 1
		}
		end := asInt(params, "line_end", start+199)
		if end < start {
			end = start
		}
		if end-start+1 > 400 {
			end = start + 399
		}
		params["line_start"] = start
		params["line_end"] = end
	case "list_dir":
		maxEntries := asInt(params, "max_entries", 200)
		if maxEntries <= 0 {
			maxEntries = 200
		}
		if maxEntries > 500 {
			maxEntries = 500
		}
		params["max_entries"] = maxEntries
	case "grep_codebase":
		maxResults := asInt(params, "max_results", 80)
		if maxResults <= 0 {
			maxResults = 80
		}
		if maxResults > 500 {
			maxResults = 500
		}
		params["max_results"] = maxResults
	}
	return params
}

func toolFailureKey(name string, params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(name)))
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(strings.TrimSpace(k))
		b.WriteString("=")
		b.WriteString(strings.TrimSpace(fmt.Sprint(params[k])))
	}
	return b.String()
}

func (e *Engine) compressToolOutput(req Request, res Result) Result {
	limit := e.resolveOutputByteLimit(req.Params)
	if limit <= 0 || strings.TrimSpace(res.Output) == "" {
		return res
	}
	out, compressed, omittedLines := compressOutput(res.Output, limit, collectRelevanceTerms(req.Params))
	if !compressed {
		return res
	}
	if res.Data == nil {
		res.Data = map[string]any{}
	}
	res.Data["output_original_bytes"] = len([]byte(res.Output))
	res.Data["output_compressed_bytes"] = len([]byte(out))
	res.Data["output_omitted_lines"] = omittedLines
	res.Output = out
	res.Truncated = true
	return res
}

func (e *Engine) resolveOutputByteLimit(params map[string]any) int {
	if v := asInt(params, "max_output_bytes", 0); v > 0 {
		return v
	}
	if v := asInt(params, "max_output_chars", 0); v > 0 {
		return v
	}
	return parseByteLimit(e.cfg.Security.Sandbox.MaxOutput, 100*1024)
}

func parseByteLimit(raw string, fallback int) int {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return fallback
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "KB"):
		mult = 1024
		s = strings.TrimSpace(strings.TrimSuffix(s, "KB"))
	case strings.HasSuffix(s, "MB"):
		mult = 1024 * 1024
		s = strings.TrimSpace(strings.TrimSuffix(s, "MB"))
	case strings.HasSuffix(s, "B"):
		mult = 1
		s = strings.TrimSpace(strings.TrimSuffix(s, "B"))
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fallback
	}
	return n * mult
}

func collectRelevanceTerms(params map[string]any) []string {
	if params == nil {
		return nil
	}
	keys := []string{"pattern", "query", "symbol", "name", "path"}
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, k := range keys {
		v := strings.TrimSpace(strings.ToLower(asString(params, k, "")))
		if v == "" {
			continue
		}
		for _, token := range strings.FieldsFunc(v, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '/' || r == ':' || r == ',' || r == ';' || r == '.'
		}) {
			t := strings.TrimSpace(token)
			if len(t) < 3 {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func compressOutput(output string, limit int, terms []string) (string, bool, int) {
	if len([]byte(output)) <= limit || limit <= 0 {
		return output, false, 0
	}

	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return truncateUTF8ByBytes(output, limit), true, 0
	}

	headN, tailN := 20, 20
	keep := map[int]struct{}{}
	for i := 0; i < minInt(headN, len(lines)); i++ {
		keep[i] = struct{}{}
	}
	for i := maxInt(0, len(lines)-tailN); i < len(lines); i++ {
		keep[i] = struct{}{}
	}

	if len(terms) > 0 {
		for i, line := range lines {
			low := strings.ToLower(line)
			for _, t := range terms {
				if strings.Contains(low, t) {
					for j := maxInt(0, i-1); j <= minInt(len(lines)-1, i+1); j++ {
						keep[j] = struct{}{}
					}
					break
				}
			}
		}
	}

	ordered := make([]int, 0, len(keep))
	for idx := range keep {
		ordered = append(ordered, idx)
	}
	sort.Ints(ordered)

	var b strings.Builder
	omitted := 0
	prev := -1
	for _, idx := range ordered {
		if prev >= 0 && idx > prev+1 {
			gap := idx - prev - 1
			omitted += gap
			b.WriteString(fmt.Sprintf("... [omitted %d lines]\n", gap))
		}
		b.WriteString(lines[idx])
		if idx < len(lines)-1 {
			b.WriteByte('\n')
		}
		prev = idx
	}

	compressed := b.String()
	if len([]byte(compressed)) > limit {
		compressed = truncateUTF8ByBytes(compressed, limit)
	}
	if len([]byte(compressed)) > limit {
		compressed = string([]byte(compressed)[:limit])
	}
	return compressed, true, omitted
}

func truncateUTF8ByBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	b := []byte(s)
	if len(b) <= maxBytes {
		return s
	}
	ellipsis := "\n... [truncated]"
	limit := maxBytes - len([]byte(ellipsis))
	if limit <= 0 {
		limit = maxBytes
		ellipsis = ""
	}
	var out strings.Builder
	n := 0
	for _, r := range s {
		rb := len([]byte(string(r)))
		if n+rb > limit {
			break
		}
		out.WriteRune(r)
		n += rb
	}
	if ellipsis != "" {
		out.WriteString(ellipsis)
	}
	return out.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func EnsureWithinRoot(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(absRoot, path)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root: %s", path)
	}
	return absPath, nil
}
