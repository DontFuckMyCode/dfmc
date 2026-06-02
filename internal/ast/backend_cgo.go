//go:build cgo

package ast

func currentBackendStatus() BackendStatus {
	reason := "tree-sitter is available for go, javascript/jsx, typescript/tsx, and python in cgo-enabled builds; unsupported languages continue to use regex fallback"
	return BackendStatus{
		Preferred: "tree-sitter",
		Active:    "hybrid",
		Reason:    reason,
		// Per-language Active inherits the overall mode ("hybrid") rather
		// than the concrete backend, so a language's Active always equals
		// BackendStatus.Active.
		Languages: buildBackendLanguageStatuses("hybrid", reason),
	}
}
