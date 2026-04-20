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
	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
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
		SymbolAware:      true,
		GraphDepth:       2,
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
	// sources tracks the strongest provenance reason per file — later
	// written into each chunk's Source so UIs and coach rules can see
	// why retrieval picked what it did. Precedence: marker > symbol >
	// graph-neighborhood > query-match > hotspot (enforced via the
	// upgradeSource helper, not the map itself).
	sources := map[string]string{}
	graph := m.codemap.Graph()

	upgradeSource := func(path, candidate string) {
		if path == "" || candidate == "" {
			return
		}
		current, ok := sources[path]
		if !ok || chunkSourceRank(candidate) > chunkSourceRank(current) {
			sources[path] = candidate
		}
	}

	for _, n := range graph.Nodes() {
		switch n.Kind {
		case "file":
			pathLower := strings.ToLower(n.Path)
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(pathLower, t) || strings.Contains(nameLower, t) {
					scores[n.Path] += 2.0
					upgradeSource(n.Path, ChunkSourceQueryMatch)
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
					upgradeSource(n.Path, ChunkSourceQueryMatch)
				}
			}
		}
	}

	// Symbol-aware pass: resolve identifiers in the query against the
	// codemap's symbol index, boost defining files, and walk outward
	// through the import graph to surface sibling files (callers/peers
	// that share module neighborhoods).
	if opts.SymbolAware {
		idents := extractIdentifiers(query)
		seeds := resolveSymbolSeeds(graph, idents)
		// Symbol hits outrank generic query-match bonuses because the
		// resolution is semantic, not substring — we know the identifier
		// *is* a defined symbol, not just a coincidental character run.
		for path, strength := range seeds {
			scores[path] += 4.0 + strength
			upgradeSource(path, ChunkSourceSymbolMatch)
		}
		if len(seeds) > 0 && opts.GraphDepth > 0 {
			seedList := make([]string, 0, len(seeds))
			for path := range seeds {
				seedList = append(seedList, path)
			}
			for path, hops := range expandViaGraph(graph, seedList, opts.GraphDepth) {
				// Inverse-scale by hop distance so closer siblings win.
				bonus := 1.5 / float64(hops)
				scores[path] += bonus
				upgradeSource(path, ChunkSourceGraphNeighborhood)
			}
		}
	}

	for _, hs := range graph.HotSpots(opts.MaxFiles * 3) {
		if hs.Path != "" {
			scores[hs.Path] += 1.0
			upgradeSource(hs.Path, ChunkSourceHotspot)
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
		if src, ok := sources[r.Path]; ok {
			chunk.Source = src
		} else {
			chunk.Source = ChunkSourceQueryMatch
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
	return m.BuildSystemPromptBundle(projectRoot, query, chunks, tools, runtime).Text()
}

// BuildSystemPromptBundle is the cache-boundary-aware sibling of
// BuildSystemPromptWithRuntime. It returns the rendered prompt as an
// ordered list of PromptSections so providers that support prompt caching
// (Anthropic) can emit cache_control annotations on the stable prefix.
// Callers that need a flat string should call bundle.Text().
func (m *Manager) BuildSystemPromptBundle(projectRoot, query string, chunks []types.ContextChunk, tools []string, runtime PromptRuntime) *promptlib.PromptBundle {
	if m == nil || m.prompts == nil {
		return &promptlib.PromptBundle{
			Sections: []promptlib.PromptSection{
				{Label: "fallback", Text: "You are DFMC, a code intelligence assistant. Be concise, practical, and safe.", Cacheable: true},
			},
		}
	}
	overrideWarning := ""
	if err := m.prompts.LoadOverrides(projectRoot); err != nil {
		overrideWarning = "Prompt override warning: " + err.Error() + ". Falling back to embedded defaults for unreadable override roots."
	}

	task := promptlib.DetectTask(query)
	skillSelection := skills.ResolveForQuery(projectRoot, query, task)
	cleanQuery := skillSelection.Query
	if cleanQuery == "" {
		cleanQuery = strings.TrimSpace(query)
	}
	task = promptlib.DetectTask(cleanQuery)
	if primary, ok := skillSelection.Primary(); ok && strings.TrimSpace(primary.Task) != "" {
		task = strings.TrimSpace(primary.Task)
	}
	language := promptlib.InferLanguage(cleanQuery, chunks)
	role := ResolvePromptRole(cleanQuery, task)
	if primary, ok := skillSelection.Primary(); ok && strings.TrimSpace(primary.Role) != "" {
		role = strings.TrimSpace(primary.Role)
	}
	profile := ResolvePromptProfile(cleanQuery, task, runtime)
	if primary, ok := skillSelection.Primary(); ok && strings.TrimSpace(primary.Profile) != "" {
		profile = strings.TrimSpace(primary.Profile)
	}
	limits := ResolvePromptRenderBudget(task, profile, runtime)
	injected := BuildInjectedContextWithBudget(projectRoot, cleanQuery, limits)
	bundle := m.prompts.RenderBundle(promptlib.RenderRequest{
		Type:     "system",
		Task:     task,
		Language: language,
		Profile:  profile,
		Role:     role,
		Vars: map[string]string{
			"project_root":     projectRoot,
			"task":             task,
			"language":         language,
			"profile":          profile,
			"role":             role,
			"project_brief":    loadProjectBrief(projectRoot, limits.ProjectBriefTokens),
			"user_query":       strings.TrimSpace(cleanQuery),
			"context_files":    summarizeContextFiles(projectRoot, chunks, limits.ContextFiles),
			"injected_context": injected,
			"tools_overview":   summarizeTools(tools, limits.ToolList),
			"tool_call_policy": BuildToolCallPolicy(task, runtime),
			"response_policy":  BuildResponsePolicy(task, profile),
			"active_skills":    summarizeActiveSkills(skillSelection.Skills),
		},
	})
	bundle = appendSkillSections(bundle, skillSelection.Skills)
	if budget := PromptTokenBudget(task, profile, runtime); budget > 0 {
		bundle = trimBundleToBudget(bundle, budget)
	}
	if overrideWarning != "" {
		bundle.Sections = append([]promptlib.PromptSection{{
			Label:     "prompt_override_warning",
			Text:      overrideWarning,
			Cacheable: false,
		}}, bundle.Sections...)
	}
	return bundle
}

func appendSkillSections(bundle *promptlib.PromptBundle, active []skills.Skill) *promptlib.PromptBundle {
	if bundle == nil || len(active) == 0 {
		return bundle
	}
	extras := make([]promptlib.PromptSection, 0, len(active))
	for _, skill := range active {
		text := strings.TrimSpace(skills.RenderSystemText(skill))
		if text == "" {
			continue
		}
		extras = append(extras, promptlib.PromptSection{
			Label:     "skill." + strings.ToLower(strings.TrimSpace(skill.Name)),
			Text:      text,
			Cacheable: true,
		})
	}
	if len(extras) == 0 {
		return bundle
	}

	sections := make([]promptlib.PromptSection, 0, len(bundle.Sections)+len(extras))
	sections = append(sections, extras...)
	sections = append(sections, bundle.Sections...)
	bundle.Sections = sections
	return bundle
}

func summarizeActiveSkills(active []skills.Skill) string {
	if len(active) == 0 {
		return ""
	}
	names := make([]string, 0, len(active))
	for _, skill := range active {
		if name := strings.TrimSpace(skill.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// trimBundleToBudget applies a token cap across the bundle. The dynamic
// (non-cacheable) sections carry the user query and per-request context —
// losing them defeats the entire prompt. So we reserve a floor for dynamic
// content first, trim the cacheable prefix into whatever is left, and only
// then let the dynamic sections use the remainder (plus any slack the stable
// prefix didn't consume).
func trimBundleToBudget(bundle *promptlib.PromptBundle, budget int) *promptlib.PromptBundle {
	if bundle == nil || budget <= 0 || len(bundle.Sections) == 0 {
		return bundle
	}

	dynamicTokens := 0
	dynamicCount := 0
	for _, s := range bundle.Sections {
		if !s.Cacheable {
			dynamicCount++
			dynamicTokens += estimateTokens(s.Text)
		}
	}

	dynamicFloor := 0
	if dynamicCount > 0 {
		// Reserve up to 25% of the budget for dynamic content (with a hard
		// 180-token floor), so the user query and per-request context survive
		// even when the stable prefix is large. Cap by actual dynamic size so
		// we don't over-reserve when there isn't much dynamic content.
		dynamicFloor = budget / 4
		dynamicFloor = max(180, dynamicFloor)
		dynamicFloor = min(dynamicFloor, dynamicTokens)
		if dynamicFloor > budget {
			dynamicFloor = budget
		}
	}

	stableBudget := budget - dynamicFloor
	stableBudget = max(0, stableBudget)

	out := &promptlib.PromptBundle{Sections: make([]promptlib.PromptSection, 0, len(bundle.Sections))}
	stableRemaining := stableBudget
	dynamicRemaining := dynamicFloor
	for _, s := range bundle.Sections {
		text := s.Text
		if s.Cacheable {
			if tok := estimateTokens(text); tok > stableRemaining {
				text = trimToTokenBudget(text, stableRemaining)
			}
			stableRemaining -= estimateTokens(text)
			// Any unused stable budget rolls forward to dynamic — the
			// reverse (dynamic → stable) is deliberately disallowed to keep
			// cache prefixes stable across turns.
			if stableRemaining < 0 {
				stableRemaining = 0
			}
		} else {
			allowance := dynamicRemaining + stableRemaining
			if tok := estimateTokens(text); tok > allowance {
				text = trimToTokenBudget(text, allowance)
			}
			used := estimateTokens(text)
			if used <= dynamicRemaining {
				dynamicRemaining -= used
			} else {
				used -= dynamicRemaining
				dynamicRemaining = 0
				stableRemaining -= used
				if stableRemaining < 0 {
					stableRemaining = 0
				}
			}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out.Sections = append(out.Sections, promptlib.PromptSection{
			Label:     s.Label,
			Text:      text,
			Cacheable: s.Cacheable,
		})
	}
	return out
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
		start = max(0, start)
		end = start + maxLines
		end = min(len(lines), end)
	} else if len(lines) > maxLines {
		end = maxLines
	}

	snippet := strings.Join(lines[start:end], "\n")
	return snippet, start + 1, end
}

func estimateTokens(content string) int {
	return tokens.Estimate(content)
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
		tag := ""
		switch ch.Source {
		case ChunkSourceSymbolMatch:
			tag = " (symbol)"
		case ChunkSourceGraphNeighborhood:
			tag = " (neighbor)"
		case ChunkSourceMarker:
			tag = " (pinned)"
		case ChunkSourceHotspot:
			tag = " (hotspot)"
		}
		lines = append(lines, fmt.Sprintf("- %s:%d-%d%s", path, ch.LineStart, ch.LineEnd, tag))
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

var (
	injectionMarker  = regexp.MustCompile(`\[\[file:([^\]#]+?)(?:#L(\d+)(?:-L?(\d+))?)?\]\]`)
	queryCodeBlockRe = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)\\r?\\n(.*?)\\r?\\n?```")
)

func extractInjectedContext(projectRoot, query string, maxBlocks, maxLines int) string {
	if strings.TrimSpace(query) == "" {
		return "(none)"
	}
	matches := injectionMarker.FindAllStringSubmatch(query, -1)
	if maxBlocks <= 0 {
		maxBlocks = 3
	}
	if maxLines <= 0 {
		maxLines = 120
	}

	blocks := make([]string, 0, maxBlocks)
	if strings.TrimSpace(projectRoot) != "" && len(matches) > 0 {
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
				lineEnd = min(len(lines), lineStart+maxLines-1)
			}

			snippet := strings.Join(lines[lineStart-1:lineEnd], "\n")
			lang := detectLanguageFromPath(rel)
			if lang == "" {
				lang = "text"
			}
			blocks = append(blocks,
				fmt.Sprintf("%s%s#L%d-L%d%s\n```%s\n%s\n```",
					types.FileMarkerPrefix, filepath.ToSlash(rel), lineStart, lineEnd, types.FileMarkerSuffix, lang, snippet))
		}
	}
	if len(blocks) < maxBlocks {
		for i, block := range extractQueryCodeBlocks(query, maxBlocks-len(blocks), maxLines) {
			if strings.TrimSpace(block) == "" {
				continue
			}
			blocks = append(blocks, fmt.Sprintf("[[query-code:%d]]\n%s", i+1, block))
			if len(blocks) >= maxBlocks {
				break
			}
		}
	}
	if len(blocks) == 0 {
		return "(none)"
	}
	return strings.Join(blocks, "\n\n")
}

func extractQueryCodeBlocks(query string, maxBlocks, maxLines int) []string {
	if maxBlocks <= 0 {
		return nil
	}
	matches := queryCodeBlockRe.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return nil
	}
	if maxLines <= 0 {
		maxLines = 120
	}
	out := make([]string, 0, min(len(matches), maxBlocks))
	for _, m := range matches {
		if len(out) >= maxBlocks {
			break
		}
		lang := strings.TrimSpace(safeSub(m, 1))
		if lang == "" {
			lang = "text"
		}
		raw := strings.ReplaceAll(safeSub(m, 2), "\r\n", "\n")
		lines := strings.Split(raw, "\n")
		if len(lines) > maxLines {
			lines = append(lines[:maxLines], "... [query code truncated]")
		}
		snippet := strings.TrimSpace(strings.Join(lines, "\n"))
		if snippet == "" {
			continue
		}
		out = append(out, fmt.Sprintf("```%s\n%s\n```", lang, snippet))
	}
	return out
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

func ResolvePromptProfile(query, task string, runtime PromptRuntime) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if containsAnyFold(q, []string{"detailed", "deep", "thorough", "exhaustive", "in-depth"}) {
		return "deep"
	}
	if containsAnyFold(q, []string{"compact", "short", "minimal", "brief", "concise", "summary"}) {
		return "compact"
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review", "planning":
		if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 {
			return "compact"
		}
		return "deep"
	}
	if runtime.LowLatency {
		return "compact"
	}
	if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 {
		return "compact"
	}
	return "compact"
}

func ResolvePromptRole(query, task string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if containsAnyFold(q, []string{"architect", "architecture", "system design"}) {
		return "architect"
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		return "security_auditor"
	case "review":
		return "code_reviewer"
	case "planning":
		return "planner"
	case "debug":
		return "debugger"
	case "refactor":
		return "refactorer"
	case "test":
		return "test_engineer"
	case "doc":
		return "documenter"
	default:
		return "generalist"
	}
}

type PromptRenderBudget struct {
	ContextFiles       int
	ToolList           int
	InjectedBlocks     int
	InjectedLines      int
	InjectedTokens     int
	ProjectBriefTokens int
}

func ResolvePromptRenderBudget(task, profile string, runtime PromptRuntime) PromptRenderBudget {
	b := PromptRenderBudget{
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
	if runtime.LowLatency {
		b.ContextFiles = max(4, int(float64(b.ContextFiles)*0.72))
		b.ToolList = max(8, int(float64(b.ToolList)*0.72))
		b.InjectedBlocks = max(1, b.InjectedBlocks-1)
		b.InjectedLines = max(28, int(float64(b.InjectedLines)*0.65))
		b.InjectedTokens = max(120, int(float64(b.InjectedTokens)*0.65))
		b.ProjectBriefTokens = max(90, int(float64(b.ProjectBriefTokens)*0.68))
	}
	if runtime.MaxContext > 0 {
		scale := float64(runtime.MaxContext) / 128000.0
		if scale > 1.0 {
			scale = 1.0
		}
		if scale < 0.22 {
			scale = 0.22
		}
		b.ContextFiles = max(3, int(float64(b.ContextFiles)*scale))
		b.ToolList = max(6, int(float64(b.ToolList)*scale))
		b.InjectedLines = max(24, int(float64(b.InjectedLines)*scale))
		b.InjectedTokens = max(100, int(float64(b.InjectedTokens)*scale))
		b.ProjectBriefTokens = max(80, int(float64(b.ProjectBriefTokens)*scale))
	}
	return b
}

func BuildInjectedContext(projectRoot, query, task, profile string, runtime PromptRuntime) string {
	resolvedProfile := strings.TrimSpace(profile)
	if resolvedProfile == "" {
		resolvedProfile = ResolvePromptProfile(query, task, runtime)
	}
	limits := ResolvePromptRenderBudget(task, resolvedProfile, runtime)
	return BuildInjectedContextWithBudget(projectRoot, query, limits)
}

func BuildInjectedContextWithBudget(projectRoot, query string, limits PromptRenderBudget) string {
	injected := extractInjectedContext(projectRoot, query, limits.InjectedBlocks, limits.InjectedLines)
	if limits.InjectedTokens > 0 {
		injected = trimToTokenBudget(injected, limits.InjectedTokens)
	}
	return injected
}

func PromptTokenBudget(task, profile string, runtime PromptRuntime) int {
	// Base contract (honesty, failure modes, output format, refusals) runs
	// ~450 stable tokens before any dynamic section. Budgets below bake that
	// in and leave meaningful differential room for injected context.
	budget := 1100
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		budget = 1800
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review":
		budget += 260
	case "planning":
		budget += 160
	case "doc":
		budget -= 60
	}
	if runtime.LowLatency {
		budget = int(float64(budget) * 0.85)
	}
	if runtime.MaxContext > 0 {
		cap := runtime.MaxContext / 4
		if cap > 3400 {
			cap = 3400
		}
		if cap < 720 {
			cap = 720
		}
		if budget > cap {
			budget = cap
		}
	}
	if budget < 720 {
		budget = 720
	}
	return budget
}

func TrimPromptToBudget(prompt string, maxTokens int) string {
	return trimToTokenBudget(prompt, maxTokens)
}

func containsAnyFold(in string, terms []string) bool {
	for _, t := range terms {
		v := strings.TrimSpace(strings.ToLower(t))
		if v == "" {
			continue
		}
		if strings.Contains(in, v) {
			return true
		}
	}
	return false
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

func BuildToolCallPolicy(task string, runtime PromptRuntime) string {
	lines := []string{
		"Discipline: call tools only when they reduce uncertainty; keep calls narrow; reuse prior outputs; validate edits with the smallest test.",
		"Prefer dedicated tools over run_command: read_file (not cat), grep_codebase (not grep/rg), glob (not find), edit_file/apply_patch (not sed/awk), write_file (not echo>), web_fetch (not curl), ast_query for outlines. Use run_command only for build/test/lint/git/deps.",
		"Parallelism: independent calls → one tool_batch_call; dependent commands → chain with && in a single run_command; never split a command across newlines; don't retry failing calls without changing inputs.",
		"Mutation safety: read_file before edit_file/write_file (engine rejects blind mutations); multi-hunk edits → apply_patch (use dry_run when non-trivial); validate with targeted test/vet/tsc before declaring done.",
		"Git/shell safety: never --no-verify or --no-gpg-sign without user consent; never force-push main or reset --hard without authorization; stage files by name (not add -A/.); after a pre-commit hook fails, fix and create a NEW commit (never --amend); HEREDOC for multi-line commit messages.",
	}
	switch strings.TrimSpace(strings.ToLower(runtime.ToolStyle)) {
	case "function-calling":
		lines = append(lines, "Protocol: strict function-call JSON matching schema; no prose inside arguments.")
	case "tool_use":
		lines = append(lines, "Protocol: tool_use blocks with strict JSON input; pair each tool_result with its tool_use id.")
	case "none":
		lines = append(lines, "Protocol: no native tool-calling; rely on provided context and direct reasoning.")
	default:
		lines = append(lines, "Protocol: follow provider-native tool format exactly; schema fidelity over verbosity.")
	}
	if runtime.MaxContext > 0 {
		toolOutputBudget := runtime.MaxContext / 5
		toolOutputBudget = max(96, toolOutputBudget)
		lines = append(lines, "Keep cumulative tool output near "+strconv.Itoa(toolOutputBudget)+" tokens unless risk requires deeper evidence.")
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		lines = append(lines, "Security overlay: collect concrete evidence before remediation edits; report exploitability conditions and confidence.")
	case "review":
		lines = append(lines, "Review overlay: anchor findings to file:line evidence; prioritize high-severity and high-confidence issues.")
	}
	return strings.Join(lines, "\n")
}

func BuildResponsePolicy(task, profile string) string {
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
	trimmed := ""
	tokenCount := 0
	for budget > 0 {
		trimmed = trimToTokenBudget(chunk.Content, budget)
		if strings.TrimSpace(trimmed) == "" {
			return types.ContextChunk{}
		}
		tokenCount = estimateTokens(trimmed)
		if tokenCount <= budget {
			break
		}
		over := tokenCount - budget
		over = max(1, over)
		budget -= over
	}
	if strings.TrimSpace(trimmed) == "" || budget <= 0 {
		return types.ContextChunk{}
	}
	chunk.Content = trimmed
	chunk.TokenCount = tokenCount
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
		return trimToTokenBudget(sig, maxTokens), 1, min(160, len(strings.Split(raw, "\n")))
	default:
		snippet, lineStart, lineEnd := extractSnippet(raw, terms, 60)
		return trimToTokenBudget(stripComments(snippet), maxTokens), lineStart, lineEnd
	}
}

func trimToTokenBudget(content string, maxTokens int) string {
	return tokens.TrimToBudget(content, maxTokens, "... [truncated for token budget]")
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
