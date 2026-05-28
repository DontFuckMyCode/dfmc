package tui

// render_panels_help.go — the ctrl+h / F11 help overlay and its
// per-tab quick-hint catalog. Sibling of render_panels.go which
// keeps the per-panel render delegators (renderStatusView /
// renderFilesView / renderToolsView), the footer assembly
// (renderFooter + footerSegments + footerRuntimeSegment), and the
// workbenchRuntimeStatus one-liner used by the web cockpit.
//
// Splitting the help overlay out keeps render_panels.go scoped to
// "what does the always-visible chrome look like" while this file
// owns "what does the press-? overlay show" — the global keyboard
// reference, the per-tab quick-hint switch, and the truncate-to-
// width formatting. Pure data + pure render; no mutation.

import (
	"strings"
)

func (m Model) renderHelpOverlay(width int) string {
	if width < 40 {
		width = 40
	}
	tab := m.tabs[m.activeTab]
	titleSuffix := "  ctrl+h to close"
	// Phase K item 3: while the help overlay is open, the chat composer
	// input doubles as a live filter. Anything the user types narrows
	// the visible lines to those matching the query (case-insensitive,
	// substring). Headers always render so the filtered view keeps its
	// section markers; section bodies drop below their header when no
	// child line matches.
	query := ""
	if m.ui.showHelpOverlay {
		query = strings.ToLower(strings.TrimSpace(m.chat.input))
	}
	if query != "" {
		titleSuffix = "  filter: " + query + "  ·  esc to clear"
	}
	header := titleStyle.Render(" Keys ") + subtleStyle.Render(titleSuffix)
	sections := helpOverlaySections(tab)
	out := []string{header, ""}
	matchCount := 0
	for _, sec := range sections {
		secLines := []string{boldStyle.Render(sec.Title)}
		matched := false
		for _, body := range sec.Body {
			if query == "" || strings.Contains(strings.ToLower(body), query) {
				secLines = append(secLines, "  "+body)
				if query != "" {
					matched = true
					matchCount++
				}
			}
		}
		// When filtering, drop sections whose body produced no matches.
		// Without filtering, every section renders so the user gets the
		// full taxonomy.
		if query == "" || matched {
			out = append(out, secLines...)
			out = append(out, "")
		}
	}
	if query != "" && matchCount == 0 {
		out = append(out, subtleStyle.Render("  no help lines match "+query+" — clear the composer or hit esc to dismiss"))
	}
	rendered := make([]string, 0, len(out))
	for _, ln := range out {
		rendered = append(rendered, truncateSingleLine(ln, width))
	}
	return strings.Join(rendered, "\n")
}

// helpOverlaySection groups a header line with its body lines so the
// filter pass can keep / drop sections atomically. The catalog mirrors
// what the pre-Phase-K hard-coded slice rendered.
type helpOverlaySection struct {
	Title string
	Body  []string
}

func helpOverlaySections(tab string) []helpOverlaySection {
	return []helpOverlaySection{
		{
			Title: tab + " tab",
			Body:  helpOverlayTabHints(tab),
		},
		{
			Title: "Global · 8 first-class tabs (F1..F8 step in strip order)",
			Body: []string{
				"f1/alt+1 chat · f2/alt+2 files · f3/alt+3 patch · f4/alt+4 workflow",
				"f5/alt+5 activity · f6/alt+6 memory · f7/alt+7 conversations · f8/alt+8 providers",
				"tab / shift+tab cycle next/prev · ctrl+o also opens providers (legacy)",
			},
		},
		{
			Title: "Global · demoted panels (open as overlays — esc/q closes)",
			Body: []string{
				"f9 status · f10 codemap · f11 tools · f12 security",
				"shift+f1 prompts · shift+f2 plans · shift+f3 context · shift+f4 orchestrate · shift+f5 shortcuts",
				"shift+f6 contexts · shift+f7 provider log · shift+f8 telegram · ctrl+shift+t tool status",
			},
		},
		{
			Title: "Global · pickers, palettes, stats",
			Body: []string{
				"ctrl+p palette · ctrl+s stats · ctrl+h/alt+h help · /model changes model",
				"chat stats: alt+a overview · alt+s todos · alt+d tasks · alt+f agents · alt+p providers",
				"ctrl+c/ctrl+q quit · ctrl+u clear chat input · esc cancels streaming turn (or closes overlay / parked banner)",
				"this overlay filters live — type in the composer to narrow the visible lines, ctrl+u or esc clears",
			},
		},
		{
			Title: "Chat composer",
			Body: []string{
				"↑/↓ history · tab accept suggestion · @ mention file · / browse commands",
				"@file:10-50 or @file#L10-L50 attaches a line range to the mention",
				"ctrl+←/→ jump word · ctrl+w kill word · ctrl+k kill to end · ctrl+u clear line",
				"ctrl+a/ctrl+e line home/end · home/end same · backspace deletes char",
				"ctrl+t or /file open file picker (alias for @, useful on AltGr layouts)",
				"/continue resumes a parked agent loop · /btw queues a note",
				"/clear wipes transcript · /quit exits · /coach mutes notes · /hints toggles trajectory",
				"/plan enters investigate-only mode · /code exits and re-enables mutations",
				"/retry resends last user msg · /edit pulls last msg back to the composer",
			},
		},
	}
}

func helpOverlayTabHints(tab string) []string {
	switch strings.TrimSpace(strings.ToLower(tab)) {
	case "chat":
		return []string{
			"enter send · alt+enter newline · / commands · @ mention",
			"wheel · shift+↑/↓ · pgup/pgdn scroll transcript",
			"ctrl+h help · ctrl+p palette · ctrl+s stats",
			"when parked: ctrl+x resumes · esc dismisses · type a note first to steer",
		}
	case "status":
		return []string{
			"←→ move between cards · ↑↓ row jump · home end top/bottom",
			"enter jumps to detail tab · / search · esc back",
		}
	case "files":
		return []string{
			"↑↓ move · enter action menu · esc back",
			"menu: ↑↓ pick · enter run",
		}
	case "patch":
		return []string{
			"↑↓ hunk · ←→ file · enter action menu · esc back",
			"menu: apply · check · undo · focus · reload",
		}
	case "workflow":
		return []string{
			"↑↓ move · enter select · space follow · → menu · esc back",
			"menu: stop · resume · copy ID · routing editor · refresh",
		}
	case "tools":
		return []string{
			"↑↓ select · enter runs · → menu · esc back",
			"menu: run · edit params · reset · rerun",
		}
	case "activity":
		return []string{
			"↑↓ scroll · pgup/pgdn page · enter open · → menu · esc back",
			"menu: pause/resume · filter · search · clear · open/focus/copy",
		}
	case "memory":
		return []string{
			"↑↓ scroll · enter expand · → menu · esc back",
			"menu: cycle tier · refresh · search · clear",
		}
	case "codemap":
		return []string{
			"↑↓ scroll · enter/→ menu · esc back",
			"menu: cycle view · refresh · top/bottom",
		}
	case "conversations":
		return []string{
			"↑↓ scroll · enter preview · → menu · esc back",
			"menu: resume · search · clear",
		}
	case "prompts":
		return []string{
			"↑↓ scroll · enter preview · → menu · esc back",
			"menu: refresh · search · clear",
		}
	case "security":
		return []string{
			"↑↓ scroll · enter/→ menu · esc back",
			"menu: toggle view · rescan · search · clear",
		}
	case "plans":
		return []string{
			"↑↓ scroll · enter re-run · → menu · esc back",
			"menu: edit · run · clear",
		}
	case "context":
		return []string{
			"↑↓ scroll · enter preview · → menu · esc back",
			"menu: edit · preview · active · clear",
		}
	case "providers":
		return []string{
			"↑↓ scroll · enter/→ menu · esc back",
			"menu: detail · primary · fallback · cycle model · save · new · refresh · search",
		}
	case "orchestrate":
		return []string{
			"shift+f4 jumps here · live hierarchy of agents/subagents/todos/drive/tokens",
			"↑↓ section cursor · → / enter action menu · j/k/pgup/pgdn page · esc close",
			"menu per section: open activity/workflow/context/status/providerlog · stop active drive run",
		}
	case "shortcuts":
		return []string{
			"shift+f5 jumps here · /shortcuts and /keys open help",
			"↑↓ scroll · pgup/pgdn page · esc close",
		}
	case "contexts":
		return []string{
			"shift+f6 jumps here · live snapshot of every concurrently-active agent context",
			"↑↓ section cursor (main / parked / sub-agents / drive) · → / enter action menu",
			"menu: continue or discard parked agent · stop active drive run · jump to detail tab",
			"j/k/pgup/pgdn scroll · esc closes",
		}
	default:
		return []string{"f1 chat · f2 files · f3 patch · f4 workflow · f5 activity · f6 memory · f7 conversations · f8 providers · f9 status · f10 codemap · f11 tools · f12 security · shift+f1..f5 prompts/plans/context/orchestrate/shortcuts · ctrl+h help · ctrl+q quit"}
	}
}
