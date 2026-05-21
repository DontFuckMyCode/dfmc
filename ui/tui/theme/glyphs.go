package theme

import "os"

// GlyphSet provides Unicode and ASCII alternatives for TUI visual elements.
// Set DFMC_ASCII_UI=1 to use ASCII fallbacks (useful on Windows terminals
// with encoding issues or when emoji/box-drawing characters render as
// mojibake).
type GlyphSet struct {
	Bullet      string // list item marker
	ArrowRight  string // direction / action indicator
	ArrowDown   string // expanded / open
	ArrowUp     string // collapsed / closed
	Check       string // success / done
	Cross       string // failure / error
	Warning     string // warning / caution
	Info        string // info / note
	Star        string // favorite / important
	Dot         string // bullet point
	Ellipsis    string // truncated content
	Pipe        string // vertical separator
	Dash        string // horizontal separator
	CornerTL    string // top-left box corner
	CornerTR    string // top-right box corner
	CornerBL    string // bottom-left box corner
	CornerBR    string // bottom-right box corner
	Horizontal  string // horizontal line
	Vertical    string // vertical line
	TeeRight    string // T-junction right
	TeeLeft     string // T-junction left
	TeeDown     string // T-junction down
	TeeUp       string // T-junction up
	CrossJunc   string // cross junction
	Cursor      string // selection cursor
	Pointer     string // mouse/pointer indicator
	Play        string // running / active
	Stop        string // stopped / inactive
	Pause       string // paused
	Spinner     string // loading / working
}

var (
	// Unicode glyphs (default, rich terminal experience)
	UnicodeGlyphs = GlyphSet{
		Bullet:     "•",
		ArrowRight: "→",
		ArrowDown:  "▼",
		ArrowUp:    "▲",
		Check:      "✓",
		Cross:      "✗",
		Warning:    "⚠",
		Info:       "ℹ",
		Star:       "★",
		Dot:        "●",
		Ellipsis:   "…",
		Pipe:       "│",
		Dash:       "─",
		CornerTL:   "┌",
		CornerTR:   "┐",
		CornerBL:   "└",
		CornerBR:   "┘",
		Horizontal: "─",
		Vertical:   "│",
		TeeRight:   "├",
		TeeLeft:    "┤",
		TeeDown:    "┬",
		TeeUp:      "┴",
		CrossJunc:  "┼",
		Cursor:     ">",
		Pointer:    "▸",
		Play:       "▶",
		Stop:       "■",
		Pause:      "⏸",
		Spinner:    "◐",
	}

	// ASCII glyphs (fallback for problematic terminals)
	ASCIIGlyphs = GlyphSet{
		Bullet:     "*",
		ArrowRight: ">",
		ArrowDown:  "v",
		ArrowUp:    "^",
		Check:      "[OK]",
		Cross:      "[X]",
		Warning:    "[!]",
		Info:       "[i]",
		Star:       "*",
		Dot:        "*",
		Ellipsis:   "...",
		Pipe:       "|",
		Dash:       "-",
		CornerTL:   "+",
		CornerTR:   "+",
		CornerBL:   "+",
		CornerBR:   "+",
		Horizontal: "-",
		Vertical:   "|",
		TeeRight:   "+",
		TeeLeft:    "+",
		TeeDown:    "+",
		TeeUp:      "+",
		CrossJunc:  "+",
		Cursor:     ">",
		Pointer:    ">",
		Play:       ">",
		Stop:       "#",
		Pause:      "||",
		Spinner:    "~",
	}

	// Active glyph set, initialized at startup based on environment
	Glyphs = defaultGlyphs()
)

func defaultGlyphs() GlyphSet {
	if os.Getenv("DFMC_ASCII_UI") == "1" {
		return ASCIIGlyphs
	}
	return UnicodeGlyphs
}

// UseASCII forces the ASCII glyph set for the remainder of the process.
func UseASCII() {
	Glyphs = ASCIIGlyphs
}

// UseUnicode forces the Unicode glyph set for the remainder of the process.
func UseUnicode() {
	Glyphs = UnicodeGlyphs
}
