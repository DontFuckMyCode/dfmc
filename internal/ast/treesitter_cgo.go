//go:build cgo

package ast

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// Per-language tree-sitter parser pools.
//
// INVARIANT: each language gets its OWN sync.Pool. Cross-language
// reuse is unsafe — a parser carries its grammar binding (Go grammar
// vs Python grammar etc.), and Put-ing a Go parser into a pool that a
// Python caller later Get-s would either parse with the wrong grammar
// (silent corruption) or panic in tree-sitter's ParseCtx. The map
// scope (`map[string]*sync.Pool` keyed by lang name) enforces this
// architecturally; do NOT collapse to a single shared pool.
//
// finalizeTreeSitterParser is the only legitimate Put path; if the
// parser failed mid-parse it must be Close()d instead of returned to
// the pool, otherwise a corrupt parser will infect the next caller
// of the same language.
var (
	treeSitterParserPoolsMu sync.RWMutex
	treeSitterParserPools   = map[string]*sync.Pool{}
)

func parseWithTreeSitter(ctx context.Context, path, lang string, content []byte) ([]types.Symbol, []string, []ParseError, bool, error) {
	language, handled, err := treeSitterLanguageFor(lang)
	if !handled || err != nil {
		return nil, nil, nil, handled, err
	}

	pool := treeSitterParserPool(lang)
	parser := pool.Get().(*tree_sitter.Parser)
	if parser == nil {
		return nil, nil, nil, true, fmt.Errorf("tree-sitter: pool returned nil parser for %s", lang)
	}
	// healthy gates the pool return — see finalizeTreeSitterParser.
	healthy := false
	defer func() { finalizeTreeSitterParser(pool, parser, healthy) }()

	if err := parser.SetLanguage(language); err != nil {
		return nil, nil, nil, true, fmt.Errorf("tree-sitter %s language: %w", lang, err)
	}
	healthy = true

	tree := parser.ParseCtx(ctx, content, nil)
	if tree == nil {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, true, err
		}
		return nil, nil, nil, false, nil
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil, nil, nil, true, nil
	}

	switch lang {
	case "go":
		return extractGoTreeSitter(path, lang, root, content), extractGoTreeSitterImports(root, content), collectTreeSitterParseErrors(root), true, nil
	case "javascript", "jsx", "typescript", "tsx":
		symbols, imports := extractJSTreeSitter(path, lang, root, content)
		return symbols, imports, collectTreeSitterParseErrors(root), true, nil
	case "python":
		symbols, imports := extractPythonTreeSitter(path, lang, root, content)
		return symbols, imports, collectTreeSitterParseErrors(root), true, nil
	default:
		return nil, nil, nil, false, nil
	}
}

// parserReturner is the slice of *sync.Pool we depend on for return.
// Defined as an interface so tests can substitute a recording mock —
// the production caller passes a *sync.Pool which satisfies it
// implicitly (sync.Pool.Put has signature func(any)).
type parserReturner interface {
	Put(any)
}

// finalizers holds a parser in a known-clean state before returning
// to the pool. It is the only legitimate Put path; if the parser failed
// mid-parse it must be Close()d instead, otherwise a corrupt parser
// will infect the next caller of the same language.
func finalizeTreeSitterParser(pool parserReturner, parser *tree_sitter.Parser, healthy bool) {
	if parser == nil {
		return
	}
	if healthy {
		// Reset clears language binding and parse state before pool return.
		// Without this, a Go-parser could corrupt a Python caller.
		parser.Reset()
		pool.Put(parser)
		return
	}
	parser.Close()
}

func treeSitterParserPool(lang string) *sync.Pool {
	treeSitterParserPoolsMu.RLock()
	if pool, ok := treeSitterParserPools[lang]; ok {
		treeSitterParserPoolsMu.RUnlock()
		return pool
	}
	treeSitterParserPoolsMu.RUnlock()

	treeSitterParserPoolsMu.Lock()
	defer treeSitterParserPoolsMu.Unlock()
	// Double-check after acquiring write lock
	if pool, ok := treeSitterParserPools[lang]; ok {
		return pool
	}

	pool := &sync.Pool{
		New: func() any {
			return tree_sitter.NewParser()
		},
	}
	treeSitterParserPools[lang] = pool
	return pool
}

func treeSitterLanguageFor(lang string) (*tree_sitter.Language, bool, error) {
	switch lang {
	case "go":
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), true, nil
	case "javascript", "jsx":
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language()), true, nil
	case "python":
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), true, nil
	case "typescript":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), true, nil
	case "tsx":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), true, nil
	default:
		return nil, false, nil
	}
}

func extractGoTreeSitter(path, lang string, root *tree_sitter.Node, content []byte) []types.Symbol {
	var symbols []types.Symbol
	walkTree(root, func(node *tree_sitter.Node) {
		switch node.Kind() {
		case "function_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				symbols = append(symbols, buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), types.SymbolFunction))
			}
		case "method_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				receiver := node.ChildByFieldName("receiver")
				meta := map[string]string{}
				if receiver != nil {
					meta["receiver"] = strings.TrimSpace(receiver.Utf8Text(content))
				}
				sym := buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), types.SymbolMethod)
				sym.Metadata = meta
				symbols = append(symbols, sym)
			}
		case "type_spec":
			name := node.ChildByFieldName("name")
			typeNode := node.ChildByFieldName("type")
			if name == nil {
				return
			}
			kind := types.SymbolType
			if typeNode != nil && typeNode.Kind() == "interface_type" {
				kind = types.SymbolInterface
			}
			symbols = append(symbols, buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), kind))
		case "var_spec":
			for _, name := range namedChildrenByField(node, "name") {
				symbols = append(symbols, buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), types.SymbolVariable))
			}
		case "const_spec":
			for _, name := range namedChildrenByField(node, "name") {
				symbols = append(symbols, buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), types.SymbolConstant))
			}
		}
	})

	return symbols
}

func extractGoTreeSitterImports(root *tree_sitter.Node, content []byte) []string {
	importsSet := map[string]struct{}{}
	walkTree(root, func(node *tree_sitter.Node) {
		if node.Kind() != "import_spec" {
			return
		}
		pathNode := node.ChildByFieldName("path")
		if pathNode == nil {
			return
		}
		value := trimQuotedTreeSitterText(pathNode.Utf8Text(content))
		if value != "" {
			importsSet[value] = struct{}{}
		}
	})
	return mapKeys(importsSet)
}

func extractJSTreeSitter(path, lang string, root *tree_sitter.Node, content []byte) ([]types.Symbol, []string) {
	importsSet := map[string]struct{}{}
	symbols := make([]types.Symbol, 0, 16)

	add := func(posNode, signatureNode *tree_sitter.Node, name string, kind types.SymbolKind) {
		if posNode == nil || signatureNode == nil {
			return
		}
		symbols = append(symbols, buildTreeSitterSymbolAt(path, lang, posNode, signatureNode, content, name, kind))
	}

	walkTree(root, func(node *tree_sitter.Node) {
		switch node.Kind() {
		case "function_declaration", "generator_function_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolFunction)
			}
		case "class_declaration", "abstract_class_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolClass)
			}
		case "interface_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolInterface)
			}
		case "type_alias_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolType)
			}
		case "enum_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolEnum)
			}
		case "lexical_declaration", "variable_declaration":
			for _, declarator := range namedChildrenOfKind(node, "variable_declarator") {
				kind := treeSitterJSDeclaratorKind(node, declarator)
				nameNode := declarator.ChildByFieldName("name")
				if nameNode == nil || nameNode.Kind() != "identifier" {
					continue
				}
				add(nameNode, declarator, nameNode.Utf8Text(content), kind)
			}
		case "import_statement":
			source := node.ChildByFieldName("source")
			if source != nil {
				if value := trimQuotedTreeSitterText(source.Utf8Text(content)); value != "" {
					importsSet[value] = struct{}{}
				}
			}
		case "call_expression":
			if value := treeSitterJSRequireImport(node, content); value != "" {
				importsSet[value] = struct{}{}
			}
		}
	})

	return symbols, mapKeys(importsSet)
}

func extractPythonTreeSitter(path, lang string, root *tree_sitter.Node, content []byte) ([]types.Symbol, []string) {
	importsSet := map[string]struct{}{}
	symbols := make([]types.Symbol, 0, 16)

	add := func(posNode, signatureNode *tree_sitter.Node, name string, kind types.SymbolKind) {
		if posNode == nil || signatureNode == nil {
			return
		}
		symbols = append(symbols, buildTreeSitterSymbolAt(path, lang, posNode, signatureNode, content, name, kind))
	}

	walkTree(root, func(node *tree_sitter.Node) {
		switch node.Kind() {
		case "class_definition":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolClass)
			}
		case "function_definition":
			if name := node.ChildByFieldName("name"); name != nil {
				add(name, node, name.Utf8Text(content), types.SymbolFunction)
			}
		case "import_statement", "future_import_statement":
			for _, child := range namedChildrenByField(node, "name") {
				if value := normalizePythonImportName(child.Utf8Text(content)); value != "" {
					importsSet[value] = struct{}{}
				}
			}
		case "import_from_statement":
			moduleName := node.ChildByFieldName("module_name")
			if moduleName != nil {
				if value := strings.TrimSpace(moduleName.Utf8Text(content)); value != "" {
					importsSet[value] = struct{}{}
				}
			}
		}
	})

	return symbols, mapKeys(importsSet)
}

func treeSitterJSDeclaratorKind(declNode, declarator *tree_sitter.Node) types.SymbolKind {
	value := declarator.ChildByFieldName("value")
	if value != nil {
		switch value.Kind() {
		case "arrow_function", "function_expression", "generator_function", "generator_function_expression":
			return types.SymbolFunction
		case "class", "class_declaration", "class_expression":
			return types.SymbolClass
		}
	}

	kindNode := declNode.ChildByFieldName("kind")
	if kindNode != nil && kindNode.Kind() == "const" {
		return types.SymbolConstant
	}
	return types.SymbolVariable
}

func treeSitterJSRequireImport(node *tree_sitter.Node, content []byte) string {
	functionNode := node.ChildByFieldName("function")
	if functionNode == nil || strings.TrimSpace(functionNode.Utf8Text(content)) != "require" {
		return ""
	}

	arguments := node.ChildByFieldName("arguments")
	if arguments == nil {
		return ""
	}

	for i := uint(0); i < arguments.NamedChildCount(); i++ {
		arg := arguments.NamedChild(i)
		if arg == nil {
			continue
		}
		if value := trimQuotedTreeSitterText(arg.Utf8Text(content)); value != "" {
			return value
		}
	}

	return ""
}

func normalizePythonImportName(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, " as "); ok {
		value = before
	}
	return strings.TrimSpace(value)
}

func namedChildrenOfKind(node *tree_sitter.Node, kind string) []*tree_sitter.Node {
	out := make([]*tree_sitter.Node, 0)
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child != nil && child.Kind() == kind {
			out = append(out, child)
		}
	}
	return out
}

func trimQuotedTreeSitterText(value string) string {
	value = strings.TrimSpace(value)
	return strings.Trim(value, "\"`'")
}

func mapKeys(set map[string]struct{}) []string {
	imports := make([]string, 0, len(set))
	for item := range set {
		imports = append(imports, item)
	}
	return imports
}

func buildTreeSitterSymbol(path, lang string, node *tree_sitter.Node, content []byte, name string, kind types.SymbolKind) types.Symbol {
	start := node.StartPosition()
	return types.Symbol{
		Name:      strings.TrimSpace(name),
		Kind:      kind,
		Path:      path,
		Line:      int(start.Row) + 1,
		Column:    int(start.Column) + 1,
		Language:  lang,
		Signature: strings.TrimSpace(node.Utf8Text(content)),
	}
}

func buildTreeSitterSymbolAt(path, lang string, posNode, signatureNode *tree_sitter.Node, content []byte, name string, kind types.SymbolKind) types.Symbol {
	start := posNode.StartPosition()
	return types.Symbol{
		Name:      strings.TrimSpace(name),
		Kind:      kind,
		Path:      path,
		Line:      int(start.Row) + 1,
		Column:    int(start.Column) + 1,
		Language:  lang,
		Signature: strings.TrimSpace(signatureNode.Utf8Text(content)),
	}
}

func namedChildrenByField(node *tree_sitter.Node, field string) []*tree_sitter.Node {
	out := make([]*tree_sitter.Node, 0)
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if node.FieldNameForNamedChild(uint32(i)) != field {
			continue
		}
		child := node.NamedChild(i)
		if child != nil {
			out = append(out, child)
		}
	}
	return out
}

func collectTreeSitterParseErrors(root *tree_sitter.Node) []ParseError {
	if root == nil || !root.HasError() {
		return nil
	}

	errs := make([]ParseError, 0, 8)
	walkTree(root, func(node *tree_sitter.Node) {
		if node == nil || len(errs) >= 8 {
			return
		}
		if !node.IsError() && !node.IsMissing() {
			return
		}
		pos := node.StartPosition()
		msg := "syntax error"
		if node.IsMissing() {
			msg = "missing syntax node"
		}
		errs = append(errs, ParseError{
			Line:    int(pos.Row) + 1,
			Column:  int(pos.Column) + 1,
			Message: msg,
		})
	})
	if len(errs) >= 8 {
		errs = append(errs, ParseError{
			Line:    -1,
			Column:  -1,
			Message: "...more errors omitted (showing first 8)",
		})
	}
	if len(errs) == 0 {
		errs = append(errs, ParseError{Line: 1, Column: 1, Message: "tree-sitter detected syntax errors"})
	}
	return errs
}

func walkTree(node *tree_sitter.Node, visit func(*tree_sitter.Node)) {
	if node == nil {
		return
	}
	visit(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walkTree(node.Child(i), visit)
	}
}
