package context

// budget_trimmer.go — applies a token cap across a PromptBundle while
// reserving a floor for the dynamic (non-cacheable) sections. The
// dynamic floor exists because losing the user query / per-request
// context defeats the entire prompt; the cacheable prefix can be
// trimmed more aggressively because it's policy text, not the
// actual question. Floor is 25% of budget (with 180-token minimum,
// capped by the actual dynamic size).

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
)

// trimBundleToBudget applies a token cap across the bundle. The dynamic
// (non-cacheable) sections carry the user query and per-request context —
// losing them defeats the entire prompt. So we reserve a floor for dynamic
// content first, trim the cacheable prefix into whatever is left, and only
// then let the dynamic sections use the remainder (plus any slack the stable
// prefix didn't consume).
func trimBundleToBudget(bundle *promptlib.PromptBundle, budget int) *promptlib.PromptBundle {
	if bundle == nil || budget <= 0 || len(bundle.Sections) == 0 {
		return bundle
	}

	dynamicTokens := 0
	dynamicCount := 0
	for _, s := range bundle.Sections {
		if !s.Cacheable {
			dynamicCount++
			dynamicTokens += tokens.Estimate(s.Text)
		}
	}

	dynamicFloor := 0
	if dynamicCount > 0 {
		// Reserve up to 25% of the budget for dynamic content (with a hard
		// 180-token floor), so the user query and per-request context survive
		// even when the stable prefix is large. Cap by actual dynamic size so
		// we don't over-reserve when there isn't much dynamic content.
		dynamicFloor = budget / 4
		dynamicFloor = max(180, dynamicFloor)
		dynamicFloor = min(dynamicFloor, dynamicTokens)
		if dynamicFloor > budget {
			dynamicFloor = budget
		}
	}

	stableBudget := budget - dynamicFloor
	stableBudget = max(0, stableBudget)

	out := &promptlib.PromptBundle{Sections: make([]promptlib.PromptSection, 0, len(bundle.Sections))}
	stableRemaining := stableBudget
	dynamicRemaining := dynamicFloor
	for _, s := range bundle.Sections {
		text := s.Text
		if s.Cacheable {
			if tok := tokens.Estimate(text); tok > stableRemaining {
				text = trimToTokenBudget(text, stableRemaining)
				stableRemaining = 0 // trimmed section consumed all stable budget
			} else {
				stableRemaining -= tok
			}
			if stableRemaining < 0 {
				stableRemaining = 0
			}
		} else {
			allowance := dynamicRemaining + stableRemaining
			if tok := tokens.Estimate(text); tok > allowance {
				text = trimToTokenBudget(text, allowance)
			}
			used := tokens.Estimate(text)
			if used <= dynamicRemaining {
				dynamicRemaining -= used
			} else {
				used -= dynamicRemaining
				dynamicRemaining = 0
				stableRemaining -= used
				if stableRemaining < 0 {
					stableRemaining = 0
				}
			}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out.Sections = append(out.Sections, promptlib.PromptSection{
			Label:     s.Label,
			Text:      text,
			Cacheable: s.Cacheable,
		})
	}
	return out
}
