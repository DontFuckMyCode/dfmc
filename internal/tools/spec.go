// Package tools — spec.go defines the provider-agnostic tool description model.
//
// A ToolSpec is the single source of truth for a tool's shape: its name,
// purpose, JSON-Schema argument shape, risk level, and search metadata. All
// provider serializers (Anthropic tool_use, OpenAI tool_calls/function-calling)
// derive their schemas from this type — we never hand-write per-provider
// schemas.
//
// ToolSpec is also the input to the meta-tool surface. With many backend tools
// registered, we expose only 4 meta tools to the model (tool_search, tool_help,
// tool_call, tool_batch_call) and the model uses them to discover and invoke
// backend tools on demand — keeping prompt token cost flat regardless of
// registry size.
package tools

import (
	"encoding/json"
	"sort"
	"strings"
)

// Risk classifies a tool's blast radius. Used by the agent loop to decide
// whether a call needs confirmation, is auto-approved, or is blocked in
// restricted sandbox modes.
type Risk string

const (
	RiskRead    Risk = "read"    // no side effects — cat, list, grep, search
	RiskWrite   Risk = "write"   // mutates project files
	RiskExecute Risk = "execute" // runs arbitrary commands / network I/O
)

// ArgType is a compact JSON-Schema-lite type tag. We use these instead of
// freeform JSON-Schema strings so provider serializers can map them
// predictably.
type ArgType string

const (
	ArgString  ArgType = "string"
	ArgInteger ArgType = "integer"
	ArgNumber  ArgType = "number"
	ArgBoolean ArgType = "boolean"
	ArgObject  ArgType = "object"
	ArgArray   ArgType = "array"
)

// Arg describes one parameter of a tool. `Items` applies to arrays; `Enum`
// restricts string/integer values.
type Arg struct {
	Name        string   `json:"name"`
	Type        ArgType  `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	Items       *Arg     `json:"items,omitempty"`
	Example     any      `json:"example,omitempty"`
	MinValue    *float64 `json:"min,omitempty"`
	MaxValue    *float64 `json:"max,omitempty"`
}

// ToolSpec is the canonical, provider-agnostic description of a tool.
type ToolSpec struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"` // human-friendly label
	Summary     string   `json:"summary"`         // one-line description (what it does)
	Purpose     string   `json:"purpose,omitempty"`
	// Prompt is the operational guide shown to the model when it asks for
	// detailed help on the tool (tool_help). It should call out when to pick
	// this tool over alternatives, footguns, and patterns that matter in
	// practice. Multi-line markdown is fine. Kept out of ShortHelp and the
	// system-prompt overview to keep common paths cheap.
	Prompt      string   `json:"prompt,omitempty"`
	Args        []Arg    `json:"args,omitempty"`
	Returns     string   `json:"returns,omitempty"`
	Risk        Risk     `json:"risk"`
	Tags        []string `json:"tags,omitempty"`     // e.g. ["filesystem","read","code"]
	Examples    []string `json:"examples,omitempty"` // short usage examples
	Idempotent  bool     `json:"idempotent,omitempty"`
	CostHint    string   `json:"cost_hint,omitempty"` // "cheap", "io-bound", "network"
	Deprecated  string   `json:"deprecated,omitempty"`
}

// Specer is an optional interface that Tools may implement to advertise a
// rich ToolSpec. Tools that don't implement it get a synthetic spec derived
// from Name()/Description() with Risk=RiskRead.
type Specer interface {
	Spec() ToolSpec
}

// JSONSchema returns a JSON-Schema object for this spec's arguments. Provider
// serializers use this as-is (Anthropic tool_use.input_schema, OpenAI
// tool.parameters).
func (s ToolSpec) JSONSchema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, arg := range s.Args {
		properties[arg.Name] = argToSchema(arg)
		if arg.Required {
			required = append(required, arg.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func argToSchema(a Arg) map[string]any {
	m := map[string]any{"type": string(a.Type)}
	if a.Description != "" {
		m["description"] = a.Description
	}
	if a.Default != nil {
		m["default"] = a.Default
	}
	if len(a.Enum) > 0 {
		m["enum"] = a.Enum
	}
	if a.Items != nil && a.Type == ArgArray {
		m["items"] = argToSchema(*a.Items)
	}
	if a.MinValue != nil {
		m["minimum"] = *a.MinValue
	}
	if a.MaxValue != nil {
		m["maximum"] = *a.MaxValue
	}
	return m
}

// ShortHelp renders a 1-2 line description suitable for tool_search results.
// Format: "name — summary [risk]".
func (s ToolSpec) ShortHelp() string {
	summary := strings.TrimSpace(s.Summary)
	if summary == "" {
		summary = strings.TrimSpace(s.Purpose)
	}
	if summary == "" {
		summary = "(no description)"
	}
	return s.Name + " — " + summary + " [" + string(s.Risk) + "]"
}

// LongHelp renders a full tool_help response with args, returns, examples.
func (s ToolSpec) LongHelp() string {
	var b strings.Builder
	b.WriteString(s.Name)
	if s.Title != "" {
		b.WriteString(" (" + s.Title + ")")
	}
	b.WriteString("\nRisk: " + string(s.Risk))
	if s.Idempotent {
		b.WriteString(" (idempotent)")
	}
	if s.CostHint != "" {
		b.WriteString("  Cost: " + s.CostHint)
	}
	b.WriteString("\n")
	if s.Summary != "" {
		b.WriteString("\nSummary: " + s.Summary + "\n")
	}
	if s.Purpose != "" {
		b.WriteString("\nPurpose: " + s.Purpose + "\n")
	}
	if strings.TrimSpace(s.Prompt) != "" {
		b.WriteString("\nGuide:\n" + strings.TrimSpace(s.Prompt) + "\n")
	}
	if len(s.Args) > 0 {
		b.WriteString("\nArgs:\n")
		for _, a := range s.Args {
			flag := ""
			if a.Required {
				flag = " (required)"
			}
			b.WriteString("  - " + a.Name + " <" + string(a.Type) + ">" + flag)
			if a.Description != "" {
				b.WriteString(": " + a.Description)
			}
			if a.Default != nil {
				def, _ := json.Marshal(a.Default)
				b.WriteString("  default=" + string(def))
			}
			if len(a.Enum) > 0 {
				en, _ := json.Marshal(a.Enum)
				b.WriteString("  enum=" + string(en))
			}
			b.WriteString("\n")
		}
	}
	if s.Returns != "" {
		b.WriteString("\nReturns: " + s.Returns + "\n")
	}
	if len(s.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range s.Examples {
			b.WriteString("  " + ex + "\n")
		}
	}
	if len(s.Tags) > 0 {
		b.WriteString("\nTags: " + strings.Join(s.Tags, ", ") + "\n")
	}
	if s.Deprecated != "" {
		b.WriteString("\nDEPRECATED: " + s.Deprecated + "\n")
	}
	return b.String()
}

// ScoreMatch ranks a spec against a search query. Higher is better; 0 means
// no match. Scoring is deterministic and cheap — no index, just substring +
// tag + word matches weighted by field.
//
//	name exact      : 100
//	name prefix     : 50
//	name substring  : 25
//	tag exact       : 20 per tag
//	summary token   : 5 per token match
//	purpose token   : 2 per token match
func ScoreMatch(spec ToolSpec, query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 1 // empty query matches everything weakly (stable sort by name)
	}
	qTokens := tokenize(q)
	name := strings.ToLower(spec.Name)
	score := 0
	switch {
	case name == q:
		score += 100
	case strings.HasPrefix(name, q):
		score += 50
	case strings.Contains(name, q):
		score += 25
	}
	for _, tag := range spec.Tags {
		t := strings.ToLower(tag)
		if t == q {
			score += 20
			continue
		}
		for _, qt := range qTokens {
			if t == qt {
				score += 15
			} else if strings.Contains(t, qt) {
				score += 5
			}
		}
	}
	score += tokenOverlap(strings.ToLower(spec.Summary), qTokens) * 5
	score += tokenOverlap(strings.ToLower(spec.Purpose), qTokens) * 2
	return score
}

func tokenize(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ',', '.', ';', ':', '/', '\\', '-', '_':
			return true
		}
		return false
	})
	out := fields[:0]
	for _, f := range fields {
		t := strings.TrimSpace(f)
		if len(t) >= 2 {
			out = append(out, t)
		}
	}
	return out
}

func tokenOverlap(text string, qTokens []string) int {
	if text == "" || len(qTokens) == 0 {
		return 0
	}
	textTokens := tokenize(text)
	set := make(map[string]struct{}, len(textTokens))
	for _, t := range textTokens {
		set[t] = struct{}{}
	}
	hits := 0
	for _, qt := range qTokens {
		if _, ok := set[qt]; ok {
			hits++
		}
	}
	return hits
}

// SortSpecs sorts a []ToolSpec alphabetically by Name. Stable ordering helps
// list rendering and snapshot tests.
func SortSpecs(specs []ToolSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
}
