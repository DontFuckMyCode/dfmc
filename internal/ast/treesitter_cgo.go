//go:build cgo

package ast

import (
	"context"
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func parseWithTreeSitter(ctx context.Context, path, lang string, content []byte) ([]types.Symbol, []string, []ParseError, bool, error) {
	if lang != "go" {
		return nil, nil, nil, false, nil
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(tree_sitter.NewLanguage(tree_sitter_go.Language())); err != nil {
		return nil, nil, nil, false, fmt.Errorf("tree-sitter go language: %w", err)
	}

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

	importsSet := map[string]struct{}{}
	var symbols []types.Symbol
	walkTree(root, func(node *tree_sitter.Node) {
		switch node.Kind() {
		case "function_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				symbols = append(symbols, buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), types.SymbolFunction))
			}
		case "method_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				symbols = append(symbols, buildTreeSitterSymbol(path, lang, node, content, name.Utf8Text(content), types.SymbolMethod))
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
		case "import_spec":
			pathNode := node.ChildByFieldName("path")
			if pathNode == nil {
				return
			}
			value := strings.TrimSpace(pathNode.Utf8Text(content))
			value = strings.Trim(value, "\"`'")
			if value != "" {
				importsSet[value] = struct{}{}
			}
		}
	})

	imports := make([]string, 0, len(importsSet))
	for item := range importsSet {
		imports = append(imports, item)
	}

	return symbols, imports, collectTreeSitterParseErrors(root), true, nil
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
