//go:build cgo

package ast

func currentBackendStatus() BackendStatus {
	reason := "tree-sitter is available for go, javascript/jsx, typescript/tsx, and python in cgo-enabled builds; unsupported languages continue to use regex fallback"
	return BackendStatus{
		Preferred: "tree-sitter",
		Active:    "hybrid",
		Reason:    reason,
		Languages: buildBackendLanguageStatuses("tree-sitter", reason),
	}
}
