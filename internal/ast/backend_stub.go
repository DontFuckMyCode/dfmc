//go:build !cgo

package ast

func currentBackendStatus() BackendStatus {
	reason := "cgo is disabled in the current build, so tree-sitter backends are unavailable"
	return BackendStatus{
		Preferred: "tree-sitter",
		Active:    "regex",
		Reason:    reason,
		Languages: buildBackendLanguageStatuses("regex", reason),
	}
}
