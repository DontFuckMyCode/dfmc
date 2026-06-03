package theme

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzRenderMarkdownBlocks drives the block-level markdown renderer over
// arbitrary (text, width). RenderMarkdownBlocks renders every assistant
// message in the chat view, so it parses fully untrusted model output —
// tables, code fences, headings, bullets, blockquotes, inline emphasis and
// links — and the table path does non-trivial column-width arithmetic
// (markdownTableColumnWidths scaling, forceHardWrap, normalizeTableRow). A
// panic here crashes the chat render. Width is deliberately fuzzed including
// 0/negative/tiny values, since it is a live terminal dimension.
//
// Contract: never panic; and a valid-UTF-8 input never produces an
// invalid-UTF-8 output line (the wrap/truncate/cell-split logic must respect
// rune boundaries — model output is routinely multibyte Turkish/CJK).
func FuzzRenderMarkdownBlocks(f *testing.F) {
	seeds := []struct {
		text  string
		width int
	}{
		{"# Başlık\n\nnormal **kalın** ve `kod` satırı", 80},
		{"| a | b |\n|---|---|\n| 1 | 2 |", 40},
		{"| çok | uzun İçerik | hücre |\n|---|---|---|\n| şşş | ğğğ | ııı |", 12}, // narrow Turkish table
		{"- bir\n- iki\n  - iç madde\n1. numaralı\n2. ikinci", 30},
		{"> alıntı satırı burada\n\n```go\nfunc f() {}\n```", 20},
		{"[etiket](https://örnek.test/yol) ve düz https://a.test/b", 60},
		{"日本語 **bold** | 表 | 格 |\n|---|---|\n| 一 | 二 |", 8},
		{"🚀 emoji **vurgu** `kod`", 5},
		{"", 0},
		{"|||||\n|---|---|---|---|---|\n|||||", 1}, // degenerate table, width 1
		{"```\nunclosed fence\nstill open", -5},    // negative width, open fence
		{strings.Repeat("# ", 50), 3},              // many empty headings, tiny width
		{"no markdown at all just prose", 0},
	}
	for _, s := range seeds {
		f.Add(s.text, s.width)
	}

	f.Fuzz(func(t *testing.T, text string, width int) {
		lines := RenderMarkdownBlocks(text, width) // must never panic

		if utf8.ValidString(text) {
			for _, ln := range lines {
				if !utf8.ValidString(ln) {
					t.Fatalf("valid input produced invalid-UTF-8 output line\n text=%q width=%d\n line=%q", text, width, ln)
				}
			}
		}
	})
}
