//go:build cgo

package ast

func currentBackendStatus() BackendStatus {
	return BackendStatus{
		Preferred: "tree-sitter",
		Active:    "hybrid",
		Reason:    "tree-sitter is available for cgo-enabled builds; unsupported languages continue to use regex fallback",
	}
}
