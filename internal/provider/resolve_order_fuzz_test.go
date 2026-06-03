package provider

import (
	"strings"
	"testing"
)

// FuzzResolveOrderInvariants pins the cascade-ordering contract the whole
// fallback machinery (Complete + Stream, the #60 path) depends on. For any
// primary/fallback/requested configuration, ResolveOrder must return a list
// that:
//   - contains no duplicate provider names (a dup would make Complete/Stream
//     retry the same dead provider twice and skew fallback telemetry),
//   - always ends the cascade with "offline" present (the last-resort
//     placeholder must never be dropped, or a fully-failing cascade would
//     return no provider at all),
//   - leads with the explicitly-requested provider when one was given
//     (an explicit `--provider X` must be tried first, not buried).
func FuzzResolveOrderInvariants(f *testing.F) {
	seeds := []struct{ primary, fallback, requested string }{
		{"anthropic", "openai,google", ""},
		{"anthropic", "openai,google", "deepseek"},
		{"", "", ""},
		{"OFFLINE", "offline,offline", "Anthropic"},
		{"  ", "a, b ,, c", "  b "},
	}
	for _, s := range seeds {
		f.Add(s.primary, s.fallback, s.requested)
	}

	f.Fuzz(func(t *testing.T, primary, fallbackCSV, requested string) {
		fallback := strings.Split(fallbackCSV, ",")
		r := &Router{primary: primary, fallback: fallback}

		order := r.ResolveOrder(requested)

		seen := map[string]struct{}{}
		for _, name := range order {
			if name == "" {
				t.Fatalf("ResolveOrder emitted an empty provider name: %v", order)
			}
			if _, dup := seen[name]; dup {
				t.Fatalf("duplicate provider %q in cascade %v", name, order)
			}
			seen[name] = struct{}{}
		}

		if _, ok := seen["offline"]; !ok {
			t.Fatalf("offline last-resort missing from cascade %v", order)
		}

		if rn := normalizeProviderName(requested); rn != "" {
			if len(order) == 0 || order[0] != rn {
				t.Fatalf("requested %q (normalized %q) not first in cascade %v", requested, rn, order)
			}
		}
	})
}
