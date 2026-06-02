package tokens

import "testing"

// TestDetectFamily_Table pins the model-name -> tokenization-family
// routing, including the order-sensitive cases that would silently
// regress if the branch order in detectFamilyImpl changed (e.g. gpt-4o
// must be matched as o200k BEFORE the gpt-4 cl100k branch, and claude +
// sonnet must resolve to the sonnet family before the generic claude one).
func TestDetectFamily_Table(t *testing.T) {
	cases := []struct {
		model string
		want  ModelFamily
	}{
		// Empty / whitespace -> default (the early-out in DetectFamily).
		{"", Familydefault},
		{"   ", Familydefault},
		// Anthropic: sonnet is more specific than the generic claude branch.
		{"claude-3-5-sonnet-20241022", Familysonnet},
		{"claude-sonnet-4-6", Familysonnet},
		{"claude-3-opus", Familyclaude},
		{"claude-opus-4-8", Familyclaude},
		{"CLAUDE-3-HAIKU", Familyclaude}, // case-insensitive
		// Google.
		{"gemini-1.5-pro", Familygemini},
		{"models/gemini-2.0-flash", Familygemini},
		// OpenAI o-series -> o200k.
		{"o1-preview", Familyo200k},
		{"o3-mini", Familyo200k},
		// gpt-4o -> o200k, and it must win over the gpt-4 cl100k branch.
		{"gpt-4o", Familyo200k},
		{"gpt-4o-mini", Familyo200k},
		{"gpt4o", Familyo200k},
		// gpt-4 / gpt-3.5 -> cl100k.
		{"gpt-4-turbo", Familycl100k},
		{"gpt4", Familycl100k},
		{"gpt-3.5-turbo", Familycl100k},
		{"gpt35-turbo", Familycl100k},
		// OpenAI-compatible families routed to cl100k.
		{"deepseek-chat", Familycl100k},
		{"kimi-k2", Familycl100k},
		{"moonshot-v1-8k", Familycl100k},
		{"mistral-large", Familycl100k},
		{"llama-3.1-70b", Familycl100k},
		{"qwen2.5-coder", Familycl100k},
		// Truly unknown -> default heuristic family.
		{"some-random-model", Familydefault},
	}
	for _, tc := range cases {
		if got := DetectFamily(tc.model); got != tc.want {
			t.Errorf("DetectFamily(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

// TestDetectFamily_CachesStableResult exercises the familyCache fast path:
// a second lookup must return the same family as the first.
func TestDetectFamily_CachesStableResult(t *testing.T) {
	const model = "gpt-4o-2024-08-06"
	first := DetectFamily(model)
	second := DetectFamily(model) // served from familyCache
	if first != second {
		t.Fatalf("cached lookup diverged: first=%q second=%q", first, second)
	}
	if first != Familyo200k {
		t.Fatalf("expected o200k for %q, got %q", model, first)
	}
}

// TestEncoderName maps the two tiktoken-backed families to their encoding
// names and returns "" for every heuristic family (claude/gemini/default),
// which is the signal CounterForFamily uses to pick the heuristic path.
func TestEncoderName(t *testing.T) {
	if got := EncoderName(Familycl100k); got != "cl100k_base" {
		t.Errorf("cl100k encoder = %q, want cl100k_base", got)
	}
	if got := EncoderName(Familyo200k); got != "o200k_base" {
		t.Errorf("o200k encoder = %q, want o200k_base", got)
	}
	for _, f := range []ModelFamily{Familyclaude, Familysonnet, Familygemini, Familydefault, FamilyUnknown} {
		if got := EncoderName(f); got != "" {
			t.Errorf("EncoderName(%q) = %q, want empty (heuristic family)", f, got)
		}
	}
}

// TestHeuristicForFamily pins the per-family char/token ratios. claude and
// sonnet use chars/3+1, gemini uses chars/4, and any other family defers
// to the calibrated HeuristicCounter (asserted only as positive here since
// its exact value is covered by counter_test.go).
func TestHeuristicForFamily(t *testing.T) {
	const text = "hello world" // 11 bytes
	if got := heuristicForFamily(Familyclaude, text); got != 11/3+1 {
		t.Errorf("claude heuristic = %d, want %d", got, 11/3+1)
	}
	if got := heuristicForFamily(Familysonnet, text); got != 11/3+1 {
		t.Errorf("sonnet heuristic = %d, want %d", got, 11/3+1)
	}
	if got := heuristicForFamily(Familygemini, text); got != 11/4 {
		t.Errorf("gemini heuristic = %d, want %d", got, 11/4)
	}
	if got := heuristicForFamily(Familydefault, text); got <= 0 {
		t.Errorf("default heuristic = %d, want positive", got)
	}
	// Empty text is always zero tokens regardless of family.
	if got := heuristicForFamily(Familyclaude, ""); got != 0 {
		t.Errorf("empty text heuristic = %d, want 0", got)
	}
}

// TestCounterForFamily_Tiktoken is the direct regression guard for the
// EncodingForModel/GetEncoding bug: the cl100k and o200k families MUST
// resolve to a real tiktoken counter. Before the fix NewTiktokenCounter
// was handed an encoding name through EncodingForModel (which only accepts
// model names), so this returned (nil, false) and every OpenAI model
// silently degraded to the heuristic.
func TestCounterForFamily_Tiktoken(t *testing.T) {
	for _, f := range []ModelFamily{Familycl100k, Familyo200k} {
		c, ok := CounterForFamily(f)
		if !ok || c == nil {
			t.Fatalf("CounterForFamily(%q) = (%v, %v); want a real tiktoken counter", f, c, ok)
		}
	}
	// Heuristic families must report no dedicated counter.
	for _, f := range []ModelFamily{Familyclaude, Familygemini, Familydefault} {
		if c, ok := CounterForFamily(f); ok || c != nil {
			t.Fatalf("CounterForFamily(%q) = (%v, %v); want (nil, false)", f, c, ok)
		}
	}
}

// TestCountForModel_UsesTiktokenForOpenAI proves the integrated path:
// gpt-4 / gpt-4o now count via tiktoken (the canonical "hello world" is 2
// tokens in both cl100k and o200k), while a Claude model takes the
// chars/3+1 heuristic. Before the fix gpt-4 would have fallen through to
// the heuristic too.
func TestCountForModel_UsesTiktokenForOpenAI(t *testing.T) {
	if got := CountForModel("gpt-4", "hello world"); got != 2 {
		t.Errorf("CountForModel(gpt-4, 'hello world') = %d, want 2 (tiktoken cl100k)", got)
	}
	if got := CountForModel("gpt-4o", "hello world"); got != 2 {
		t.Errorf("CountForModel(gpt-4o, 'hello world') = %d, want 2 (tiktoken o200k)", got)
	}
	if got := CountForModel("claude-3-opus", "hello world"); got != 11/3+1 {
		t.Errorf("CountForModel(claude, 'hello world') = %d, want %d (heuristic)", got, 11/3+1)
	}
	if got := CountForModel("gpt-4", ""); got != 0 {
		t.Errorf("empty text should be 0 tokens, got %d", got)
	}
}
