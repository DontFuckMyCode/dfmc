package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// Invalidate forwards a file-modified signal to the codemap so the next
// BuildWithOptions call sees fresh symbol data. No-op on nil receiver or
// when the codemap isn't wired.
func (m *Manager) Invalidate(path string) {
	if m == nil || path == "" || m.codemap == nil {
		return
	}
	m.codemap.InvalidateFile(path)
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

	// Apply task-type differentiation to retrieval strategy.
	switch opts.Strategy {
	case StrategySecurity:
		opts.GraphDepth = max(opts.GraphDepth, 3)
	case StrategyDebug:
		opts.GraphDepth = max(opts.GraphDepth, 2)
	case StrategyReview:
		opts.GraphDepth = min(opts.GraphDepth, 1)
	case StrategyRefactor:
		opts.GraphDepth = max(opts.GraphDepth, 2)
	}

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
			dynamicTokens += tokens.Estimate(s.Text)
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
			if tok := tokens.Estimate(text); tok > stableRemaining {
				text = trimToTokenBudget(text, stableRemaining)
			}
			stableRemaining -= tokens.Estimate(text)
			// Any unused stable budget rolls forward to dynamic — the
			// reverse (dynamic → stable) is deliberately disallowed to keep
			// cache prefixes stable across turns.
			if stableRemaining < 0 {
				stableRemaining = 0
			}
		} else {
			allowance := dynamicRemaining + stableRemaining
			if tok := tokens.Estimate(text); tok > allowance {
				text = trimToTokenBudget(text, allowance)
			}
			used := tokens.Estimate(text)
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



