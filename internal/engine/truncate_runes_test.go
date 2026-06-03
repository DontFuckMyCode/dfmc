package engine

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestEngineTruncators_RuneSafe pins that the engine-side truncators slice by
// runes, not bytes. truncateString clips subagent-journal Task/Summary that are
// PERSISTED to bbolt and later injected into LLM context; todoSnippet clips
// TODO text shown in the TUI strip. Both routinely carry Turkish (2-byte runes
// like ş/ö/ç), where a byte cut splits a rune and corrupts the text.
func TestEngineTruncators_RuneSafe(t *testing.T) {
	s := strings.Repeat("ş", 10) // 20 bytes, 10 runes — any odd byte cut splits a rune
	for n := 1; n < 10; n++ {
		if got := truncateString(s, n); !utf8.ValidString(got) {
			t.Errorf("truncateString(%q, %d) invalid UTF-8: %q", s, n, got)
		}
	}
	task := "Görev: kullanıcı oturumunu şifrele ve çıkışta temizle"
	for _, n := range []int{5, 11, 20, 33} {
		if got := truncateString(task, n); !utf8.ValidString(got) {
			t.Errorf("truncateString(task, %d) invalid UTF-8: %q", n, got)
		}
	}

	todo := strings.Repeat("Görev ", 50)
	for _, max := range []int{3, 7, 15, 100} {
		if got := todoSnippet(todo, max); !utf8.ValidString(got) {
			t.Errorf("todoSnippet(todo, %d) invalid UTF-8: %q", max, got)
		}
	}
}
