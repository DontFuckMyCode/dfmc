// Package tokens provides token count estimation for prompts, context chunks,
// and tool results. Model-family-aware counters are registered via
// tiktoken/Cl100kBase for OpenAI-compatible, and a dedicated heuristic for
// Anthropic (no public tokenizer).
package tokens

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// ModelFamily classifies a model by its tokenization family.
type ModelFamily string

const (
	FamilyUnknown ModelFamily = ""
	Familycl100k  ModelFamily = "cl100k_base"   // GPT-4, GPT-3.5, OpenAI compat
	Familyo200k   ModelFamily = "o200k_base"    // GPT-4o, o1, o3
	Familyclaude  ModelFamily = "claude"        // Anthropic Claude — no public tiktoken, heuristic
	Familysonnet  ModelFamily = "claude-sonnet" // Claude 3.5 Sonnet — same heuristic as claude
	Familygemini  ModelFamily = "gemini"        // Google — has upstream API (CtxTokenCounter)
	Familydefault ModelFamily = "default"       // Unknown — heuristic fallback
)

var (
	familyMu    sync.RWMutex
	familyCache = make(map[string]ModelFamily)
)

// DetectFamily returns the tokenization family for a model name.
func DetectFamily(model string) ModelFamily {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return Familydefault
	}

	familyMu.RLock()
	if f, ok := familyCache[model]; ok {
		familyMu.RUnlock()
		return f
	}
	familyMu.RUnlock()

	f := detectFamilyImpl(model)

	familyMu.Lock()
	familyCache[model] = f
	familyMu.Unlock()

	return f
}

// detectFamilyImpl is the actual detection logic. Keep it small and inlineable.
func detectFamilyImpl(model string) ModelFamily {
	// Anthropic
	if strings.Contains(model, "claude") {
		if strings.Contains(model, "sonnet") {
			return Familysonnet
		}
		return Familyclaude
	}
	// Google
	if strings.Contains(model, "gemini") {
		return Familygemini
	}
	// OpenAI o-series
	if strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o2") || strings.HasPrefix(model, "o3") {
		return Familyo200k
	}
	// OpenAI GPT-4o / gpt-4o
	if strings.Contains(model, "gpt-4o") || strings.Contains(model, "gpt4o") {
		return Familyo200k
	}
	// OpenAI GPT-4 family
	if strings.Contains(model, "gpt-4") || strings.Contains(model, "gpt4") {
		return Familycl100k
	}
	// OpenAI GPT-3.5
	if strings.Contains(model, "gpt-3.5") || strings.Contains(model, "gpt35") {
		return Familycl100k
	}
	// DeepSeek
	if strings.Contains(model, "deepseek") {
		return Familycl100k
	}
	// Kimi / Moonshot
	if strings.Contains(model, "kimi") || strings.Contains(model, "moonshot") {
		return Familycl100k
	}
	// Azure OpenAI
	if strings.Contains(model, "azure") || strings.Contains(model, "gpt-4") {
		return Familycl100k
	}
	// Mistral / others — treat as cl100k (most OpenAI-compatible)
	if strings.Contains(model, "mistral") || strings.Contains(model, "llama") || strings.Contains(model, "qwen") {
		return Familycl100k
	}
	return Familydefault
}

var encoderNameForFamily = map[ModelFamily]string{
	Familycl100k: "cl100k_base",
	Familyo200k:  "o200k_base",
}

// EncoderName returns the tiktoken encoding name for a family, or "" if
// the family uses heuristic counting.
func EncoderName(f ModelFamily) string {
	return encoderNameForFamily[f]
}

// counterForFamily caches a compiled counter per family to avoid
// repeated encoding loads.
var (
	counterMu  sync.RWMutex
	counters   = make(map[ModelFamily]Counter)
	loadErrors = make(map[ModelFamily]bool) // poison pill to avoid repeated failures
)

// CounterForFamily returns a Counter for the given family. Returns nil, false
// when the family uses heuristic counting (claude/gemini/default) or when
// loading the tiktoken encoding fails.
func CounterForFamily(f ModelFamily) (Counter, bool) {
	if enc := EncoderName(f); enc != "" {
		counterMu.RLock()
		c, ok := counters[f]
		bad := loadErrors[f]
		counterMu.RUnlock()
		if ok {
			return c, true
		}
		if bad {
			return nil, false
		}

		tc, err := NewTiktokenCounter(enc)
		counterMu.Lock()
		if err != nil {
			loadErrors[f] = true
			counterMu.Unlock()
			return nil, false
		}
		counters[f] = tc
		counterMu.Unlock()
		return tc, true
	}
	return nil, false // heuristic family
}

// CountForModel returns the token count for text using the counter
// appropriate for model. Falls back to HeuristicCounter when the family
// has no tiktoken encoding or loading fails.
func CountForModel(model, text string) int {
	family := DetectFamily(model)
	if c, ok := CounterForFamily(family); ok {
		return c.Count(text)
	}
	return heuristicForFamily(family, text)
}

// heuristicForFamily applies the best heuristic for a given family.
//
// chars counts RUNES, not bytes: a byte count roughly doubles the
// estimate for multibyte (Turkish/CJK) content, which made the engine
// believe history was larger than it was and compact/trim too early —
// silently dropping real context. Each non-empty branch also floors at
// +1 so a short-but-real string never estimates to zero (downstream
// chunk builders skip token counts <= 0).
func heuristicForFamily(f ModelFamily, text string) int {
	chars := utf8.RuneCountInString(text)
	if chars == 0 {
		return 0
	}
	switch f {
	case Familysonnet:
		// Claude 3.5 Sonnet uses same tokenizer as Claude 3
		// ~3.5 chars/token for code-heavy content
		return chars/3 + 1
	case Familyclaude:
		// Claude 3/3.5 — similar to GPT-4
		return chars/3 + 1
	case Familygemini:
		// Gemini uses SentencePiece — approx 4 chars/token for mixed content
		return chars/4 + 1
	default:
		// Unknown — use the calibrated HeuristicCounter
		h := NewHeuristic()
		return h.Count(text)
	}
}
