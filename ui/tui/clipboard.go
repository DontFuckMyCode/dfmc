// Clipboard helper for the TUI.
//
// We speak OSC 52 (ansi.SetClipboard) so copy-to-clipboard works over
// SSH, in tmux, and across every terminal that honors the protocol —
// no xclip / pbcopy / wl-copy shell-out dance. Bubbletea lets us print
// an arbitrary sequence via tea.Println; that returns a Cmd which the
// runtime writes outside the alt-screen, so the terminal sees the
// escape before the next render hits it.
//
// Callers pass raw text; we base64-encode and clamp to OSC 52's ~100k
// practical limit (tmux's default is lower but bubbletea's transport
// doesn't chunk for us). Anything bigger would almost certainly be a
// bug in the caller; we truncate with a visible marker instead of
// dropping the clipboard op silently.

package tui

import (
	"encoding/base64"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// clipboardMaxBytes caps the payload we will ship over OSC 52. Many
// terminals truncate beyond ~100KB; some tmux setups block anything
// above ~32KB without `set -g set-clipboard on`. Pick a safe middle
// and note the truncation in the notice so the user knows why the
// paste looks shorter than expected.
const clipboardMaxBytes = 64 * 1024

// clipboardResult reports whether the copy succeeded and how many
// bytes it shipped. truncated=true means the original payload was
// larger than clipboardMaxBytes and the clipboard now holds a head
// slice only.
type clipboardResult struct {
	Bytes     int
	Truncated bool
	Err       error
}

// copyToClipboardCmd returns a bubbletea Cmd that emits an OSC 52
// sequence writing data to the terminal's system clipboard. The Cmd
// emits no message — the TUI caller updates its own state
// synchronously (e.g. m.notice = "copied N bytes") before returning
// the Cmd, because OSC 52 has no acknowledgement and there's no
// useful post-copy event to wait on.
func copyToClipboardCmd(data string) (tea.Cmd, clipboardResult) {
	data = strings.TrimRight(data, "\n") // the user rarely wants a trailing newline in their paste
	res := clipboardResult{Bytes: len(data)}
	if data == "" {
		// Empty payloads still reset the clipboard in spec; we'd rather
		// keep the previous contents and tell the caller nothing was done.
		return nil, res
	}
	if len(data) > clipboardMaxBytes {
		data = data[:clipboardMaxBytes]
		res.Bytes = clipboardMaxBytes
		res.Truncated = true
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(data))
	seq := ansi.SetClipboard(ansi.SystemClipboard, encoded)
	// tea.Printf pushes the sequence outside the alt-screen render
	// cycle so the terminal parses the OSC reliably.
	return tea.Printf("%s", seq), res
}
