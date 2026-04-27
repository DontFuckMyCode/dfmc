package ast

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
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
	reFunc := regexp.MustCompile(`^\s*func\s+([A-Za-z_]\w*)\s*\(`)
	reMethod := regexp.MustCompile(`^\s*func\s*\([^)]*\)\s*([A-Za-z_]\w*)\s*\(`)
	reInterfaceType := regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)\s+interface\b`)
	reType := regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)\s+`)
	reConst := regexp.MustCompile(`^\s*const\s+([A-Za-z_][\w\s,]*)\b`)
	reVar := regexp.MustCompile(`^\s*var\s+([A-Za-z_][\w\s,]*)\b`)

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

		if m := reMethod.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolMethod, m[1], i+1, trimmed)
			continue
		}
		if m := reFunc.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolFunction, m[1], i+1, trimmed)
			continue
		}
		if m := reInterfaceType.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolInterface, m[1], i+1, trimmed)
			continue
		}
		if m := reType.FindStringSubmatch(line); len(m) > 1 {
			add(types.SymbolType, m[1], i+1, trimmed)
			continue
		}
		if m := reConst.FindStringSubmatch(line); len(m) > 1 {
			for _, name := range splitIdentifierList(m[1]) {
				add(types.SymbolConstant, name, i+1, trimmed)
			}
			continue
		}
		if m := reVar.FindStringSubmatch(line); len(m) > 1 {
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
	reQuoted := regexp.MustCompile(`"([^"]+)"|` + "`" + `([^` + "`" + `]+)` + "`")
	inBlock := false

	addMatches := func(line string) {
		for _, match := range reQuoted.FindAllStringSubmatch(line, -1) {
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
	reNames := regexp.MustCompile(`^\s*([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\b`)
	match := reNames.FindStringSubmatch(line)
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
