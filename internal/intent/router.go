package intent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// ProviderLookup finds a provider by name. The router takes a function
// rather than the provider.Router type directly so tests can stub the
// lookup without spinning up a real router. In production the engine
// passes router.Get through.
type ProviderLookup func(name string) (provider.Provider, bool)

// Router orchestrates the intent classification call. Construct one per
// engine and re-use it: it caches no state between Evaluate calls, but
// holds references to the config and provider lookup so callers don't
// have to re-resolve them on every turn.
type Router struct {
	cfg    config.IntentConfig
	lookup ProviderLookup
}

// NewRouter builds a router. cfg.Enabled=false makes Evaluate always
// return Fallback(raw) so the engine can wire this in unconditionally.
// lookup may be nil — Evaluate then short-circuits to fallback. This
// permissive constructor is intentional: the intent layer must never
// be the reason an engine fails to start.
func NewRouter(cfg config.IntentConfig, lookup ProviderLookup) *Router {
	return &Router{cfg: cfg, lookup: lookup}
}

// Enabled reports whether the router will actually call an LLM. Callers
// can use this to avoid building an expensive Snapshot when the layer
// is off (or to surface a UI badge).
func (r *Router) Enabled() bool {
	if r == nil || !r.cfg.Enabled {
		return false
	}
	if r.lookup == nil {
		return false
	}
	if _, ok := r.resolveProvider(); !ok {
		return false
	}
	return true
}

// Evaluate runs the classifier. Always returns a usable Decision, even
// on error: under FailOpen=true (the default) any failure produces a
// Fallback(raw) decision and a nil error, so the engine can route on
// the result without an extra nil-check. When FailOpen=false the error
// is returned to the caller alongside Fallback(raw), giving the engine
// a chance to surface the failure (e.g. for ops debugging) while still
// having a safe routing decision in hand.
//
// Provider call has a hard timeout from cfg.TimeoutMs to keep latency
// bounded — a slow intent call must not stall the user-facing turn.
func (r *Router) Evaluate(ctx context.Context, raw string, snap Snapshot) (Decision, error) {
	if r == nil || !r.cfg.Enabled {
		return Fallback(raw), nil
	}
	if strings.TrimSpace(raw) == "" {
		return Fallback(raw), nil
	}
	prov, ok := r.resolveProvider()
	if !ok {
		return Fallback(raw), nil
	}

	start := time.Now()
	timeout := time.Duration(r.cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	system, user := buildClassifierMessages(snap.Render(r.cfg.MaxSnapshotChars), raw)
	req := provider.CompletionRequest{
		System: system,
		Messages: []provider.Message{
			{Role: types.RoleUser, Content: user},
		},
		ToolChoice: "none",
	}
	if r.cfg.Model != "" {
		req.Model = r.cfg.Model
	}

	resp, err := prov.Complete(callCtx, req)
	latency := time.Since(start)
	if err != nil {
		return r.handleErr(raw, err, latency)
	}
	dec, parseErr := parseDecision(resp.Text, raw)
	if parseErr != nil {
		return r.handleErr(raw, parseErr, latency)
	}
	dec.Source = "llm"
	dec.Latency = latency
	return dec, nil
}

// resolveProvider picks the configured provider when set, otherwise
// walks a small priority list of cheap-and-fast options. The first one
// the lookup returns wins. Returns (nil, false) when none are
// available — common in offline-only setups, and the right time to
// silently disable the layer rather than error.
func (r *Router) resolveProvider() (provider.Provider, bool) {
	if r.lookup == nil {
		return nil, false
	}
	if name := strings.TrimSpace(r.cfg.Provider); name != "" {
		if p, ok := r.lookup(name); ok {
			return p, true
		}
	}
	// Default cascade: Haiku, gpt-4o-mini, gemini flash. Kept short on
	// purpose — these three cover ~95% of user keys and are all cheap
	// enough that the intent layer's per-turn cost is rounding error.
	for _, name := range []string{"anthropic", "openai", "gemini"} {
		if p, ok := r.lookup(name); ok && p.Hints().SupportsTools {
			// SupportsTools is a proxy for "real provider, not the
			// offline placeholder" — placeholders advertise
			// SupportsTools=false. Avoids wasting a call on a stub
			// that would just echo the prompt back.
			return p, true
		}
	}
	return nil, false
}

func (r *Router) handleErr(raw string, err error, latency time.Duration) (Decision, error) {
	dec := Fallback(raw)
	dec.Latency = latency
	dec.Reasoning = fmt.Sprintf("intent layer failed open: %v", err)
	if r.cfg.FailOpen {
		return dec, nil
	}
	return dec, err
}

// rawDecision matches the JSON contract spelled out in the system
// prompt. Kept private; callers see the typed Decision.
type rawDecision struct {
	Intent           string `json:"intent"`
	EnrichedRequest  string `json:"enriched_request"`
	Reasoning        string `json:"reasoning"`
	FollowUpQuestion string `json:"follow_up_question"`
}

// parseDecision unmarshals the classifier's response, normalizes empty
// fields, and validates that intent is one of the three legal values.
// On any failure it returns an *invalidJSONError so the router knows
// to fall back. Tolerates leading/trailing whitespace and a single
// pair of code-fence markers in case the model wraps the JSON.
func parseDecision(text, raw string) (Decision, error) {
	cleaned := stripCodeFences(strings.TrimSpace(text))
	if cleaned == "" {
		return Decision{}, &invalidJSONError{raw: text, err: errors.New("empty response")}
	}
	var rd rawDecision
	if err := json.Unmarshal([]byte(cleaned), &rd); err != nil {
		return Decision{}, &invalidJSONError{raw: cleaned, err: err}
	}
	intent := Intent(strings.ToLower(strings.TrimSpace(rd.Intent)))
	switch intent {
	case IntentResume, IntentNew, IntentClarify:
	default:
		return Decision{}, &invalidJSONError{
			raw: cleaned,
			err: fmt.Errorf("unknown intent value %q", rd.Intent),
		}
	}
	enriched := strings.TrimSpace(rd.EnrichedRequest)
	if enriched == "" && intent != IntentClarify {
		// Classifier returned a routing decision but forgot the rewrite.
		// Better to fall back to the raw input than to send empty text
		// to the main model.
		enriched = raw
	}
	return Decision{
		Intent:           intent,
		EnrichedRequest:  enriched,
		Reasoning:        strings.TrimSpace(rd.Reasoning),
		FollowUpQuestion: strings.TrimSpace(rd.FollowUpQuestion),
	}, nil
}

// stripCodeFences peels a single ```...``` (or ```json...```) wrapper
// off the response. Some models still wrap JSON despite being told not
// to; this is a one-line defense rather than a parser-rejection.
func stripCodeFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// Strip the language tag line (```json) if present.
		first := strings.TrimSpace(s[:i])
		if first == "" || isAllAlpha(first) {
			s = s[i+1:]
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

func isAllAlpha(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
}
