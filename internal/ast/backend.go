package ast

type BackendLanguageStatus struct {
	Language  string `json:"language"`
	Preferred string `json:"preferred"`
	Active    string `json:"active"`
	Reason    string `json:"reason,omitempty"`
}

type BackendStatus struct {
	Preferred string                  `json:"preferred"`
	Active    string                  `json:"active"`
	Reason    string                  `json:"reason,omitempty"`
	Languages []BackendLanguageStatus `json:"languages,omitempty"`
}

func (e *Engine) BackendStatus() BackendStatus {
	return currentBackendStatus()
}

func (e *Engine) ParseMetrics() ParseMetrics {
	if e == nil || e.metrics == nil {
		return ParseMetrics{}
	}
	return e.metrics.snapshot()
}

func buildBackendLanguageStatuses(active, reason string) []BackendLanguageStatus {
	// tree-sitter-backed languages: Active follows the `active`
	// parameter, which callers pass as the overall BackendStatus.Active
	// ("hybrid" in cgo builds, "regex" in !cgo builds) so a language's
	// Active always equals the overall Active.
	tsLanguages := []string{
		"go",
		"javascript",
		"jsx",
		"typescript",
		"tsx",
		"python",
	}
	// Regex-only languages: tree-sitter bindings are not wired (no
	// CGO grammar imported), so Active is always "regex" regardless
	// of build mode. Preferred is also "regex" -- there's nothing
	// better to fall back to and we don't want callers to interpret
	// these as "downgraded".
	regexOnly := []string{
		"rust",
		"ruby",
		"java",
	}
	out := make([]BackendLanguageStatus, 0, len(tsLanguages)+len(regexOnly))
	for _, lang := range tsLanguages {
		out = append(out, BackendLanguageStatus{
			Language:  lang,
			Preferred: "tree-sitter",
			Active:    active,
			Reason:    reason,
		})
	}
	for _, lang := range regexOnly {
		out = append(out, BackendLanguageStatus{
			Language:  lang,
			Preferred: "regex",
			Active:    "regex",
		})
	}
	return out
}
