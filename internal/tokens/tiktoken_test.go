package tokens

import (
	"strings"
	"testing"
)

// TestNewTiktokenCounter_ValidEncodings loads the two encodings DFMC
// actually uses by their ENCODING name. This is the unit-level guard for
// the GetEncoding-vs-EncodingForModel fix: NewTiktokenCounter must accept
// "cl100k_base" / "o200k_base" (encoding names), which EncodingForModel
// rejected with "no encoding for model cl100k_base".
func TestNewTiktokenCounter_ValidEncodings(t *testing.T) {
	for _, enc := range []string{"cl100k_base", "o200k_base"} {
		c, err := NewTiktokenCounter(enc)
		if err != nil {
			t.Fatalf("NewTiktokenCounter(%q) failed: %v", enc, err)
		}
		if c == nil {
			t.Fatalf("NewTiktokenCounter(%q) returned nil counter", enc)
		}
	}
}

// TestNewTiktokenCounter_InvalidEncoding surfaces a clear error rather than
// a nil-pointer counter when the encoding name is unknown.
func TestNewTiktokenCounter_InvalidEncoding(t *testing.T) {
	c, err := NewTiktokenCounter("definitely_not_an_encoding")
	if err == nil {
		t.Fatalf("expected error for unknown encoding, got counter %v", c)
	}
	if c != nil {
		t.Fatalf("expected nil counter on error, got %v", c)
	}
}

// TestTiktokenCounter_KnownCounts pins a couple of canonical BPE token
// counts so a future dependency bump that changes tokenization is caught.
// "hello world" is 2 cl100k tokens; the empty/whitespace string is 0.
func TestTiktokenCounter_KnownCounts(t *testing.T) {
	c, err := NewTiktokenCounter("cl100k_base")
	if err != nil {
		t.Fatalf("load cl100k: %v", err)
	}
	if got := c.Count("hello world"); got != 2 {
		t.Errorf("Count('hello world') = %d, want 2", got)
	}
	if got := c.Count(""); got != 0 {
		t.Errorf("Count('') = %d, want 0", got)
	}
	if got := c.Count("   \n\t  "); got != 0 {
		t.Errorf("Count(whitespace) = %d, want 0", got)
	}
	// A longer string must produce more tokens than a short one.
	short := c.Count("cat")
	long := c.Count("the quick brown fox jumps over the lazy dog")
	if long <= short {
		t.Errorf("expected longer text to have more tokens: short=%d long=%d", short, long)
	}
}

// TestTiktokenCounter_CountMessages sums per-message content plus the
// configured overhead, so a multi-message sequence costs strictly more
// than the bare concatenation of its contents.
func TestTiktokenCounter_CountMessages(t *testing.T) {
	c, err := NewTiktokenCounter("cl100k_base")
	if err != nil {
		t.Fatalf("load cl100k: %v", err)
	}
	msgs := []Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there friend"},
	}
	total := c.CountMessages(msgs)
	rawContent := c.Count("hello world") + c.Count("hi there friend")
	if total <= rawContent {
		t.Fatalf("CountMessages should add per-message overhead: total=%d raw=%d", total, rawContent)
	}
	if total <= 0 {
		t.Fatalf("expected positive total, got %d", total)
	}
}

// TestTiktokenCounter_Deterministic guards against any hidden state in the
// lazily-initialised encoder: repeated counts of the same input match.
func TestTiktokenCounter_Deterministic(t *testing.T) {
	c, err := NewTiktokenCounter("o200k_base")
	if err != nil {
		t.Fatalf("load o200k: %v", err)
	}
	text := strings.Repeat("token ", 200)
	a, b := c.Count(text), c.Count(text)
	if a != b {
		t.Fatalf("non-deterministic count: %d vs %d", a, b)
	}
}
