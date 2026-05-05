package promptlib

// catalog.go — S5 PromptCatalog layer.
//
// Prompts in DFMC compose along axes (base ∘ task ∘ role ∘ language ∘
// profile). For provider-level prompt caching (Anthropic) we also need to
// tell the transport which parts of the rendered prompt are *stable*
// (cacheable) and which parts change per request.
//
// The catalog introduces a single sentinel — CacheBreakMarker — that
// template authors place at the stable/dynamic boundary. Rendered prompts
// are split on the marker: everything before it is cacheable, everything
// after is dynamic. Providers that do not support caching see the marker
// stripped out and receive the flat joined text.

import (
	"strings"
)

// CacheBreakMarker is the textual sentinel that divides the stable prefix of
// a rendered prompt from its dynamic suffix. Template authors place this on
// a line of its own where the natural cache boundary lies. The token ratio
// of a typical chat is ~80% stable / ~20% dynamic, so the marker tends to
// sit right before the user_query / injected_context placeholders.
const CacheBreakMarker = "<<DFMC_CACHE_BREAK>>"

// PromptSection is a labelled fragment of a rendered prompt. Cacheable
// sections form the stable prefix that providers may cache across requests.
type PromptSection struct {
	// Label is a short, stable identifier used in telemetry and tests
	// (e.g. "stable", "dynamic"). It is never sent to the provider.
	Label string
	// Text is the rendered content of the section with the cache-break
	// marker already stripped. May be empty (callers should filter).
	Text string
	// Cacheable is true for sections that should be annotated for
	// provider-level prompt caching.
	Cacheable bool
}

// PromptBundle is the structured output of the catalog. Callers that do not
// care about cache boundaries can use Text(); providers that do use
// ProviderSections() to emit their native cache-control annotations.
type PromptBundle struct {
	Sections []PromptSection
}

// Text returns the flat joined form of the bundle, with sections separated
// by a blank line. Empty sections are skipped. The cache-break marker is
// never reintroduced — callers that need the marker should inspect the
// sections directly.
func (b *PromptBundle) Text() string {
	if b == nil || len(b.Sections) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b.Sections))
	for _, s := range b.Sections {
		trimmed := strings.TrimSpace(s.Text)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, "\n\n")
}

// CacheableText returns the concatenated text of the stable prefix only —
// useful for tests and telemetry that want to compute the cacheable
// footprint without recomputing the whole bundle.
func (b *PromptBundle) CacheableText() string {
	if b == nil {
		return ""
	}
	parts := make([]string, 0, len(b.Sections))
	for _, s := range b.Sections {
		if !s.Cacheable {
			continue
		}
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// HasCacheable reports whether the bundle has at least one cacheable
// section — callers use this as a cheap check before emitting structured
// provider payloads.
func (b *PromptBundle) HasCacheable() bool {
	if b == nil {
		return false
	}
	for _, s := range b.Sections {
		if s.Cacheable && strings.TrimSpace(s.Text) != "" {
			return true
		}
	}
	return false
}

// DynamicText returns the concatenated text of the per-request sections.
func (b *PromptBundle) DynamicText() string {
	if b == nil {
		return ""
	}
	parts := make([]string, 0, len(b.Sections))
	for _, s := range b.Sections {
		if s.Cacheable {
			continue
		}
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// RenderBundle composes the library the same way Render does but returns a
// PromptBundle that preserves the stable/dynamic boundary declared by the
// CacheBreakMarker sentinel. When a template contains no marker the whole
// rendered output is treated as dynamic (no cache hint) — conservative
// default: never promise cacheability that wasn't explicitly asked for.
func (l *Library) RenderBundle(req RenderRequest) *PromptBundle {
	text := l.Render(req)
	return splitRenderedBundle(text)
}

// splitRenderedBundle pulls apart a flat rendered string on the cache-break
// marker, producing at most two sections. Exported in-package so tests can
// exercise the boundary logic without spinning up a full library.
func splitRenderedBundle(text string) *PromptBundle {
	if strings.TrimSpace(text) == "" {
		return &PromptBundle{}
	}
	before, after, found := strings.Cut(text, CacheBreakMarker)
	if !found {
		return &PromptBundle{Sections: []PromptSection{
			{Label: "dynamic", Text: strings.TrimSpace(text), Cacheable: false},
		}}
	}
	stable := strings.TrimSpace(before)
	dynamic := strings.TrimSpace(after)

	sections := make([]PromptSection, 0, 2)
	if stable != "" {
		sections = append(sections, PromptSection{Label: "stable", Text: stable, Cacheable: true})
	}
	if dynamic != "" {
		sections = append(sections, PromptSection{Label: "dynamic", Text: dynamic, Cacheable: false})
	}
	return &PromptBundle{Sections: sections}
}
