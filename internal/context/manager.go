package context

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Manager struct {
	codemap *codemap.Engine
	prompts *promptlib.Library
}

type BuildOptions struct {
	MaxFiles         int
	MaxTokensTotal   int
	MaxTokensPerFile int
	Compression      string
	IncludeTests     bool
	IncludeDocs      bool
}

type PromptRuntime struct {
	Provider    string
	Model       string
	ToolStyle   string
	DefaultMode string
	Cache       bool
	LowLatency  bool
	MaxContext  int
	BestFor     []string
}

func New(cm *codemap.Engine) *Manager {
	return &Manager{
		codemap: cm,
		prompts: promptlib.New(),
	}
}

func (m *Manager) Build(query string, maxFiles int) ([]types.ContextChunk, error) {
	return m.BuildWithOptions(query, BuildOptions{
		MaxFiles:         maxFiles,
		MaxTokensTotal:   maxFiles * 1200,
		MaxTokensPerFile: 1200,
		Compression:      "standard",
		IncludeTests:     true,
		IncludeDocs:      true,
	})
}

func (m *Manager) BuildWithOptions(query string, opts BuildOptions) ([]types.ContextChunk, error) {
	if m == nil || m.codemap == nil || m.codemap.Graph() == nil {
		return nil, nil
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 6
	}
	if opts.MaxTokensPerFile <= 0 {
		opts.MaxTokensPerFile = 1200
	}
	if opts.MaxTokensTotal <= 0 {
		opts.MaxTokensTotal = opts.MaxFiles * opts.MaxTokensPerFile
	}
	if opts.MaxTokensTotal < 128 {
		opts.MaxTokensTotal = 128
	}
	opts.Compression = normalizeCompression(opts.Compression)

	terms := tokenizeQuery(query)
	scores := map[string]float64{}
	graph := m.codemap.Graph()

	for _, n := range graph.Nodes() {
		switch n.Kind {
		case "file":
			pathLower := strings.ToLower(n.Path)
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(pathLower, t) || strings.Contains(nameLower, t) {
					scores[n.Path] += 2.0
				}
			}
			if _, ok := scores[n.Path]; !ok {
				scores[n.Path] = 0.15
			}
		default:
			if n.Path == "" {
				continue
			}
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(nameLower, t) {
					scores[n.Path] += 3.0
				}
			}
		}
	}

	for _, hs := range graph.HotSpots(opts.MaxFiles * 3) {
		if hs.Path != "" {
			scores[hs.Path] += 1.0
		}
	}

	type ranked struct {
		Path  string
		Score float64
	}
	rankedPaths := make([]ranked, 0, len(scores))
	for path, score := range scores {
		rankedPaths = append(rankedPaths, ranked{Path: path, Score: score})
	}
	sort.Slice(rankedPaths, func(i, j int) bool {
		if rankedPaths[i].Score == rankedPaths[j].Score {
			return rankedPaths[i].Path < rankedPaths[j].Path
		}
		return rankedPaths[i].Score > rankedPaths[j].Score
	})

	chunks := make([]types.ContextChunk, 0, opts.MaxFiles)
	remaining := opts.MaxTokensTotal
	for _, r := range rankedPaths {
		if len(chunks) >= opts.MaxFiles || remaining <= 0 {
			break
		}
		if !shouldIncludePath(r.Path, opts.IncludeTests, opts.IncludeDocs) {
			continue
		}

		content, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		chunk := buildChunkForBudget(r.Path, string(content), terms, r.Score, opts.Compression, opts.MaxTokensPerFile)
		if chunk.TokenCount <= 0 || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if chunk.TokenCount > remaining {
			chunk = downshiftChunkForRemaining(chunk, remaining, opts.MaxTokensPerFile)
		}
		if chunk.TokenCount <= 0 || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		chunks = append(chunks, chunk)
		remaining -= chunk.TokenCount
	}

	return chunks, nil
}

func (m *Manager) BuildSystemPrompt(projectRoot, query string, chunks []types.ContextChunk, tools []string) string {
	return m.BuildSystemPromptWithRuntime(projectRoot, query, chunks, tools, PromptRuntime{})
}

func (m *Manager) BuildSystemPromptWithRuntime(projectRoot, query string, chunks []types.ContextChunk, tools []string, runtime PromptRuntime) string {
	if m == nil || m.prompts == nil {
		return "You are DFMC, a code intelligence assistant. Be concise, practical, and safe."
	}
	_ = m.prompts.LoadOverrides(projectRoot)

	task := promptlib.DetectTask(query)
	language := promptlib.InferLanguage(query, chunks)
	profile := detectPromptProfile(query, task)
	limits := promptRenderBudget(task, profile)
	injected := extractInjectedContext(projectRoot, query, limits.InjectedBlocks, limits.InjectedLines)
	if limits.InjectedTokens > 0 {
		injected = trimToTokenBudget(injected, limits.InjectedTokens)
	}
	return m.prompts.Render(promptlib.RenderRequest{
		Type:     "system",
		Task:     task,
		Language: language,
		Profile:  profile,
		Vars: map[string]string{
			"project_root":     projectRoot,
			"task":             task,
			"language":         language,
			"profile":          profile,
			"project_brief":    loadProjectBrief(projectRoot, limits.ProjectBriefTokens),
			"user_query":       strings.TrimSpace(query),
			"context_files":    summarizeContextFiles(projectRoot, chunks, limits.ContextFiles),
			"injected_context": injected,
			"tools_overview":   summarizeTools(tools, limits.ToolList),
			"tool_call_policy": buildToolCallPolicy(task, runtime),
			"response_policy":  buildResponsePolicy(task, profile),
		},
	})
}

func tokenizeQuery(query string) []string {
	parts := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.'
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func extractSnippet(content string, terms []string, maxLines int) (string, int, int) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return "", 1, 1
	}
	if maxLines <= 0 {
		maxLines = 60
	}

	needleIdx := -1
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, t := range terms {
			if strings.Contains(lower, t) {
				needleIdx = i
				break
			}
		}
		if needleIdx >= 0 {
			break
		}
	}

	start := 0
	end := len(lines)
	if needleIdx >= 0 {
		start = needleIdx - maxLines/2
		if start < 0 {
			start = 0
		}
		end = start + maxLines
		if end > len(lines) {
			end = len(lines)
		}
	} else if len(lines) > maxLines {
		end = maxLines
	}

	snippet := strings.Join(lines[start:end], "\n")
	return snippet, start + 1, end
}

func estimateTokens(content string) int {
	return len(strings.Fields(content))
}

func summarizeContextFiles(projectRoot string, chunks []types.ContextChunk, limit int) string {
	if len(chunks) == 0 || limit <= 0 {
		return "(none)"
	}
	overflow := 0
	if len(chunks) > limit {
		overflow = len(chunks) - limit
		chunks = chunks[:limit]
	}
	lines := make([]string, 0, len(chunks))
	for _, ch := range chunks {
		path := compactPath(projectRoot, ch.Path)
		if path == "" {
			path = "(unknown)"
		}
		lines = append(lines, fmt.Sprintf("- %s:%d-%d", path, ch.LineStart, ch.LineEnd))
	}
	if overflow > 0 {
		lines = append(lines, fmt.Sprintf("- ... +%d more files", overflow))
	}
	return strings.Join(lines, "\n")
}

func compactPath(projectRoot, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	absPath, errPath := filepath.Abs(p)
	absRoot, errRoot := filepath.Abs(strings.TrimSpace(projectRoot))
	if errPath == nil && errRoot == nil && strings.TrimSpace(absRoot) != "" {
		if rel, err := filepath.Rel(absRoot, absPath); err == nil {
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				p = rel
			}
		}
	}
	p = filepath.ToSlash(p)
	if len(p) <= 88 {
		return p
	}
	return p[:42] + ".../" + p[len(p)-40:]
}

func detectLanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	default:
		return ""
	}
}

var injectionMarker = regexp.MustCompile(`\[\[file:([^\]#]+?)(?:#L(\d+)(?:-L?(\d+))?)?\]\]`)

func extractInjectedContext(projectRoot, query string, maxBlocks, maxLines int) string {
	if strings.TrimSpace(projectRoot) == "" || strings.TrimSpace(query) == "" {
		return "(none)"
	}
	matches := injectionMarker.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return "(none)"
	}
	if maxBlocks <= 0 {
		maxBlocks = 3
	}
	if maxLines <= 0 {
		maxLines = 120
	}

	blocks := make([]string, 0, min(len(matches), maxBlocks))
	seen := map[string]struct{}{}
	for _, m := range matches {
		if len(blocks) >= maxBlocks {
			break
		}
		rel := strings.TrimSpace(m[1])
		if rel == "" {
			continue
		}
		key := rel + "#" + safeSub(m, 2) + "#" + safeSub(m, 3)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		abs, err := resolvePathWithinRoot(projectRoot, rel)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		lineStart := 1
		lineEnd := len(lines)
		if safeSub(m, 2) != "" {
			if n, err := strconv.Atoi(safeSub(m, 2)); err == nil && n > 0 {
				lineStart = n
			}
		}
		if safeSub(m, 3) != "" {
			if n, err := strconv.Atoi(safeSub(m, 3)); err == nil && n >= lineStart {
				lineEnd = n
			}
		}
		if lineStart > len(lines) {
			lineStart = len(lines)
		}
		if lineStart < 1 {
			lineStart = 1
		}
		if lineEnd > len(lines) {
			lineEnd = len(lines)
		}
		if lineEnd < lineStart {
			lineEnd = lineStart
		}
		if lineEnd-lineStart+1 > maxLines {
			lineEnd = lineStart + maxLines - 1
			if lineEnd > len(lines) {
				lineEnd = len(lines)
			}
		}

		snippet := strings.Join(lines[lineStart-1:lineEnd], "\n")
		lang := detectLanguageFromPath(rel)
		if lang == "" {
			lang = "text"
		}
		blocks = append(blocks,
			fmt.Sprintf("[[file:%s#L%d-L%d]]\n```%s\n%s\n```",
				filepath.ToSlash(rel), lineStart, lineEnd, lang, snippet))
	}
	if len(blocks) == 0 {
		return "(none)"
	}
	return strings.Join(blocks, "\n\n")
}

func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}
	return absTarget, nil
}

func safeSub(parts []string, idx int) string {
	if idx >= 0 && idx < len(parts) {
		return strings.TrimSpace(parts[idx])
	}
	return ""
}

func detectPromptProfile(query, task string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if strings.Contains(q, "detayli") || strings.Contains(q, "detaylı") || strings.Contains(q, "detailed") || strings.Contains(q, "deep") {
		return "deep"
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review", "planning":
		return "deep"
	default:
		return "compact"
	}
}

type renderBudget struct {
	ContextFiles       int
	ToolList           int
	InjectedBlocks     int
	InjectedLines      int
	InjectedTokens     int
	ProjectBriefTokens int
}

func promptRenderBudget(task, profile string) renderBudget {
	b := renderBudget{
		ContextFiles:       10,
		ToolList:           16,
		InjectedBlocks:     2,
		InjectedLines:      80,
		InjectedTokens:     320,
		ProjectBriefTokens: 180,
	}
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		b.ContextFiles = 16
		b.ToolList = 24
		b.InjectedBlocks = 3
		b.InjectedLines = 140
		b.InjectedTokens = 700
		b.ProjectBriefTokens = 320
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review":
		b.ContextFiles += 2
		b.InjectedTokens += 140
	case "planning":
		b.ContextFiles += 2
	}
	return b
}

func summarizeTools(tools []string, limit int) string {
	if len(tools) == 0 {
		return "(none)"
	}
	clean := make([]string, 0, len(tools))
	seen := map[string]struct{}{}
	for _, name := range tools {
		n := strings.TrimSpace(name)
		if n == "" {
			continue
		}
		k := strings.ToLower(n)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		clean = append(clean, n)
	}
	sort.Strings(clean)
	if limit > 0 && len(clean) > limit {
		clean = clean[:limit]
	}
	lines := make([]string, 0, len(clean))
	for _, n := range clean {
		lines = append(lines, "- "+n)
	}
	return strings.Join(lines, "\n")
}

func loadProjectBrief(projectRoot string, maxTokens int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxTokens <= 0 {
		return "(none)"
	}
	path := filepath.Join(root, ".dfmc", "magic", "MAGIC_DOC.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "(none)"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "(none)"
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= 48 {
			break
		}
	}
	if len(filtered) == 0 {
		return "(none)"
	}
	return trimToTokenBudget(strings.Join(filtered, "\n"), maxTokens)
}

func buildToolCallPolicy(task string, runtime PromptRuntime) string {
	lines := []string{
		"1) Call tools only when they reduce uncertainty materially.",
		"2) Prefer the minimum tool set needed for a reliable result.",
		"3) Keep calls narrow (targeted files/ranges/filters).",
		"4) Reuse prior tool outputs; avoid duplicate calls.",
		"5) Validate edited scope with the smallest relevant test first.",
	}
	switch strings.TrimSpace(strings.ToLower(runtime.ToolStyle)) {
	case "function-calling":
		lines = append(lines,
			"6) Tool protocol: emit strict function-call JSON that matches schema exactly.",
			"7) Keep tool payloads deterministic; no mixed prose inside arguments.")
	case "tool_use":
		lines = append(lines,
			"6) Tool protocol: emit tool_use blocks with strict JSON input.",
			"7) Pair each tool_result with the initiating tool_use id.")
	case "none":
		lines = append(lines,
			"6) Runtime has no native tool-calling; rely on provided context and direct reasoning.")
	default:
		lines = append(lines,
			"6) Follow provider-native tool format exactly; prioritize schema fidelity over verbosity.")
	}
	if runtime.MaxContext > 0 {
		toolOutputBudget := runtime.MaxContext / 5
		if toolOutputBudget < 96 {
			toolOutputBudget = 96
		}
		lines = append(lines, "8) Keep cumulative tool output near "+strconv.Itoa(toolOutputBudget)+" tokens unless risk requires deeper evidence.")
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		lines = append(lines,
			"9) Collect concrete evidence before remediation edits.",
			"10) Report exploitability conditions and confidence.")
	case "review":
		lines = append(lines,
			"9) Anchor findings to concrete evidence (file/line).",
			"10) Prioritize high-severity and high-confidence issues.")
	}
	return strings.Join(lines, "\n")
}

func buildResponsePolicy(task, profile string) string {
	depth := "compact"
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		depth = "deep"
	}
	lines := []string{
		"- Output depth: " + depth,
		"- Maximize signal density; avoid filler text.",
		"- Keep assumptions explicit and short.",
		"- Prefer precise file references for code claims.",
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "review", "security":
		lines = append(lines,
			"- Order findings by severity first.",
			"- Include impact and concrete fix guidance per finding.")
	case "planning":
		lines = append(lines,
			"- Provide phased execution plan with checkpoints.")
	default:
		lines = append(lines,
			"- Start with short answer, then critical details.")
	}
	return strings.Join(lines, "\n")
}

func normalizeCompression(v string) string {
	c := strings.ToLower(strings.TrimSpace(v))
	switch c {
	case "none", "standard", "aggressive":
		return c
	default:
		return "standard"
	}
}

func buildChunkForBudget(path, raw string, terms []string, score float64, compression string, maxTokensPerFile int) types.ContextChunk {
	levels := compressionFallbackOrder(compression)
	lang := detectLanguageFromPath(path)
	for _, lvl := range levels {
		content, lineStart, lineEnd := compressContent(raw, terms, lang, lvl, maxTokensPerFile)
		tc := estimateTokens(content)
		if tc <= 0 || strings.TrimSpace(content) == "" {
			continue
		}
		return types.ContextChunk{
			Path:        path,
			Language:    lang,
			Content:     content,
			LineStart:   lineStart,
			LineEnd:     lineEnd,
			TokenCount:  tc,
			Score:       score,
			Compression: lvl,
		}
	}
	return types.ContextChunk{}
}

func downshiftChunkForRemaining(chunk types.ContextChunk, remaining, maxTokensPerFile int) types.ContextChunk {
	if remaining <= 0 {
		return types.ContextChunk{}
	}
	budget := remaining
	if maxTokensPerFile > 0 && budget > maxTokensPerFile {
		budget = maxTokensPerFile
	}
	if chunk.TokenCount <= budget {
		return chunk
	}
	trimmed := trimToTokenBudget(chunk.Content, budget)
	if strings.TrimSpace(trimmed) == "" {
		return types.ContextChunk{}
	}
	chunk.Content = trimmed
	chunk.TokenCount = estimateTokens(trimmed)
	chunk.Compression = chunk.Compression + "+trim"
	return chunk
}

func compressionFallbackOrder(primary string) []string {
	switch normalizeCompression(primary) {
	case "none":
		return []string{"none", "standard", "aggressive"}
	case "aggressive":
		return []string{"aggressive"}
	default:
		return []string{"standard", "aggressive"}
	}
}

func compressContent(raw string, terms []string, lang, level string, maxTokens int) (string, int, int) {
	switch level {
	case "none":
		lineStart, lineEnd := 1, len(strings.Split(raw, "\n"))
		return trimToTokenBudget(raw, maxTokens), lineStart, lineEnd
	case "aggressive":
		sig := extractSignatures(raw, lang, 160)
		if strings.TrimSpace(sig) == "" {
			snippet, lineStart, lineEnd := extractSnippet(raw, terms, 30)
			return trimToTokenBudget(stripComments(snippet), maxTokens), lineStart, lineEnd
		}
		return trimToTokenBudget(sig, maxTokens), 1, minInt(160, len(strings.Split(raw, "\n")))
	default:
		snippet, lineStart, lineEnd := extractSnippet(raw, terms, 60)
		return trimToTokenBudget(stripComments(snippet), maxTokens), lineStart, lineEnd
	}
}

func trimToTokenBudget(content string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	words := strings.Fields(content)
	if len(words) <= maxTokens {
		return strings.TrimSpace(content)
	}
	suffix := "... [truncated for token budget]"
	suffixTokens := estimateTokens(suffix)
	if maxTokens <= suffixTokens {
		return strings.Join(words[:maxTokens], " ")
	}
	limit := maxTokens - suffixTokens
	if limit <= 0 {
		limit = maxTokens
	}
	return strings.Join(words[:limit], " ") + "\n" + suffix
}

func stripComments(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		l := line
		trim := strings.TrimSpace(l)
		if inBlock {
			if strings.Contains(trim, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trim, "/*") {
			if !strings.Contains(trim, "*/") {
				inBlock = true
			}
			continue
		}
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "#") {
			continue
		}
		if idx := strings.Index(l, "//"); idx >= 0 {
			l = l[:idx]
		}
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

var signatureLineRe = regexp.MustCompile(`^\s*(func|type|class|interface|def|fn|pub|const|var|let|struct|enum|impl|export\s+(function|class|const|let|type|interface)|async\s+function)\b`)

func extractSignatures(content, lang string, maxLines int) string {
	if maxLines <= 0 {
		maxLines = 120
	}
	lines := strings.Split(content, "\n")
	out := make([]string, 0, min(maxLines, len(lines)))
	for _, line := range lines {
		if len(out) >= maxLines {
			break
		}
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if signatureLineRe.MatchString(trim) {
			out = append(out, trim)
			continue
		}
		if (lang == "go" || lang == "rust" || lang == "java" || lang == "csharp") && strings.HasPrefix(trim, "package ") {
			out = append(out, trim)
			continue
		}
		if strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "from ") {
			out = append(out, trim)
		}
	}
	return strings.Join(out, "\n")
}

func shouldIncludePath(path string, includeTests, includeDocs bool) bool {
	p := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if p == "" {
		return false
	}
	if !includeTests {
		if strings.Contains(p, "/test/") || strings.Contains(p, "/tests/") || strings.HasSuffix(p, "_test.go") ||
			strings.HasSuffix(p, ".spec.ts") || strings.HasSuffix(p, ".test.ts") || strings.HasSuffix(p, ".spec.js") || strings.HasSuffix(p, ".test.js") {
			return false
		}
	}
	if !includeDocs {
		if strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".rst") || strings.HasSuffix(p, ".txt") || strings.Contains(p, "/docs/") {
			return false
		}
	}
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
