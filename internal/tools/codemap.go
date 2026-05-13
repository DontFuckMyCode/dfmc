// codemap_tool.go — Layer 4 of the context-gathering stack:
// project-level overview surface for the model.
//
// The model uses this to answer "what's in this project?" before it
// dives into specific files. The output is signatures-only — no
// function bodies — so an entire mid-sized repo fits in a single tool
// result. Run once at session start (or when the model is lost),
// never repeatedly: the data is stable across most edits.
//
// Pipeline:
//   1. Walk the project tree, applying the same exclude list as
//      grep_codebase + a respect for `.gitignore` directories so
//      vendored mirrors and generated artifacts stay out of the map.
//   2. For each source file, run ast.Engine.ParseFile (cached) to
//      pull symbols + their signatures.
//   3. Group by file, sort symbols by line, render markdown grouped
//      by directory. The format is what the spec calls for:
//        pkg/auth/service.go (Go)
//          type UserService struct                    L12
//          func NewUserService(db *sql.DB) *UserService  L24
//          func (s *UserService) Authenticate(...)    L31
//   4. Stats footer (files scanned, symbols, parse errors, languages,
//      duration_ms) so the model can sanity-check coverage.
//
// Bodies-only is INTENTIONAL: include_bodies=true would mean fetching
// full source per file (megabytes for a real repo). When the model
// wants a body it should call get_symbol / find_symbol — that's the
// next layer down.
//
// No caching here yet — codemap.Engine has its own metrics; a future
// enhancement can add an mtime-keyed result cache at the tool layer.

package tools

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// CodemapTool exposes the project-overview surface to the agent loop.
// Holds a lazy ast.Engine so repeat calls within one session reuse the
// parse cache; first call pays the cost, subsequent calls are cheap.
type CodemapTool struct {
	engine *ast.Engine
}

func NewCodemapTool() *CodemapTool { return &CodemapTool{engine: ast.New()} }

func (t *CodemapTool) Name() string { return "codemap" }
func (t *CodemapTool) Description() string {
	return "High-level project overview — every file, every symbol, every signature, no bodies."
}

func (t *CodemapTool) Close() error {
	if t == nil || t.engine == nil {
		return nil
	}
	return t.engine.Close()
}

func (t *CodemapTool) getEngine() *ast.Engine { return t.engine }

func (t *CodemapTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "codemap",
		Title:   "Project codemap",
		Summary: "Signatures-only project overview, grouped by file. Use ONCE per session to orient.",
		Purpose: "The Layer 4 entry point: answers 'what's in this project, where?' without spending tokens on function bodies. Pair with find_symbol / read_file when you want to dive in.",
		Prompt: `Project overview generator. Layer 4 (orientation) of the read stack — sits above ` + "`find_symbol`" + ` (semantic locate) and ` + "`read_file`" + ` (raw fetch). Layer 1 = grep_codebase (cheapest discovery).

Order of cost: ` + "`grep_codebase`" + ` < ` + "`codemap`" + ` < ` + "`find_symbol`" + ` < ` + "`read_file`" + `. Pick the cheapest tool that answers the question.

Output format (markdown):

  pkg/auth/service.go (Go)
    type UserService struct                                    L12
    func NewUserService(db *sql.DB) *UserService               L24
    func (s *UserService) Authenticate(user, pass string) error  L31

When to use:
- First call in a fresh session — gives you a map of names + locations.
- After the user mentions an unfamiliar area ("look at the auth code") and you don't know where it lives.
- When you've gotten lost and need re-orientation.

When NOT to use:
- You already know the file/symbol — call find_symbol or read_file directly.
- Repeatedly within the same session — the output is stable; rerunning wastes tokens.
- For function bodies — codemap is signatures-only by design. Bodies live behind find_symbol.

Args:
- path (optional): scope to a subdirectory (e.g. "internal/engine"). Default: project root.
- max_depth (optional, default 12): cap directory walk depth.
- max_files (optional, default 2000, ceiling 5000): hard cap on files included; truncated:true if hit.
- languages (optional): filter ["go","python",...] — empty means all detected languages.

Excludes mirror grep_codebase: .git, .dfmc, node_modules, vendor, bin, dist, build, .next, target.`,
		Risk: RiskRead,
		Tags: []string{"overview", "read", "ast", "navigation", "orientation"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: "Subdirectory to scope the map (default: project root)."},
			{Name: "max_depth", Type: ArgInteger, Default: 12, Description: "Maximum directory walk depth."},
			{Name: "max_files", Type: ArgInteger, Default: 2000, Description: "Cap on files included (ceiling 5000)."},
			{Name: "languages", Type: ArgArray, Items: &Arg{Type: ArgString}, Description: `Filter to languages (e.g. ["go","python"]).`},
		},
		Returns: "Markdown overview grouped by file with stats footer (files, symbols, languages, duration_ms).",
		Examples: []string{
			`{}`,
			`{"path":"internal/engine","max_files":500}`,
			`{"languages":["go"]}`,
		},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

// codemapFileEntry carries the per-file projection used during render.
// Populated during the walk, then sorted + grouped at the end.
type codemapFileEntry struct {
	path     string // forward-slash project-relative
	language string
	symbols  []types.Symbol
}

func (t *CodemapTool) Execute(ctx context.Context, req Request) (Result, error) {
	root := strings.TrimSpace(asString(req.Params, "path", ""))
	if root == "" {
		root = req.ProjectRoot
	} else {
		abs, err := EnsureWithinRoot(req.ProjectRoot, root)
		if err != nil {
			return Result{}, err
		}
		root = abs
	}

	maxDepth := asInt(req.Params, "max_depth", 12)
	if maxDepth <= 0 {
		maxDepth = 12
	}
	maxFiles := asInt(req.Params, "max_files", 2000)
	if maxFiles <= 0 {
		maxFiles = 2000
	}
	if maxFiles > 5000 {
		maxFiles = 5000
	}
	wantedLangs := codemapWantedLanguages(req.Params)

	startedAt := time.Now()
	rootDepth := strings.Count(strings.TrimRight(filepath.ToSlash(root), "/"), "/")

	var (
		entries      []codemapFileEntry
		filesSeen    int
		parseErrors  int
		truncated    bool
		skippedFiles int
		langCounts   = map[string]int{}
	)

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if dropDirsForCodemap[d.Name()] {
				return fs.SkipDir
			}
			depth := strings.Count(strings.TrimRight(filepath.ToSlash(path), "/"), "/") - rootDepth
			if depth > maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !codemapInterestingFile(d.Name()) {
			return nil
		}
		safePath, serr := EnsureWithinRoot(req.ProjectRoot, path)
		if serr != nil {
			skippedFiles++
			return nil
		}
		if filesSeen >= maxFiles {
			truncated = true
			return fs.SkipAll
		}
		filesSeen++

		parseRes, perr := t.getEngine().ParseFile(ctx, safePath)
		if perr != nil || parseRes == nil {
			parseErrors++
			return nil
		}
		if len(wantedLangs) > 0 && !wantedLangs[strings.ToLower(parseRes.Language)] {
			return nil
		}
		if len(parseRes.Symbols) == 0 {
			return nil
		}
		rel, err := filepath.Rel(req.ProjectRoot, safePath)
		if err != nil {
			rel = safePath
		}
		entries = append(entries, codemapFileEntry{
			path:     filepath.ToSlash(rel),
			language: parseRes.Language,
			symbols:  parseRes.Symbols,
		})
		if parseRes.Language != "" {
			langCounts[parseRes.Language]++
		}
		return nil
	})
	if walkErr != nil && walkErr != fs.SkipAll {
		return Result{}, walkErr
	}

	// Sort entries by path so output is deterministic across runs.
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	totalSymbols := 0
	output := codemapRenderMarkdown(entries, &totalSymbols)
	stats := codemapStatsLine(filesSeen, totalSymbols, parseErrors, skippedFiles, langCounts, time.Since(startedAt), truncated)
	output = stats + "\n\n" + output

	return Result{
		Output: output,
		Data: map[string]any{
			"root":            filepath.ToSlash(root),
			"files":           filesSeen,
			"files_with_syms": len(entries),
			"symbols":         totalSymbols,
			"parse_errors":    parseErrors,
			"skipped_files":   skippedFiles,
			"languages":       langCounts,
			"truncated":       truncated,
			"duration_ms":     time.Since(startedAt).Milliseconds(),
		},
		Truncated: truncated,
	}, nil
}

