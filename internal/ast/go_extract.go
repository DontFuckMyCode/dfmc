package ast

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// Go-specific extractor regexes hoisted to package scope. Pre-fix these
// were rebuilt on every extractGoSymbols / extractGoImports / splitIdentifierList
// call — for !cgo builds (or any tree-sitter parse fallback path) that's
// 8 regexp.Compile invocations per Go file. The JS/Python/Rust symbol
// regexes were already hoisted in engine.go; the Go set lagged behind.
var (
	reGoFunc          = regexp.MustCompile(`^\s*func\s+([A-Za-z_]\w*)\s*\(`)
	reGoMethod        = regexp.MustCompile(`^\s*func\s*\([^)]*\)\s*([A-Za-z_]\w*)\s*\(`)
	reGoInterfaceType = regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)\s+interface\b`)
	reGoType          = regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)\s+`)
	reGoConst         = regexp.MustCompile(`^\s*const\s+([A-Za-z_][\w\s,]*)\b`)
	reGoVar           = regexp.MustCompile(`^\s*var\s+([A-Za-z_][\w\s,]*)\b`)
	// reGoQuotedImport matches double-quoted "path" or backtick-quoted `path`
	// import strings inside a Go source file.
	reGoQuotedImport = regexp.MustCompile(`"([^"]+)"|` + "`" + `([^` + "`" + `]+)` + "`")
	// reGoIdentList captures a leading comma-separated identifier list,
	// used for `const X, Y, Z = ...` and `var A, B = ...`.
	reGoIdentList = regexp.MustCompile(`^\s*([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\b`)
)

func extractWithPreferredBackend(ctx context.Context, path, lang string, content []byte) ([]types.Symbol, []string, []ParseError, string, error) {
	if symbols, imports, errs, handled, err := parseWithTreeSitter(ctx, path, lang, content); handled && err == nil {
		return symbols, imports, errs, "tree-sitter", nil
	} else if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, nil, "", err
		}
	}
	return extractSymbols(path, lang, content), extractImports(lang, content), nil, "regex", nil
}

func extractGoSymbols(path, lang string, content []byte) []types.Symbol {
	lines := strings.Split(string(content), "\n")

	var (
		symbols      []types.Symbol
		inConstBlock bool
		inVarBlock   bool
	)

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

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "const (":
			inConstBlock = true
			inVarBlock = false
			continue
		case "var (":
			inVarBlock = true
			inConstBlock = false
			continue
		case ")":
			if inConstBlock || inVarBlock {
				inConstBlock = false
				inVarBlock = false
				continue
			}
		}

		if m := reGoMethod.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolMethod, m[1], i+1, trimmed)
			continue
		}
		if m := reGoFunc.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolFunction, m[1], i+1, trimmed)
			continue
		}
		if m := reGoInterfaceType.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolInterface, m[1], i+1, trimmed)
			continue
		}
		if m := reGoType.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolType, m[1], i+1, trimmed)
			continue
		}
		if m := reGoConst.FindStringSubmatch(line); len(m) > 1 {
			for _, name := range splitIdentifierList(m[1]) {
				add(types.SymbolConstant, name, i+1, trimmed)
			}
			continue
		}
		if m := reGoVar.FindStringSubmatch(line); len(m) > 1 {
			for _, name := range splitIdentifierList(m[1]) {
				add(types.SymbolVariable, name, i+1, trimmed)
			}
			continue
		}
		if inConstBlock {
			for _, name := range splitIdentifierList(line) {
				add(types.SymbolConstant, name, i+1, trimmed)
			}
			continue
		}
		if inVarBlock {
			for _, name := range splitIdentifierList(line) {
				add(types.SymbolVariable, name, i+1, trimmed)
			}
		}
	}

	return symbols
}

func extractGoImports(content []byte) []string {
	lines := strings.Split(string(content), "\n")
	set := map[string]struct{}{}
	inBlock := false

	addMatches := func(line string) {
		for _, match := range reGoQuotedImport.FindAllStringSubmatch(line, -1) {
			for i := 1; i < len(match); i++ {
				v := strings.TrimSpace(match[i])
				if v != "" {
					set[v] = struct{}{}
				}
			}
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "import (":
			inBlock = true
		case inBlock && trimmed == ")":
			inBlock = false
		case inBlock:
			addMatches(line)
		case strings.HasPrefix(trimmed, "import "):
			addMatches(line)
		}
	}

	imports := make([]string, 0, len(set))
	for item := range set {
		imports = append(imports, item)
	}
	return imports
}

func splitIdentifierList(line string) []string {
	match := reGoIdentList.FindStringSubmatch(line)
	if len(match) < 2 {
		return nil
	}
	parts := strings.Split(match[1], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
