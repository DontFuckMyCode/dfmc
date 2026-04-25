// Package langintel hosts per-language knowledge bases used to surface
// relevant tips, patterns, and warnings during analysis. The Engine's
// analyze pipeline queries the registry for AST-node-relevant knowledge
// so the LLM sees targeted guidance without prompting overhead.
//
// Knowledge is structured as four categories:
//   - Practice   — idiomatic patterns to follow
//   - BugPattern — common mistakes and how to fix them
//   - SecurityRule — language-specific security pitfalls
//   - Idiom      — local naming/convention norms
//
// Each entry is tagged with AST node kinds where it applies so the
// registry can return O(1) matches per node without scanning everything.

package langintel

import "encoding/json"

// Practice describes an idiomatic pattern worth following.
type Practice struct {
	ID      string   `json:"id"`      // e.g. "go-error-wrap"
	Summary string   `json:"summary"`  // one-liner
	Body    string   `json:"body"`     // markdown detail
	Langs   []string `json:"langs"`    // ["go", "python", ...]; empty = universal
	Kinds   []string `json:"kinds"`    // AST node kinds where this applies
	Tags    []string `json:"tags"`     // ["concurrency", "error-handling", ...]
}

// BugPattern describes a frequent mistake.
type BugPattern struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Body     string   `json:"body"`
	Langs    []string `json:"langs"`
	Kinds    []string `json:"kinds"`
	Severity string   `json:"severity"` // "error", "warning", "info"
	Fix      string   `json:"fix,omitempty"` // markdown suggestion
	CWE      string   `json:"cwe,omitempty"`
}

// SecurityRule describes a language-specific security concern.
type SecurityRule struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Body     string   `json:"body"`
	Langs    []string `json:"langs"`
	Kinds    []string `json:"kinds"`
	CWE      string   `json:"cwe"`
	OWASP    string   `json:"owasp,omitempty"`
	Severity string   `json:"severity"` // "critical","high","medium","low"
}

// Idiom describes local naming or style conventions.
type Idiom struct {
	ID     string   `json:"id"`
	Lang   string   `json:"lang"`
	Rule   string   `json:"rule"`    // short description
	Detail string   `json:"detail"` // markdown
	Kinds  []string `json:"kinds,omitempty"`
}

// Registry holds all knowledge for all languages.
type Registry struct {
	Practices     []Practice     `json:"practices"`
	BugPatterns   []BugPattern   `json:"bug_patterns"`
	SecurityRules []SecurityRule `json:"security_rules"`
	Idioms        []Idiom        `json:"idioms"`
}

// EmptyRegistry returns a registry with no knowledge loaded.
func EmptyRegistry() *Registry {
	return &Registry{}
}

// Merge returns a new registry with other merged in. Entries with the
// same ID are deduplicated (other wins).
func (r *Registry) Merge(other *Registry) *Registry {
	out := &Registry{
		Practices:     append([]Practice(nil), r.Practices...),
		BugPatterns:   append([]BugPattern(nil), r.BugPatterns...),
		SecurityRules: append([]SecurityRule(nil), r.SecurityRules...),
		Idioms:        append([]Idiom(nil), r.Idioms...),
	}
	out.Practices = mergeDedup(r.Practices, other.Practices)
	out.BugPatterns = mergeDedupBP(r.BugPatterns, other.BugPatterns)
	out.SecurityRules = mergeDedupSR(r.SecurityRules, other.SecurityRules)
	out.Idioms = mergeDedupI(r.Idioms, other.Idioms)
	return out
}

// MarshalJSON implements json.Marshaler for Registry, providing a stable
// field order for caching.
func (r Registry) MarshalJSON() ([]byte, error) {
	type alias Registry
	return json.Marshal(alias(r))
}
