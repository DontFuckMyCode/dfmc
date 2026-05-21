package theme

import (
	"os"
	"testing"
)

func TestDefaultGlyphs(t *testing.T) {
	// Default should be Unicode
	g := defaultGlyphs()
	if g.Bullet != "•" {
		t.Errorf("expected Unicode bullet '•', got %q", g.Bullet)
	}
	if g.ArrowRight != "→" {
		t.Errorf("expected Unicode arrow '→', got %q", g.ArrowRight)
	}
	if g.Check != "✓" {
		t.Errorf("expected Unicode check '✓', got %q", g.Check)
	}
}

func TestASCIIGlyphs(t *testing.T) {
	g := ASCIIGlyphs
	if g.Bullet != "*" {
		t.Errorf("expected ASCII bullet '*', got %q", g.Bullet)
	}
	if g.ArrowRight != ">" {
		t.Errorf("expected ASCII arrow '>', got %q", g.ArrowRight)
	}
	if g.Check != "[OK]" {
		t.Errorf("expected ASCII check '[OK]', got %q", g.Check)
	}
	if g.CornerTL != "+" {
		t.Errorf("expected ASCII corner '+', got %q", g.CornerTL)
	}
}

func TestUseASCII(t *testing.T) {
	old := Glyphs
	defer func() { Glyphs = old }()

	UseASCII()
	if Glyphs.Bullet != "*" {
		t.Errorf("UseASCII failed: expected '*', got %q", Glyphs.Bullet)
	}
}

func TestUseUnicode(t *testing.T) {
	old := Glyphs
	defer func() { Glyphs = old }()

	UseUnicode()
	if Glyphs.Bullet != "•" {
		t.Errorf("UseUnicode failed: expected '•', got %q", Glyphs.Bullet)
	}
}

func TestDefaultGlyphsWithEnv(t *testing.T) {
	old := os.Getenv("DFMC_ASCII_UI")
	defer os.Setenv("DFMC_ASCII_UI", old)

	os.Setenv("DFMC_ASCII_UI", "1")
	g := defaultGlyphs()
	if g.Bullet != "*" {
		t.Errorf("expected ASCII bullet with env, got %q", g.Bullet)
	}

	os.Setenv("DFMC_ASCII_UI", "")
	g = defaultGlyphs()
	if g.Bullet != "•" {
		t.Errorf("expected Unicode bullet without env, got %q", g.Bullet)
	}
}
