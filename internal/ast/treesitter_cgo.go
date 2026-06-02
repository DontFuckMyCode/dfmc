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
var treeSitterParsers = newTreeSitterParserRegistry()

// treeSitterParserRegistry manages per-language sync.Pool instances.
// Uses sync.Map for lock-free reads — sync.Pool itself is already
// concurrent-safe, so we avoid redundant mutex contention.
type treeSitterParserRegistry struct {
	pools  sync.Map // map[string]*sync.Pool — lock-free reads
	newOne func() any
}

func newTreeSitterParserRegistry() *treeSitterParserRegistry {
	return &treeSitterParserRegistry{
		newOne: func() any {
			return tree_sitter.NewParser()
		},
	}
}

func (r *treeSitterParserRegistry) pool(lang string) *sync.Pool {
	if pool, ok := r.pools.Load(lang); ok {
		return pool.(*sync.Pool)
	}
	pool := &sync.Pool{New: r.newOne}
	r.pools.Store(lang, pool)
	return pool
}

func parseWithTreeSitter(ctx context.Context, path, lang string, content []byte) ([]types.Symbol, []string, []ParseError, bool, error) {
	language, handled, err := treeSitterLanguageFor(lang)
	if !handled || err != nil {
		return nil, nil, nil, handled, err
	}

	pool := treeSitterParserPool(lang)
	p := pool.Get()
	var parser *tree_sitter.Parser
	if p != nil {
		var ok bool
		parser, ok = p.(*tree_sitter.Parser)
		if !ok {
			parser = nil
		}
	}
	if parser == nil {
		parser = tree_sitter.NewParser()
	}
	// healthy gates the pool return — see finalizeTreeSitterParser.
	healthy := false
	defer func() { finalizeTreeSitterParser(pool, parser, healthy) }()

	if err := parser.SetLanguage(language); err != nil {
		return nil, nil, nil, true, fmt.Errorf("tree-sitter %s language: %w", lang, err)
	}
	healthy = true

	// Periodic context-check during parse. tree-sitter's ParseCtx
	// honours ctx deadline but we check explicitly so a cancelled
	// context surfaces immediately rather than returning stale symbols.
	select {
	case <-ctx.Done():
		return nil, nil, nil, false, ctx.Err()
	default:
	}

	tree := parser.ParseWithOptions(func(byteIndex int, _ tree_sitter.Point) []byte {
		if byteIndex >= len(content) {
			return nil
		}
		return content[byteIndex:]
	}, nil, &tree_sitter.ParseOptions{
		ProgressCallback: func(tree_sitter.ParseState) bool {
			return ctx.Err() != nil
		},
	})
	if tree == nil {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, true, err
		}
		return nil, nil, nil, false, fmt.Errorf("tree-sitter parser returned nil for language %q (content length %d)", lang, len(content))
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil, nil, nil, true, nil
	}

	// Check context cancellation before returning parsed results
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, false, err
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

type parserReturner interface {
	Put(any)
}

func finalizeTreeSitterParser(pool parserReturner, parser *tree_sitter.Parser, healthy bool) {
	if parser == nil {
		return
	}
	if healthy {
		pool.Put(parser)
	} else {
		parser.Close()
	}
}

func treeSitterParserPool(lang string) *sync.Pool {
	return treeSitterParsers.pool(lang)
}

func treeSitterLanguageFor(lang string) (*tree_sitter.Language, bool, error) {
	switch lang {
	case "go":
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), true, nil
	case "javascript", "jsx":
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language()), true, nil
	case "typescript":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), true, nil
	case "tsx":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), true, nil
	case "python":
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), true, nil
	default:
		return nil, false, nil
	}
}

func collectTreeSitterParseErrors(root *tree_sitter.Node) []ParseError {
	if root.HasError() {
		errors := []ParseError{}
		var walkErrors func(n *tree_sitter.Node)
		walkErrors = func(n *tree_sitter.Node) {
			if n.Kind() == "ERROR" {
				errors = append(errors, ParseError{
					Line:    int(n.StartPosition().Row) + 1,
					Column:  int(n.StartPosition().Column) + 1,
					Message: n.ToSexp(),
				})
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				walkErrors(n.Child(i))
			}
		}
		walkErrors(root)
		return errors
	}
	return nil
}

func extractGoTreeSitter(path, lang string, root *tree_sitter.Node, content []byte) []types.Symbol {
	symbols := []types.Symbol{}
	seen := make(map[string]bool)

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "function_declaration":
			name := childText(n, "identifier", content)
			if name != "" && !seen[name] {
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "function",
					Path:      path,
					Language:  "go",
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "method_declaration":
			name := childText(n, "name", content)
			if name == "" {
				name = childText(n, "field_identifier", content)
			}
			receiver := ""
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				if child.Kind() == "parameter_list" {
					receiver = textForNode(child, content)
					break
				}
			}
			if name != "" {
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "method",
					Path:      path,
					Language:  "go",
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
					Metadata:  map[string]string{"receiver": receiver},
				})
			}
		case "type_declaration":
			// type_declaration wraps one or more type_specs; the name is
			// the spec's type_identifier child, and an interface_type child
			// marks it as an interface rather than a plain type/struct.
			for i := uint(0); i < n.ChildCount(); i++ {
				spec := n.Child(i)
				if spec.Kind() != "type_spec" {
					continue
				}
				name := childText(spec, "type_identifier", content)
				if name == "" || seen[name] {
					continue
				}
				kind := types.SymbolType
				for j := uint(0); j < spec.ChildCount(); j++ {
					if spec.Child(j).Kind() == "interface_type" {
						kind = types.SymbolInterface
						break
					}
				}
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:     name,
					Kind:     kind,
					Path:     path,
					Language: "go",
					Line:     int(spec.StartPosition().Row) + 1,
					Column:   int(spec.StartPosition().Column) + 1,
				})
			}
		case "const_spec", "var_spec":
			// Match the spec node directly (walk reaches it whether it's a
			// direct child of the declaration or nested in a *_spec_list, as
			// grouped `var (...)` blocks are). The spec's identifier children
			// are the declared names; its type field is a type_identifier and
			// is skipped.
			declKind := types.SymbolConstant
			if n.Kind() == "var_spec" {
				declKind = types.SymbolVariable
			}
			for j := uint(0); j < n.ChildCount(); j++ {
				id := n.Child(j)
				if id.Kind() != "identifier" {
					continue
				}
				name := textForNode(id, content)
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:     name,
					Kind:     declKind,
					Path:     path,
					Language: "go",
					Line:     int(id.StartPosition().Row) + 1,
					Column:   int(id.StartPosition().Column) + 1,
				})
			}
		case "call_expression":
			fn := n.ChildByFieldName("function")
			if fn != nil {
				name := textForNode(fn, content)
				if name != "" {
					symbols = append(symbols, types.Symbol{
						Name:     name,
						Kind:     "call",
						Path:     path,
						Language: "go",
						Line:     int(n.StartPosition().Row) + 1,
						Column:   int(n.StartPosition().Column) + 1,
					})
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return symbols
}

func extractGoTreeSitterImports(root *tree_sitter.Node, content []byte) []string {
	imports := []string{}
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if n.Kind() == "import_declaration" {
			if importPath := extractImportPath(n, content); importPath != "" {
				imports = append(imports, importPath)
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return imports
}

func extractImportPath(node *tree_sitter.Node, content []byte) string {
	var find StringVisitor
	find.visit(node, content)
	if find.result != "" && len(find.result) >= 2 {
		return find.result[1 : len(find.result)-1]
	}
	return ""
}

type StringVisitor struct {
	result string
	done   bool
}

func (f *StringVisitor) visit(n *tree_sitter.Node, content []byte) {
	if f.done {
		return
	}
	if n.Kind() == "string" || n.Kind() == "string_literal" || n.Kind() == "string_content" {
		f.result = textForNode(n, content)
		f.done = true
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		f.visit(n.Child(i), content)
	}
}

func extractJSTreeSitter(path, lang string, root *tree_sitter.Node, content []byte) ([]types.Symbol, []string) {
	symbols := []types.Symbol{}
	imports := []string{}
	seen := make(map[string]bool)

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "function_declaration":
			name := childText(n, "identifier", content)
			if name != "" && !seen[name] {
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "function",
					Path:      path,
					Language:  lang,
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "method_definition":
			name := childText(n, "property_identifier", content)
			if name != "" {
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "method",
					Path:      path,
					Language:  lang,
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "class_declaration", "abstract_class_declaration":
			name := childText(n, "identifier", content)
			if name == "" {
				// TypeScript class names are type_identifier, not identifier.
				name = childText(n, "type_identifier", content)
			}
			if name != "" && !seen[name] {
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "class",
					Path:      path,
					Language:  lang,
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "enum_declaration":
			name := childText(n, "identifier", content)
			if name != "" && !seen[name] {
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "enum",
					Path:      path,
					Language:  lang,
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "variable_declarator":
			// `const f = (…) => …` / `= function(){}` reads as a function
			// symbol, matching how the regex extractor treats arrow/function
			// values bound to a const/let name.
			hasFn := false
			for i := uint(0); i < n.ChildCount(); i++ {
				switch n.Child(i).Kind() {
				case "arrow_function", "function", "function_expression", "generator_function":
					hasFn = true
				}
			}
			if hasFn {
				name := childText(n, "identifier", content)
				if name != "" && !seen[name] {
					seen[name] = true
					symbols = append(symbols, types.Symbol{
						Name:     name,
						Kind:     "function",
						Path:     path,
						Language: lang,
						Line:     int(n.StartPosition().Row) + 1,
						Column:   int(n.StartPosition().Column) + 1,
					})
				}
			}
		case "import_statement":
			if imp := extractImportPath(n, content); imp != "" {
				imports = append(imports, imp)
			}
		case "call_expression":
			fn := n.ChildByFieldName("function")
			if fn != nil {
				name := textForNode(fn, content)
				if name != "" {
					symbols = append(symbols, types.Symbol{
						Name:     name,
						Kind:     "call",
						Path:     path,
						Language: lang,
						Line:     int(n.StartPosition().Row) + 1,
						Column:   int(n.StartPosition().Column) + 1,
					})
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return symbols, imports
}

func extractPythonTreeSitter(path, lang string, root *tree_sitter.Node, content []byte) ([]types.Symbol, []string) {
	symbols := []types.Symbol{}
	imports := []string{}
	seen := make(map[string]bool)

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "function_definition":
			name := childText(n, "name", content)
			if name != "" && !seen[name] {
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "function",
					Path:      path,
					Language:  lang,
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "class_definition":
			name := childText(n, "name", content)
			if name != "" && !seen[name] {
				seen[name] = true
				symbols = append(symbols, types.Symbol{
					Name:      name,
					Kind:      "class",
					Path:      path,
					Language:  lang,
					Line:      int(n.StartPosition().Row) + 1,
					Column:    int(n.StartPosition().Column) + 1,
					Signature: signatureBeforeBody(n, content),
				})
			}
		case "import_statement", "import_from_statement":
			// Python imports can be complex, using basic extractor for now
			var find StringVisitor
			find.visit(n, content)
			if find.result != "" {
				imports = append(imports, find.result)
			}
		case "call":
			fn := n.ChildByFieldName("function")
			if fn != nil {
				name := textForNode(fn, content)
				if name != "" {
					symbols = append(symbols, types.Symbol{
						Name:     name,
						Kind:     "call",
						Path:     path,
						Language: lang,
						Line:     int(n.StartPosition().Row) + 1,
						Column:   int(n.StartPosition().Column) + 1,
					})
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return symbols, imports
}

func childText(n *tree_sitter.Node, childType string, content []byte) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() == childType {
			return textForNode(child, content)
		}
	}
	return ""
}

func textForNode(n *tree_sitter.Node, content []byte) string {
	start, end := n.StartByte(), n.EndByte()
	if int(end) > len(content) {
		end = uint(len(content))
	}
	if start > end {
		return ""
	}
	return string(content[start:end])
}

// signatureBeforeBody returns the declaration text up to (but not
// including) the node's body block, matching what the regex extractors
// put in Symbol.Signature. Works for any tree-sitter grammar that uses
// "body" as the field name for the inner block (Go function /
// method_declaration, JS function / method / class_declaration, Python
// function / class_definition). For bodyless declarations (interface
// methods, externally-linked stubs) it falls back to the first line of
// the node's text.
func signatureBeforeBody(n *tree_sitter.Node, content []byte) string {
	body := n.ChildByFieldName("body")
	if body != nil {
		start, end := n.StartByte(), body.StartByte()
		if end > start && int(end) <= len(content) {
			return strings.TrimSpace(string(content[start:end]))
		}
	}
	raw := textForNode(n, content)
	if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw)
}
