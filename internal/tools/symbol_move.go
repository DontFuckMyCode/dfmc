// symbol_move.go — Phase 7 tool for moving a symbol between files.
// Moves a function/type/variable declaration to a destination file and
// updates all references across the project. dry_run by default.
//
// Pipeline:
// 1. Parse source file to locate the symbol and its full scope (brace-balanced)
// 2. Read destination file — if absent, create with matching package declaration
// 3. Append the symbol body (with optional rename) to destination
// 4. Remove/mark declaration in source file
// 5. Update all references to the symbol in other project files
//
// SECURITY HARDENING (closes VULN-006):
//
//   - `to_file` is routed through EnsureWithinRoot before constructing
//     destPath. Earlier versions used `filepath.Join(projectRoot,
//     toFile)` which silently allowed `to_file="../../tmp/pwned.go"`
//     to write attacker-chosen Go source anywhere the dfmc process
//     could reach (graduates to RCE via dropping into ~/.dfmc/hooks).
//   - When the destination already exists, EnsureReadBeforeMutation
//     fires before the overwrite — same gate as edit_file/apply_patch.
//     Missing destinations (creating a new file) are exempt because
//     there's nothing to read first.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type SymbolMoveTool struct {
	engine *Engine
}

func NewSymbolMoveTool() *SymbolMoveTool { return &SymbolMoveTool{} }
func (t *SymbolMoveTool) Name() string   { return "symbol_move" }

// SetEngine wires the tools.Engine reference so the destination
// overwrite path can call EnsureReadBeforeMutation. nil is safe —
// the read-gate check short-circuits to "no engine, no gate" so
// tests that build the tool standalone keep working.
func (t *SymbolMoveTool) SetEngine(e *Engine) { t.engine = e }
func (t *SymbolMoveTool) Description() string {
	return "Move a function/type/variable declaration to another file and update all references."
}

func (t *SymbolMoveTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "symbol_move",
		Title:   "Move symbol",
		Summary: "Move a function/type/variable to another file and update all project references.",
		Purpose: `Use when you've decided to reorganize code across files. Like symbol_rename but moves the declaration to a new file while updating all references. Shows impact before mutating.`,
		Risk:     RiskWrite,
		Tags:     []string{"rename", "refactor", "move", "symbol", "ast", "bulk-edit"},
		Args: []Arg{
			{Name: "from", Type: ArgString, Required: true, Description: `Symbol name to move.`},
			{Name: "to_file", Type: ArgString, Required: true, Description: `Destination file path (relative to project root).`},
			{Name: "to", Type: ArgString, Description: `New symbol name in destination (default: same as from).`},
			{Name: "kind", Type: ArgString, Description: `func | type | var | const | method | all. Default: all.`},
			{Name: "dry_run", Type: ArgBoolean, Default: true, Description: `Preview without writing. Default true.`},
			{Name: "skip_tests", Type: ArgBoolean, Default: false, Description: `Skip test files when updating references. Default false.`},
		},
		Returns:        "Structured JSON: {impact: {files, locations}, changes: [{path, old, new, line}], dry_run: bool}",
		Idempotent:     false,
		CostHint:       "cpu-bound",
	}
}

type moveImpact struct {
	Files     int `json:"files"`
	Locations int `json:"locations"`
	Moved     int `json:"moved"`
	Updated   int `json:"updated"`
	Skipped   int `json:"skipped"`
}

type moveChange struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	Line int    `json:"line"`
}

// symbolLocation holds the precise location of a symbol in a file.
type symbolLocation struct {
	filePath   string
	startLine  int // 1-based
	endLine    int // 1-based (brace-balanced scope end)
	signature  string
	kind       string
	declLine   string // the full declaration line
}

// Execute finds the symbol in the project and moves it to to_file.
func (t *SymbolMoveTool) Execute(ctx context.Context, req Request) (Result, error) {
	from := strings.TrimSpace(asString(req.Params, "from", ""))
	toFile := strings.TrimSpace(asString(req.Params, "to_file", ""))
	toName := strings.TrimSpace(asString(req.Params, "to", ""))
	kind := strings.TrimSpace(asString(req.Params, "kind", "all"))
	dryRun := asBool(req.Params, "dry_run", true)
	skipTests := asBool(req.Params, "skip_tests", false)

	if from == "" {
		return Result{}, missingParamError("symbol_move", "from", req.Params,
			`{"from":"Foo","to_file":"bar.go"}`,
			`from is required — the current symbol name to locate and move.`)
	}
	if toFile == "" {
		return Result{}, missingParamError("symbol_move", "to_file", req.Params,
			`{"from":"Foo","to_file":"bar.go"}`,
			`to_file is required — the destination file path.`)
	}
	if toName == "" {
		toName = from
	}

	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "."
	}

	// Locate the symbol across the project.
	loc, err := findSymbolLocation(projectRoot, from, kind)
	if err != nil {
		return Result{}, fmt.Errorf("symbol_move: %v", err)
	}
	if loc == nil {
		return Result{
			Output: fmt.Sprintf("symbol_move: no symbol %q found in project", from),
			Data: map[string]any{
				"from":   from,
				"to":     toName,
				"impact": moveImpact{},
			},
		}, nil
	}

	// Resolve destination path. EnsureWithinRoot rejects `to_file`
	// values that escape the project (`../../etc/passwd`,
	// `/tmp/pwned.go`, anything resolving outside projectRoot via
	// symlinks). Earlier versions used a naive `filepath.Join` here
	// which silently allowed traversal — the VULN-006 primitive.
	destPath, err := EnsureWithinRoot(projectRoot, toFile)
	if err != nil {
		return Result{}, fmt.Errorf("symbol_move: to_file outside project root: %w", err)
	}
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return Result{}, fmt.Errorf("symbol_move: cannot create destination directory: %w", err)
	}

	// Read source file content for editing.
	srcContent, err := os.ReadFile(loc.filePath)
	if err != nil {
		return Result{}, fmt.Errorf("symbol_move: read source file: %w", err)
	}
	srcLines := strings.Split(string(srcContent), "\n")

	// Extract the symbol body (startLine-1 to endLine-1, 0-indexed).
	if loc.startLine < 1 {
		loc.startLine = 1
	}
	if loc.endLine < loc.startLine {
		loc.endLine = loc.startLine
	}
	symbolBody := strings.Join(srcLines[loc.startLine-1:loc.endLine], "\n")

	// Apply rename to the body if toName differs from from.
	if toName != from {
		symbolBody = applyRenameInLine(symbolBody, from, toName)
	}

	// Read existing destination content or build new file.
	destExists := false
	var destLines []string
	if existing, err := os.ReadFile(destPath); err == nil {
		destExists = true
		destLines = strings.Split(string(existing), "\n")
	} else {
		// Build skeleton for new file using source file's package.
		srcPkg := extractPackage(srcLines)
		comment := fmt.Sprintf("// %s — moved from %s\n", filepath.Base(toFile), filepath.Base(loc.filePath))
		header := comment + "package " + srcPkg + "\n\n"
		destLines = strings.Split(header, "\n")
		if destLines[len(destLines)-1] == "" {
			destLines = destLines[:len(destLines)-1]
		}
	}

	// Check if toName already exists in destination.
	for _, line := range destLines {
		if strings.Contains(line, "func "+toName) || strings.Contains(line, "type "+toName) ||
			strings.Contains(line, "var "+toName) || strings.Contains(line, "const "+toName) {
			return Result{}, fmt.Errorf("symbol_move: %q already exists in destination file %q", toName, toFile)
		}
	}

	// --- Phase 2: collect all references to the symbol (excluding source file) ---
	allFiles := collectGoFiles(projectRoot, skipTests)
	var changes []moveChange
	totalUpdated := 0
	totalFiles := 0

	for _, fpath := range allFiles {
		if fpath == loc.filePath && !destExists {
			// Source file will be modified (original declaration removed)
			continue
		}
		if skipTests && isTestFile(fpath) {
			continue
		}
		matches := findRenameMatches(fpath, from, kind)
		if len(matches) == 0 {
			continue
		}
		totalFiles++
		for _, m := range matches {
			if dryRun {
				changes = append(changes, moveChange{
					Path: fpath,
					Old:  m.fullLine,
					New:  applyRenameInLine(m.fullLine, from, toName),
					Line: m.lineNum,
				})
			} else {
				// Actually update the reference.
				fc, err := os.ReadFile(fpath)
				if err != nil {
					continue
				}
				lines := strings.Split(string(fc), "\n")
				if m.lineNum >= 1 && m.lineNum <= len(lines) {
					lines[m.lineNum-1] = applyRenameInLine(lines[m.lineNum-1], from, toName)
				}
				if err := os.WriteFile(fpath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
					_ = err
				}
				changes = append(changes, moveChange{
					Path: fpath,
					Old:  m.fullLine,
					New:  applyRenameInLine(m.fullLine, from, toName),
					Line: m.lineNum,
				})
			}
			totalUpdated++
		}
	}

	// --- Apply file mutations (only when dry_run=false) ---
	if !dryRun {
		// Remove declaration from source.
		newSrcLines := append([]string{}, srcLines[:loc.startLine-1]...)
		newSrcLines = append(newSrcLines, srcLines[loc.endLine:]...)
		if err := os.WriteFile(loc.filePath, []byte(strings.Join(newSrcLines, "\n")), 0644); err != nil {
			return Result{}, fmt.Errorf("symbol_move: write source file: %w", err)
		}

		// Append symbol body to destination. Existing files go
		// through the same read-before-mutation gate as
		// edit_file/apply_patch — overwriting a file the model never
		// read silently destroys whatever was there.
		if destExists {
			if t.engine != nil {
				if guardErr := t.engine.EnsureReadBeforeMutation(destPath); guardErr != nil {
					return Result{}, fmt.Errorf("symbol_move: %w", guardErr)
				}
			}
			destContent, _ := os.ReadFile(destPath)
			newDest := string(destContent)
			if !strings.HasSuffix(newDest, "\n") {
				newDest += "\n"
			}
			newDest += symbolBody + "\n"
			if err := os.WriteFile(destPath, []byte(newDest), 0644); err != nil {
				return Result{}, fmt.Errorf("symbol_move: write destination file: %w", err)
			}
		} else {
			// For new file, build properly.
			newFileContent := buildNewGoFile(srcLines, symbolBody)
			if err := os.WriteFile(destPath, []byte(newFileContent), 0644); err != nil {
				return Result{}, fmt.Errorf("symbol_move: write new destination file: %w", err)
			}
		}
	}

	action := "Would move"
	if !dryRun {
		action = "Moved"
	}
	totalLocations := totalUpdated + 1 // +1 for the declaration itself
	return Result{
		Output: fmt.Sprintf("%s %q (%d locations in %d files, declaration in %s → %s)",
			action, from, totalLocations, totalFiles+1, loc.filePath, toFile),
		Data: map[string]any{
			"from":   from,
			"to":     toName,
			"source": loc.filePath,
			"dest":   toFile,
			"impact": moveImpact{
				Files:     totalFiles + 1,
				Locations: totalLocations,
				Moved:     1,
				Updated:   totalUpdated,
				Skipped:   0,
			},
			"dry_run": dryRun,
			"changes": changes,
		},
	}, nil
}

// --- helpers ---

// extractPackage returns the package name from a set of Go source lines.
func extractPackage(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return strings.TrimPrefix(line, "package ")
		}
	}
	return "main"
}

// buildNewGoFile constructs a Go file with the correct package and imports,
// then appends the moved symbol body.
func buildNewGoFile(srcLines []string, symbolBody string) string {
	pkg := extractPackage(srcLines)
	imports := extractImportsSection(srcLines)
	result := "package " + pkg + "\n"
	if imports != "" {
		result += imports + "\n"
	}
	result += "\n" + symbolBody + "\n"
	return result
}

// extractImportsSection builds an import block from existing source if it had one.
func extractImportsSection(lines []string) string {
	inImport := false
	var importLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import") {
			inImport = true
			importLines = append(importLines, line)
			continue
		}
		if inImport {
			if trimmed == ")" || strings.HasPrefix(trimmed, "package ") {
				break
			}
			importLines = append(importLines, line)
		}
	}
	if len(importLines) > 0 {
		return strings.Join(importLines, "\n")
	}
	return ""
}

// findSymbolLocation locates the first occurrence of a symbol by name and kind.
func findSymbolLocation(projectRoot, name, kind string) (*symbolLocation, error) {
	files := collectGoFiles(projectRoot, false)
	astEngine := ast.New()
	defer astEngine.Close()

	for _, fpath := range files {
		matches := findRenameMatches(fpath, name, kind)
		if len(matches) == 0 {
			continue
		}
		first := matches[0]

		// Parse the file to find the symbol's full scope.
		parsed, err := astEngine.ParseFile(context.Background(), fpath)
		if err != nil {
			continue
		}

		content, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")

		// Find the matching symbol in the parsed result.
		var sym types.Symbol
		for _, s := range parsed.Symbols {
			if s.Name == name {
				sym = s
				break
			}
		}

		if sym.Line == 0 {
			sym.Line = first.lineNum
		}

		endLine := extractScopeEnd(parsed.Language, lines, sym.Line)

		return &symbolLocation{
			filePath:   fpath,
			startLine:  sym.Line,
			endLine:    endLine,
			signature:  sym.Signature,
			kind:       string(sym.Kind),
			declLine:   first.fullLine,
		}, nil
	}
	return nil, fmt.Errorf("symbol %q not found", name)
}

// applyRenameInLine is borrowed from symbol_rename (word-boundary replace).
// It is defined in symbol_rename.go and callable directly within this package.
