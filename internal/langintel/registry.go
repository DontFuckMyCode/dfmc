// Package langintel hosts per-language knowledge bases used to surface
// relevant tips, patterns, and warnings during analysis.
package langintel

import "slices"

// NewRegistry returns a fully-loaded registry with all embedded knowledge bases.
func NewRegistry() *Registry {
	base := goKB()
	base = base.Merge(tsKB())
	base = base.Merge(pyKB())
	base = base.Merge(rustKB())
	base = base.Merge(javaKB())
	base = base.Merge(phpKB())
	base = base.Merge(csharpKB())
	return base
}

// BestPracticesFor returns relevant Practice summaries for the given
// AST node kinds. Returns up to maxTips entries, sorted by relevance
// (entries with kind matches rank above kind-agnostic entries).
func (r *Registry) BestPracticesFor(kinds []string, maxTips int) []Practice {
	var matched, general []Practice
	for _, p := range r.Practices {
		if len(p.Kinds) == 0 {
			general = append(general, p)
		} else if anySliceContains(p.Kinds, kinds) {
			matched = append(matched, p)
		}
	}
	out := append([]Practice(nil), matched...)
	out = append(out, general...)
	if maxTips > 0 && len(out) > maxTips {
		out = out[:maxTips]
	}
	return out
}

// BugPatternsFor returns BugPatterns applicable to given AST node kinds.
func (r *Registry) BugPatternsFor(kinds []string) []BugPattern {
	var out []BugPattern
	for _, b := range r.BugPatterns {
		if len(b.Kinds) == 0 || anySliceContains(b.Kinds, kinds) {
			out = append(out, b)
		}
	}
	return out
}

// SecurityRulesFor returns SecurityRules applicable to given AST node kinds.
func (r *Registry) SecurityRulesFor(kinds []string) []SecurityRule {
	var out []SecurityRule
	for _, s := range r.SecurityRules {
		if len(s.Kinds) == 0 || anySliceContains(s.Kinds, kinds) {
			out = append(out, s)
		}
	}
	return out
}

// IdiomsFor returns Idioms for the given language.
func (r *Registry) IdiomsFor(lang string) []Idiom {
	lang = normalizeLang(lang)
	var out []Idiom
	for _, i := range r.Idioms {
		if i.Lang == "" || i.Lang == lang {
			out = append(out, i)
		}
	}
	return out
}

// ForLang returns entries relevant to a given language.
func (r *Registry) ForLang(l string) *Registry {
	l = normalizeLang(l)
	out := &Registry{}
	for _, p := range r.Practices {
		if len(p.Langs) == 0 || slices.Contains(p.Langs, l) {
			out.Practices = append(out.Practices, p)
		}
	}
	for _, b := range r.BugPatterns {
		if len(b.Langs) == 0 || slices.Contains(b.Langs, l) {
			out.BugPatterns = append(out.BugPatterns, b)
		}
	}
	for _, s := range r.SecurityRules {
		if len(s.Langs) == 0 || slices.Contains(s.Langs, l) {
			out.SecurityRules = append(out.SecurityRules, s)
		}
	}
	for _, i := range r.Idioms {
		if i.Lang == "" || i.Lang == l {
			out.Idioms = append(out.Idioms, i)
		}
	}
	return out
}

// ForKinds returns entries applicable to any of the given AST node kinds.
func (r *Registry) ForKinds(kinds []string) *Registry {
	out := &Registry{}
	for _, p := range r.Practices {
		if len(p.Kinds) == 0 || anySliceContains(p.Kinds, kinds) {
			out.Practices = append(out.Practices, p)
		}
	}
	for _, b := range r.BugPatterns {
		if len(b.Kinds) == 0 || anySliceContains(b.Kinds, kinds) {
			out.BugPatterns = append(out.BugPatterns, b)
		}
	}
	for _, s := range r.SecurityRules {
		if len(s.Kinds) == 0 || anySliceContains(s.Kinds, kinds) {
			out.SecurityRules = append(out.SecurityRules, s)
		}
	}
	return out
}

// NormalizeLang is exported so callers can normalize language names.
func NormalizeLang(l string) string { return normalizeLang(l) }

// anySliceContains returns true if any element of a is in b.
func anySliceContains(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// normalizeLang returns the canonical form of a language identifier.
func normalizeLang(l string) string {
	switch l {
	case "go", "golang":
		return "go"
	case "ts", "typescript":
		return "typescript"
	case "js", "javascript":
		return "javascript"
	case "python", "py":
		return "python"
	case "rust", "rs":
		return "rust"
	case "java":
		return "java"
	case "php":
		return "php"
	case "csharp", "c#":
		return "csharp"
	default:
		return l
	}
}

// --- deduplication helpers (used by types.go Merge) ---

func mergeDedup(a, b []Practice) []Practice {
	seen := make(map[string]bool)
	for _, p := range a {
		seen[p.ID] = true
	}
	out := append([]Practice(nil), a...)
	for _, p := range b {
		if !seen[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func mergeDedupBP(a, b []BugPattern) []BugPattern {
	seen := make(map[string]bool)
	for _, p := range a {
		seen[p.ID] = true
	}
	out := append([]BugPattern(nil), a...)
	for _, p := range b {
		if !seen[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func mergeDedupSR(a, b []SecurityRule) []SecurityRule {
	seen := make(map[string]bool)
	for _, r := range a {
		seen[r.ID] = true
	}
	out := append([]SecurityRule(nil), a...)
	for _, r := range b {
		if !seen[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

func mergeDedupI(a, b []Idiom) []Idiom {
	seen := make(map[string]bool)
	for _, i := range a {
		seen[i.ID] = true
	}
	out := append([]Idiom(nil), a...)
	for _, i := range b {
		if !seen[i.ID] {
			out = append(out, i)
		}
	}
	return out
}
