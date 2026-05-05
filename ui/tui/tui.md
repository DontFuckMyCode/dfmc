# TUI Audit — Chat & Panels Analysis & Fix Plan

**Date:** 2026-05-03
**Scope:** Chat tab (composer, transcript, scrollback), Files panel, Stats panel, Patch tab, Activity/Memory panels, Help overlay, keyboard routing
**Status:** ANALYSIS DONE → IMPLEMENTATION PENDING

---

## P1 — Critical: Must Fix

### P1.1 Bracketed Paste + Chunk Detection Double Path

**File:** `ui/tui/chat_key.go:59-118`

**Problem:**
`tea.EnableBracketedPaste` is active in `Init()`. When bracketed paste fires, it delivers the full pasted text as one `tea.KeyMsg{Paste: true}` (lines 59-68). This path is correct — it captures the complete text, creates one `pasteBlock`, inserts a placeholder, closes the window (`pasteWindowEnd = time.Time{}`), and returns immediately.

BUT the code below (lines 70-118) still runs chunk-based detection:
- `hasNewline || len(inserted) >= 16` triggers `isPaste`
- A new block or accumulation happens
- `pasteWindowEnd` is set 200ms forward

Result: on Windows Terminal (which sends `\r\n` per paste), bracketed paste fires with `Paste: true` AND the chunk path also triggers because `hasNewline=true`. Two blocks get created for one paste.

**Fix:**

```go
case tea.KeyRunes:
    inserted := string(msg.Runes)
    m.exitInputHistoryNavigation()
    m.slashMenu.command = 0
    // ...
    if msg.Paste {
        // BRACKETED PASTE — entire text in one message, no window needed
        n := len(m.chat.pasteBlocks) + 1
        block := pasteBlock{content: inserted, blockNum: n, lineCount: strings.Count(inserted, "\n")}
        m.chat.pasteBlocks = append(m.chat.pasteBlocks, block)
        m.insertInputText(block.placeholder())
        m.notice = fmt.Sprintf("PASTE %d bytes", len(inserted))
        m.chat.pasteWindowEnd = time.Time{} // no window — Enter submits immediately
        return m, nil
    }
    // CHUNK-BASED PASTE DETECTION — Windows non-bracketed fallback
    // (bracketed paste above already returned, so msg.Paste is always false here)
    hasNewline := strings.Contains(inserted, "\n")
    isPaste := hasNewline || len(inserted) >= 16
    // ... rest unchanged
```

**Also:** Remove the dead `isPaste` branch from line 70-96 — it only triggers when `msg.Paste == false` now, so the window extension logic stays for non-bracketed terminals that send line-by-line chunks.

---

### P1.2 Esc Triple-Duty Conflation

**File:** `ui/tui/chat_key.go:232-248`

**Problem:**
`tea.KeyEsc` handles three completely unrelated things:
1. Streaming cancel (`m.cancelActiveStream()`)
2. Resume prompt dismiss (`m.ui.resumePromptActive = false`)
3. Fall-through to global handler

These need separation because:
- `ctrl+c` already cancels streaming (update.go:521-530 kills paste blocks, doesn't cancel stream)
- Esc as streaming cancel conflicts with "Esc dismisses resume prompt" — if both flags are true, only the streaming cancel fires
- Users pressing Esc to dismiss an overlay expect it to close, not cancel a stream

**Also:** `ctrl+c` inside update.go:521-530 handles paste block cancel, then `tea.Quit`. But ctrl+c while streaming — does it cancel the stream? Check `m.cancelActiveStream()` call site.

**Fix — separate concerns:**

```go
case tea.KeyEsc:
    // Priority 1: dismiss top-most non-streaming overlay first
    if m.ui.resumePromptActive {
        m.ui.resumePromptActive = false
        m.notice = "Resume prompt dismissed — /continue re-opens it."
        return m, nil
    }
    // Streaming cancel is ctrl+c territory — Esc should not cancel
    // Let the notice say "Esc: dismiss or ctrl+c to cancel"
    return m, nil
```

Add a new message type `cancelStreamMsg` and handle it separately. Streaming cancel should be `ctrl+c` ONLY when actively sending. This makes Esc purely a UI dismiss key.

**In update.go:** Ensure ctrl+c when `m.chat.sending` calls `m.cancelActiveStream()` before the paste-block check.

---

### P1.3 `toolStripExpanded` Has No Persistence

**File:** `ui/tui/panel_states.go:346`

**Problem:**
`toolStripExpanded` defaults to `true` (expanded tool chips). User runs `/tools` to collapse, session continues — preference not persisted. Next session back to expanded.

**Fix:**
Add `toolStripExpanded` to `uiToggles` and make `/tools` toggle persist to a config key OR at minimum remember it for the session. True fix: add to `~/.dfmc/config.yaml` under `ui.*` or similar. Minimal fix: toggle a session-level `defaultToolStripExpanded` that `/tools` reads.

Better: Add a `dfmc config` subcommand or environment variable `DFMC_TOOL_STRIP_EXPANDED=false` that the TUI reads on init. The config already exists — just wire it up.

---

## P2 — Important: Should Fix

### P2.1 Stats Panel Auto-Activate Is Annoying

**File:** `ui/tui/update.go:50-61`

**Problem:**
`autoActivateStatsPanelMode` fires on EVERY engine event (`agent:loop:start`, `tool:result`, etc.) when:
- On chat tab (`activeTab == 0`)
- Not in selection mode
- Not focus-locked

This means every tool result slides the stats panel open for 4 seconds. Users hate it.

**Fix:**

```go
func (m *Model) autoActivateStatsPanelMode(mode statsPanelMode, label string) {
    // ONLY auto-activate on first agent:loop:start of a session,
    // not on every tool result. The "workflow focus" is useful once
    // at the start of a task, not 40 times during tool execution.
    if m.activeTab != 0 || m.ui.selectionModeActive || m.ui.statsPanelFocusLocked {
        return
    }
    // Only fire when there is no prior explicit user action on the stats panel.
    // If the user ever manually dismissed it (showStatsPanel was set), stop auto-activating.
    now := time.Now()
    if m.ui.showStatsPanel && m.statsPanelBoostActive(now) {
        // already open and boosted — just refresh content
        m.ui.statsPanelMode = mode
        return
    }
    // Only auto-open on agent:loop:start, not on tool:result
    // Caller (engine_events handler) should pass a flag or we check msg type
    // For now: only open if the panel has never been shown this session
    if m.ui.statsPanelBoostUntil.IsZero() {
        m.ui.showStatsPanel = true
        m.ui.statsPanelMode = mode
        m.ui.statsPanelBoostUntil = now.Add(statsPanelBoostDuration)
        m.notice = "Workflow focus: " + label
    }
}
```

Alternative: remove `autoActivateStatsPanelMode` entirely and require manual `alt+a/s/d/f/p`. Ask user which they prefer.

---

### P2.2 Help Overlay Keybinding Grupping

**File:** `ui/tui/help.go`

**Problem:**
`renderHelpOverlay` dumps ALL keybindings in one flat list. Finding "how do I scroll transcript" requires scanning 60+ entries.

**Fix — group by context:**

```go
type helpGroup struct {
    Title   string
    Bindings []helpBinding
}

groups := []helpGroup{
    {"Chat composer", []helpBinding{
        {"↑ / ↓", "history nav / suggestion nav"},
        {"Enter", "submit / newline in paste window"},
        {"Ctrl+J", "literal newline"},
        // ...
    }},
    {"Transcript scroll", []helpBinding{
        {"PgUp / PgDown", "scroll 8 lines"},
        {"Shift+↑ / ↓", "scroll 3 lines"},
        {"Shift+PgUp/PgDown", "scroll half-page"},
        {"End", "jump to latest"},
        // ...
    }},
    {"Panels", []helpBinding{
        {"F1-F12", "switch tabs (F1=Chat, F2=Status, ...)"},
        // ...
    }},
}
```

Also: make overlay scrollable if terminal is short.

---

### P2.3 Input History Draft Loss on Second Prev

**File:** `ui/tui/input.go:399-410`

**Problem:**
`recallInputHistoryPrev` saves `draft = m.chat.input` EVERY time it's called when `index < 0` (first entry into history navigation). But when navigating further up (index already ≥ 0), the previous draft is overwritten:

```go
if m.inputHistory.index < 0 {
    m.inputHistory.draft = m.chat.input  // saves draft
    m.inputHistory.index = len(m.inputHistory.history) - 1
} else if m.inputHistory.index > 0 {
    m.inputHistory.index--
    // draft is NOT saved here — previous draft LOST
}
m.setChatInput(m.inputHistory.history[m.inputHistory.index])
```

User types "hello", presses Up → history shows last item, draft="hello". User presses Up again → history navigates further back, draft still "hello". User presses Down once → returns to "hello". User presses Down again → goes to newest history item, draft is gone.

**Fix:**

```go
func (m *Model) recallInputHistoryPrev() bool {
    if len(m.inputHistory.history) == 0 {
        return false
    }
    if m.inputHistory.index < 0 {
        // First entry: save what the user was typing before going into history
        m.inputHistory.draft = m.chat.input
        m.inputHistory.index = len(m.inputHistory.history) - 1
    } else if m.inputHistory.index > 0 {
        m.inputHistory.index--
    }
    // index == 0 && user still presses Up → already at oldest, do nothing
    m.setChatInput(m.inputHistory.history[m.inputHistory.index])
    return true
}
```

The draft is saved ONCE at first entry. Navigation within history doesn't overwrite it. Only `recallInputHistoryNext` restoring past the newest item restores the draft.

Also fix `exitInputHistoryNavigation` to also clear draft:

```go
func (m *Model) exitInputHistoryNavigation() {
    m.inputHistory.index = -1
    m.inputHistory.draft = ""  // always clear
    // also reset suggestion menus?
}
```

---

### P2.4 Files Panel Tab-Switch Code Duplication

**File:** `ui/tui/panel_keys.go:36-73`

**Problem:**
Three handlers (`i`, `e`, `v`) all do the same sequence:
1. Get `selectedFile()`
2. Build a chat input string
3. Set `activeTab = 0`
4. Set notice
5. Return

Duplication across lines 36-73.

**Fix — extract helper:**

```go
func (m Model) insertFileIntoComposer(path, prefix string) (Model, tea.Cmd) {
    if path == "" {
        return m, nil
    }
    marker := prefix + path
    current := strings.TrimRight(m.chat.input, " ")
    if current != "" {
        current += " "
    }
    m.setChatInput(current + marker)
    m.activeTab = 0
    m.notice = "Inserted " + marker + " into chat."
    return m, nil
}

// handleFilesKey cases:
case "i":
    return m.insertFileIntoComposer(m.selectedFile(), fmt.Sprintf("[[file:%s]]", m.selectedFile()))
case "e":
    return m.insertFileIntoComposer(m.selectedFile(), fmt.Sprintf("Explain [[file:%s]] ", m.selectedFile()))
case "v":
    return m.insertFileIntoComposer(m.selectedFile(), fmt.Sprintf("Review [[file:%s]] ", m.selectedFile()))
```

---

### P2.5 Coach Notes Two Code Paths

**Files:** `ui/tui/transcript.go:31-66`, `ui/tui/engine_events.go`

**Problem:**
`appendCoachMessage` (transcript.go:31-66) appends to transcript, calls `m.appendActivity()`, and accumulates `sessionCoachNotes`.

Two sources of truth for coach notes — if the engine_events path fails or is bypassed, summary is empty even if transcript has coach lines.

**Fix:**
Have `appendCoachMessage` also append to `sessionCoachNotes`:

```go
func (m Model) appendCoachMessage(text string, severity coachSeverity, origin string, action string) Model {
    // ... existing transcript.append logic ...
    m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleCoach, body))
    m.chat.scrollback = 0
    m.appendActivity("coach: " + text)
    // ALSO accumulate for session summary
    m.agentLoop.sessionCoachNotes = append(m.agentLoop.sessionCoachNotes, text)
    // ...
}
```

Keep `appendCoachMessage` as the single accumulation point and avoid adding a second `sessionCoachNotes` writer in `handleEngineEvent`.

---

### P2.6 `scrollTranscript` Delta Magic Numbers

**File:** `ui/tui/transcript.go:133`, `ui/tui/chat_key.go:227-256`

**Problem:**
Magic numbers: `-8` (PgUp/PgDown), `-3` (Shift+Up/Down), `mouseWheelStep` (typically 3-5), `mouseWheelPageStep` (8-16). These are scattered without explanation.

**Fix — extract as package constants:**

```go
// scrollStep is how many transcript lines PgUp/PgDown scrolls.
// 8 is one logical "page" in a typical terminal with header/input.
const scrollPageStep = 8

// scrollFineStep is the finer scroll for Shift+modifier keys.
// 3 matches the mouse wheel single-tick step.
const scrollFineStep = 3

// mouseWheelStep matches most mouse wheel detents (3–5 lines).
// Initialized from terminal or hardcoded fallback.
var mouseWheelStep = 3

// mouseWheelPageStep for Shift+wheel — a half-page jump.
const mouseWheelPageStep = 8
```

Document WHY 8: a typical terminal shows ~20-30 lines of transcript after header+input. One "page" is roughly 8 lines so PgUp gives you one full screen of history.

---

## P3 — Nice to Have

### F1-F12 One-Indexed Inconsistency

**File:** `ui/tui/update.go:623-697`

**Problem:**
Actual tab order in `tui.go:304`:
```
tabs = [Chat(0), Status(1), Files(2), Patch(3), Workflow(4),
       Tools(5), Activity(6), Memory(7), CodeMap(8), Conversations(9),
       Prompts(10), Security(11), Plans(12), Context(13), Providers(14)]
```

Current F-key mappings vs correct:
```
F1 → activeTab=0  → Chat       ✓ correct
F2 → activeTab=2  → Files     ✗ WRONG (should be Status=1)
     Ctrl+I                  → Status    ✓ (correct but undocumented)
F3 → activeTab=6  → Activity  ✗ WRONG (should be Files=2)
F4 → activeTab=?  → Providers ✓ correct
F5 → activeTab=4  → Workflow  ✓ correct
F6 → activeTab=5  → Tools    ✗ WRONG (should be Patch=3)
F7 → activeTab=3  → Patch    ✗ WRONG (should be Tools=5)
F8 → activeTab=7  → Memory    ✓ correct
F9 → activeTab=8  → CodeMap   ✓ correct
F10 → activeTab=9 → Conversations ✓ correct
F11 → activeTab=10 → Prompts  ✓ correct
F12 → activeTab=11 → Security ✓ correct
```

**Fix — correct F-key mappings:**

```go
case "f1", "alt+1":
    m.activeTab = 0   // Chat
    return m, nil
case "f2", "alt+2":
    m.activeTab = 1   // Status (was 2=Files)
    return m, nil
case "ctrl+i":
    m = m.activateDiagnosticTab("Status")
    return m, nil
case "f3", "alt+3":
    m.activeTab = 2   // Files (was 6=Activity)
    return m, nil
case "f4", "alt+4":
    m.activeTab = 3   // Patch (was Providers, now correct)
    return m, nil
case "f5", "alt+5":
    m.activeTab = 4   // Workflow
    return m, nil
case "f6", "alt+6":
    m.activeTab = 5   // Tools (was 3=Patch, now correct)
    return m, nil
case "f7", "alt+7":
    m.activeTab = 6   // Activity (was 3=Patch, now correct)
    return m, nil
case "f8", "alt+8":
    m.activeTab = 7   // Memory
    return m, nil
case "f9", "alt+9":
    m.activeTab = 8   // CodeMap
    return m, nil
case "f10", "alt+0":
    m.activeTab = 9   // Conversations
    return m, nil
case "f11", "alt+t":
    m.activeTab = 10  // Prompts
    return m, nil
case "f12":
    m.activeTab = 11  // Security
    return m, nil
case "ctrl+o":
    m.activeTab = 14  // Providers (new F-key for Providers missing)
    return m, nil
```

Also add `alt+0` for Status (consistent with `alt+1` through `alt+9` pattern), and map F12 to Security (already correct). Add `alt+shift+0` or `ctrl+o` for Providers since it has no F-key.

---

### Activity Follow Heuristic

**File:** `ui/tui/panel_states.go:363-370`

**Problem:**
`activityPanelState.follow` is a boolean — either pinned to tail or not. In practice users want: "if I'm at the bottom, auto-scroll. If I've scrolled up to read something, stop auto-scrolling."

**Fix:**
Replace `follow bool` with a scroll threshold. When `scroll >= maxScroll - 2` consider "at tail" = follow mode. Any manual scroll up sets `follow = false`. New event arrives: if `follow` is true, reset scroll to tail.

```go
func (m *Model) isActivityAtTail() bool {
    if len(m.activity.entries) == 0 {
        return true
    }
    maxScroll := len(m.activity.entries)
    return m.activity.scroll >= maxScroll-2
}
```

---

### Selection Mode / Drag-Select Communication

**File:** `ui/tui/render_layout.go:78-88`, `ui/tui/update.go:571-574`

**Problem:**
User asked "drag ederek seçebilmeler" (drag to select). Current `selectionModeActive` disables the TUI frame (border/background) so terminal native text selection works. This IS the correct implementation — when selection mode is on, you can drag-select text in the transcript column.

But the TUI doesn't communicate this clearly. User might not know `/select` or `alt+x` exists.

**Fix:**
Add a notice when selection mode activates: `"Selection mode — terminal drag-select works now · alt+x to exit"`. Make the hint visible in the input area when selection mode is active.

---

### Pipe/Alt+key Conflicts

**File:** `ui/tui/update.go:555-599`, `ui/tui/chat_key.go`

**Problem:**
Many bindings use both `j`/`k` and `alt+j`/`alt+k` as equivalents. But on some terminals Alt+J is not deliverable or sends a different escape sequence. Turkish-Q keyboard with MinTTY: `@` comes as `alt+q` (chat_key.go:175 workaround). This means `alt+<letter>` bindings are unreliable on Turkish keyboards.

**Fix:**
Prefer non-alt variants as primary. Make alt-versions supplementary. Add a notice in help overlay: "alt+keys may not work on Turkish keyboards — use letter keys instead."

Also review all `alt+x` bindings in update.go and chat_key.go for Turkish keyboard compatibility. The existing `alt+q` workaround in chat_key.go is good but incomplete — other alt-letters may also fail.

---

## Keyboard Shortcut Reference (consolidated)

### Chat Tab — Composer

| Key | Action |
|-----|--------|
| ↑ / ↓ | history nav (when no suggestion) / suggestion nav |
| Enter | submit / newline in paste window |
| Ctrl+J | literal newline |
| Shift+Enter | literal newline (if terminal supports) |
| Alt+Enter | literal newline (terminal-dependent) |
| Ctrl+W | kill word before cursor |
| Ctrl+K | kill to end of line |
| Ctrl+U | clear entire input line |
| Ctrl+A / Home | cursor to line start |
| Ctrl+E / End | cursor to line end / jump to latest transcript |
| Ctrl+← / → | word left/right |
| Ctrl+T | open file mention picker |
| @ | open file mention picker |
| Ctrl+H / Backspace | delete char before cursor |
| Delete | delete char at cursor |
| PgUp / PgDown | scroll transcript ±8 lines |
| Shift+PgUp/PgDown | scroll transcript ±3 lines |
| Shift+↑ / ↓ | scroll transcript ±3 lines |
| Mouse wheel | scroll transcript ±3 lines |
| Shift+mouse wheel | scroll transcript ±8 lines |
| Esc | dismiss resume prompt (streaming cancel is ctrl+c) |
| Ctrl+C | cancel paste blocks / quit |
| Ctrl+P | open slash command menu |
| Ctrl+G | jump to Activity tab |
| Tab | autocomplete (mention/slash/quick action) |

### Panel Tabs (F-keys are 1-indexed in user mental model: F1=first tab)

| Key | Tab | Code index |
|-----|-----|------------|
| F1 | Chat | 0 |
| F2 | Status | 1 |
| F3 | Files | 2 |
| F4 | Patch | 3 |
| F5 | Workflow | 4 |
| F6 | Tools | 5 |
| F7 | Activity | 6 |
| F8 | Memory | 7 |
| F9 | CodeMap | 8 |
| F10 | Conversations | 9 |
| F11 | Prompts | 10 |
| F12 | Security | 11 |
| Ctrl+I | Context | (via activateContextPanel) |
| Ctrl+O | Providers | (via activateProvidersPanel) |
| Tab / Shift+Tab | next / previous tab | — |

### Stats Panel (alt+a/s/d/f/p on Chat tab)

| Key | Action |
|-----|--------|
| alt+a | Overview mode |
| alt+s | Todos mode |
| alt+d | Tasks mode |
| alt+f | Subagents mode |
| alt+p | Providers mode |
| alt+x | toggle selection mode (drag-select) |
| ctrl+h | help overlay |
| ctrl+s | toggle stats panel |

### Files Panel

| Key | Action |
|-----|--------|
| j / ↓ | next file |
| k / ↑ | previous file |
| Enter | load preview |
| r | reload directory |
| p | toggle pin |
| i | insert [[file:path]] into chat |
| e | prepare Explain prompt |
| v | prepare Review prompt |

### Patch Panel

| Key | Action |
|-----|--------|
| n / b | next / previous patched file |
| j / k | next / previous hunk in file |
| f | focus current file |
| c | check patch (dry-run) |
| a | apply patch |
| u | undo last conversation message |
| d | reload workspace diff |
| l | reload latest assistant patch |

---

## Implementation Order

1. **P1.1** — Fix bracketed paste double path (chat_key.go)
2. **P1.2** — Separate Esc concerns (chat_key.go, update.go)
3. **P1.3** — toolStripExpanded persistence (config + init)
4. **P2.3** — Input history draft fix (input.go)
5. **P2.6** — scrollTranscript constants (transcript.go, constants)
6. **P2.4** — Files panel helper (panel_keys.go)
7. **P2.5** — Coach notes single source (transcript.go, engine_events)
8. **P2.1** — Stats panel auto-activate (update.go)
9. **P2.2** — Help overlay grouping (help.go)
10. **P3.F1-F12** — Fix F-key tab indices (update.go)
11. **P3.Activity** — Activity follow heuristic (activity_panel)
12. **P3.Selection** — Selection mode notice (render_chat, notice)
13. **P3.Turkish** — Alt-key compatibility notes (help.go)

After implementation: every fix needs a corresponding test or test update. Scroll behavior, paste behavior, history navigation each have existing tests — update those tests to match new behavior.
