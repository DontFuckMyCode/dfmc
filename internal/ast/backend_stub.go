//go:build !cgo

package ast

func currentBackendStatus() BackendStatus {
	return BackendStatus{
		Preferred: "tree-sitter",
		Active:    "regex",
		Reason:    "cgo is disabled in the current build, so tree-sitter backends are unavailable",
	}
}
