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
	languages := []string{
		"go",
		"javascript",
		"jsx",
		"typescript",
		"tsx",
		"python",
	}
	out := make([]BackendLanguageStatus, 0, len(languages))
	for _, lang := range languages {
		out = append(out, BackendLanguageStatus{
			Language:  lang,
			Preferred: "tree-sitter",
			Active:    active,
			Reason:    reason,
		})
	}
	return out
}
