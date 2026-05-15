// Walk -- stable rule-author entry point for iterating an
// already-parsed file's symbols.
//
// Why this exists: callers historically did
//
//	res, _ := eng.ParseContent(ctx, path, content)
//	for _, sym := range res.Symbols { ... }
//
// which works but pins consumers to the ParseResult shape. Walk is
// the stable surface: a visitor callback receives each extracted
// symbol in source order and returns a WalkAction to keep going or
// stop. Future enhancements -- richer node info when tree-sitter is
// active, scope ranges, call-edge metadata -- can flow through the
// visitor signature without breaking existing callers.
//
// dfmc_report_ast.md §R4: "Add public Walk(ctx, node, visitor) API
// to ast.Engine for rule authors." This is the first cut. It does
// NOT expose tree-sitter Nodes (those are CGO-only and not present
// in the regex-fallback path); a future iteration can add a
// node-tree visitor when both backends can serve it.

package ast

import (
	"context"
	"fmt"
	"os"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// WalkAction tells Walk what to do after visiting a symbol. The
// zero value (WalkContinue) is the common case so visitors that
// "always want everything" can just return the zero value.
type WalkAction int

const (
	// WalkContinue keeps walking, passing the next symbol to the
	// visitor. Default behaviour when the visitor returns the zero
	// value.
	WalkContinue WalkAction = iota
	// WalkStop terminates the walk immediately. Remaining symbols
	// are not visited. The Walk call still returns the underlying
	// ParseResult so callers can inspect imports / errors / backend
	// after an early stop.
	WalkStop
)

// SymbolVisitor is invoked once per symbol during Walk. The visitor
// receives a value copy (types.Symbol is small) and returns a
// WalkAction. Returning WalkStop terminates the walk; any other
// return value (including the zero value) continues.
type SymbolVisitor func(sym types.Symbol) WalkAction

// Walk parses `content` (going through the cache exactly like
// ParseContent) and invokes `visitor` for each extracted symbol in
// source order. Returns the underlying ParseResult so callers can
// also inspect imports / errors / backend without making a separate
// ParseContent call.
//
// A nil visitor is treated as "parse only" -- semantically identical
// to ParseContent. Useful for callers that want the side-effect of
// caching the parse without committing to a visitor signature.
//
// Errors from ParseContent are surfaced unchanged. The returned
// ParseResult is nil only when ParseContent itself errored.
func (e *Engine) Walk(ctx context.Context, path string, content []byte, visitor SymbolVisitor) (*ParseResult, error) {
	if e == nil {
		return nil, fmt.Errorf("ast: Walk on nil engine")
	}
	res, err := e.ParseContent(ctx, path, content)
	if err != nil {
		return nil, err
	}
	if visitor == nil {
		return res, nil
	}
	for _, sym := range res.Symbols {
		if visitor(sym) == WalkStop {
			break
		}
	}
	return res, nil
}

// WalkPath is the convenience entry point that reads the file from
// disk before walking. Useful when callers already have a path but
// not the content. Equivalent to ReadFile + Walk; the read error is
// wrapped so callers can distinguish "I/O failed" from "parse
// failed".
func (e *Engine) WalkPath(ctx context.Context, path string, visitor SymbolVisitor) (*ParseResult, error) {
	if e == nil {
		return nil, fmt.Errorf("ast: WalkPath on nil engine")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ast: read file %s: %w", path, err)
	}
	return e.Walk(ctx, path, content, visitor)
}
