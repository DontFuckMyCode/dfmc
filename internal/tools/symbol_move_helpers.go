package tools

// symbol_move_helpers.go — package extraction, new-file scaffolding,
// imports section preservation, and symbol-location lookup used by the
// SymbolMoveTool. Sibling to symbol_move.go which keeps the tool surface
// + Spec + Execute pipeline. applyRenameInLine lives in symbol_rename.go.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

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
	defer func() { _ = astEngine.Close() }()

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
			filePath:  fpath,
			startLine: sym.Line,
			endLine:   endLine,
			signature: sym.Signature,
			kind:      string(sym.Kind),
			declLine:  first.fullLine,
		}, nil
	}
	return nil, fmt.Errorf("symbol %q not found", name)
}
