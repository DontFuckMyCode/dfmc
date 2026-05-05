//go:build !cgo

package ast

import (
	"context"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func parseWithTreeSitter(_ context.Context, _ string, _ string, _ []byte) ([]types.Symbol, []string, []ParseError, bool, error) {
	return nil, nil, nil, false, nil
}
