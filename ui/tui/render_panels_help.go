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
				"legacy aliases still work: alt+i tools · ctrl+y plans · ctrl+w context · alt+r orchestrate",
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
			"enter send · alt+enter newline · ctrl+x send/queue · / commands · @ mention",
			"wheel · shift+↑/↓ · pgup/pgdn scroll transcript",
			"/model changes model · alt+p opens compact runtime status",
			"alt+a overview · alt+s todos · alt+d tasks · alt+f subagents · alt+p providers in the right stats panel",
			"when parked: ctrl+x resumes · esc dismisses · type a note first to steer",
		}
	case "status":
		return []string{
			"← → / h l move between cards · ↑ ↓ / j k row jump · home/g end/G",
			"enter jumps to detail tab · r refresh",
		}
	case "files":
		return []string{
			"↑↓ move · enter / → opens action menu · esc closes",
			"menu: ↑↓ pick · enter run · letters in [brackets] are accelerators",
		}
	case "patch":
		return []string{
			"↑↓ next/prev hunk · n/b next/prev file · enter / → action menu",
			"menu: apply · check · undo · focus · reload — accelerators: a/c/u/f/d",
		}
	case "workflow":
		return []string{
			"↑↓ move · enter select run / expand TODO · → action menu · esc back",
			"menu: stop · resume · copy ID into chat · open routing editor · refresh",
		}
	case "tools":
		return []string{
			"↑↓ select · enter runs current · → opens action menu",
			"menu: run · edit params · reset · rerun — banner shows EDITING when active",
		}
	case "activity":
		return []string{
			"↑↓ scroll · pgup/pgdn page · g/G top/tail · → action menu",
			"menu: pause/resume · cycle filter · 1-6 filters · search · clear · open / focus / copy",
		}
	case "memory":
		return []string{
			"↑↓ scroll · enter / → opens action menu (cycle tier · refresh · search · clear)",
			"single-letter accelerators (t/r//c) still work for power users",
		}
	case "codemap":
		return []string{
			"↑↓ scroll · enter / → opens action menu (cycle view · refresh · top/bottom)",
			"accelerators: v/r/g/G",
		}
	case "conversations":
		return []string{
			"↑↓ scroll · enter previews · → opens action menu (refresh · search · clear)",
			"accelerators: r/// c",
		}
	case "prompts":
		return []string{
			"↑↓ scroll · enter previews · → opens action menu (refresh · search · clear)",
			"accelerators: r/// c",
		}
	case "security":
		return []string{
			"↑↓ scroll · enter / → opens action menu (toggle view · rescan · search · clear)",
			"accelerators: v/r/// c",
		}
	case "plans":
		return []string{
			"↑↓ scroll · enter re-runs split · → action menu (edit · run · clear)",
		}
	case "context":
		return []string{
			"↑↓ scroll · enter previews · → action menu (edit · preview · active · clear)",
		}
	case "providers":
		return []string{
			"↑↓ scroll · enter views detail · → action menu",
			"menu: detail · primary · fallback · cycle model · save · new · refresh · search",
		}
	case "orchestrate":
		return []string{
			"shift+f4 (or alt+r) jumps here · live hierarchy of agents/subagents/todos/drive/tokens",
			"j/k/pgup/pgdn/g/G scroll · read-only panel — drive control from chat (/drive)",
		}
	case "shortcuts":
		return []string{
			"shift+f5 jumps here from any tab · /shortcuts and /keys open the help overlay",
			"j/k/pgup/pgdn/g/G scroll · per-tab quick hints: ctrl+h overlay",
		}
	case "contexts":
		return []string{
			"shift+f6 jumps here · live snapshot of every concurrently-active agent context",
			"main · parked · sub-agents · drive run with their tokens / step / last tool",
			"esc closes · /continue resumes parked · /drive stop kills the active run",
		}
	default:
		return []string{"f1 chat · f2 files · f3 patch · f4 workflow · f5 activity · f6 memory · f7 conversations · f8 providers · f9 status · f10 codemap · f11 tools · f12 security · shift+f1..f5 prompts/plans/context/orchestrate/shortcuts · ctrl+h help · ctrl+q quit"}
	}
}
