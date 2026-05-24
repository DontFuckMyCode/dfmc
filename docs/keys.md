# DFMC Keyboard Shortcuts Reference

> All shortcuts extracted from `ui/tui/` source files: `render_shortcuts.go`, `*_key.go`, `*_panel_keys.go`

---

## 🔲 Panel Navigation (F-Keys)

| Key | Panel | Description |
|-----|-------|-------------|
| `F1` | Chat | Main composer, transcript, slash commands |
| `F2` | Files | Project file picker, pins, preview |
| `F3` | Patch | Worktree diff, staged hunks, apply/dry-run |
| `F4` | Workflow | Drive cockpit, run list, TODO ladder |
| `F5` | Activity | Event firehose, search/filter, live follow |
| `F6` | Memory | Episodic/semantic memory layers |
| `F7` | Conversations | Saved conversations, branch navigation |
| `F8` | Providers | Provider catalog, keys, profiles |
| `F9` | Status | Engine + provider + AST/codemap snapshot |
| `F10` | CodeMap | Symbol/dep graph, cycles, hotspots |
| `F11` | Tools | Tool registry, parameter editor, tests |
| `F12` | Security | Scanner, secrets, vulnerability scan |
| `Shift+F1` | Prompts | Task/role/language prompt overlays |
| `Shift+F2` | Plans | Plan-split editor, subtask preview |
| `Shift+F3` | Context | Context-build preview, ordered snippets |
| `Shift+F4` | Orchestrate | Agents/subagents/todos/drive/token hierarchy |
| `Shift+F5` | Shortcuts | **This screen** — all shortcuts |
| `Shift+F6` | Contexts | Live agents — main, parked, subagents, drive run |

> `Alt+1..8` = Terminal-based alternative to F1..F8
> `Tab / Shift+Tab` = Panel cycle
> `Ctrl+B` = **Panel switcher** — fuzzy-filter overlay listing every panel. Use this when your terminal eats specific F-keys (most terminals consume `F11` for fullscreen, some consume `F1` for terminal help, `F4` for close-tab). Type 2-3 letters of the panel name → enter.

> ⚠ **F11 / F1 commonly intercepted:** if `F11` toggles fullscreen instead of opening Tools, use `Alt+I` or `Ctrl+B → tools`. If `F1` opens terminal help instead of Chat, use `Alt+1` or `Ctrl+B → chat`.

---

## ⌨️ Chat Composer

### Send / Queue

| Shortcut | Action |
|----------|--------|
| `Ctrl+X` | Send composer (queues during streaming) |
| `Enter / Ctrl+J` | Literal newline (`Alt+Enter` also works) |
| `Ctrl+C` | Cancel active turn (rage-quit if idle) |
| `Esc` | Close resume prompt · close picker |

### Editing

| Shortcut | Action |
|----------|--------|
| `Ctrl+W` | Delete word before cursor (keeps @mentions atomic) |
| `Ctrl+K` | Delete to end of line |
| `Ctrl+U` | Clear entire line |
| `Backspace` | Delete character before cursor |
| `Delete` | Delete character at cursor |

### Navigation

| Shortcut | Action |
|----------|--------|
| `Ctrl+A / Home` | Jump to line start |
| `Ctrl+E / End` | Jump to line end · jump to latest |
| `Ctrl+← / →` | Jump word by word |
| `↑ / ↓` | History navigation · suggestion navigation |
| `PgUp / PgDn` | Scroll transcript by 8 lines |
| `Shift+PgUp/Dn` | Scroll transcript by 3 lines |
| `Shift+↑ / ↓` | Scroll transcript by 3 lines |

### Pickers

| Shortcut | Action |
|----------|--------|
| `@` or `Ctrl+T` | Open file mention picker |
| `/` | Open slash command picker |
| `Tab` | Autocomplete (mention/slash/quick action) |
| `Ctrl+P` | Open slash command menu |
| `Ctrl+B` | Open panel switcher (fuzzy filter all 18 panels) |
| `Ctrl+G` | Jump to Activity tab |

---

## 📊 Stats Panel (Right sidebar of Chat tab)

| Shortcut | Action |
|----------|--------|
| `Alt+A` | Overview mode (default) |
| `Alt+S` | Todos mode |
| `Alt+D` | Tasks mode |
| `Alt+F` | Subagents mode |
| `Alt+P` | Providers mode |
| `Ctrl+S` | Show/hide panel |
| `Alt+X` | Toggle selection mode (transcript drag-select) |

---

## 🛑 Control — Stop / Clear

| Shortcut | Action |
|----------|--------|
| `Ctrl+C` | Cancel active turn (subagents auto-unwind) |
| `/cancel` | Slash alias for Ctrl+C (`/abort`, `/stop` also work) |
| `/drive stop [id]` | Cancel autonomous drive run |
| `/todos clear` | Clear shared TODO list |
| `/tasks clear` | Clear non-drive tasks |
| `/clear` | Clear transcript only (memory untouched) |
| `/compact [N]` | Compact old transcript into summary |
| `/queue clear` | Clear queued prompts |

---

## 🔍 Diagnostics — Inspection

| Command | Action |
|---------|--------|
| `/stats` | Session metrics: rounds, savings, fill, cost |
| `/workflow` | Snapshot of todos + subagents + drive + plan |
| `/todos` | Shared TODO list |
| `/tasks` | Task store panel (`j/k` navigate, `enter/esc`) |
| `/subagents` | Subagent fan-out + last delegation |
| `/queue` | Queued follow-up prompts |
| `/intent show` | Show last intent decision in full |
| `/doctor` | In-chat health snapshot |
| `/approve` | Tool-approval gate status |
| `/hooks` | Lifecycle hooks per event |
| `/status` | Engine + provider snapshot |

---

## 📝 Slash Commands — Daily Use

| Command | Action |
|---------|--------|
| `/drive <task>` | Start autonomous plan/execute loop |
| `/drive active` | Show currently running drive run |
| `/drive list` | List recent drive runs |
| `/drive stop [id]` | Stop the active drive run (resumable) |
| `/drive resume <id>` | Resume a stopped drive run by id (prefix match accepted) |
| `/continue` | Resume parked agent loop |
| `/plan` | Enter plan mode (read-only investigation, no file writes) |
| `/code` | Exit plan mode (prompts can mutate files again) |
| `/retry` | Regenerate the most recent assistant reply |
| `/edit` | Pull last user message into composer to amend and resend |
| `/copy` | Copy most recent assistant reply to system clipboard |
| `/version` | Print the DFMC build version (in-chat) |
| `/btw <note>` | Inject note into next agent step |
| `/split <task>` | Split large task into subtasks |
| `/review <path>` | Review file/directory |
| `/explain <path>` | Explain file |
| `/refactor <path>` | Suggest comprehensive refactor |
| `/test <path>` | Generate test draft for target |
| `/doc <path>` | Create/update documentation draft |
| `/scan` | Security + correctness scan |
| `/map` | Render codemap |
| `/conversation new` | Start new conversation (reset context) |
| `/export [path]` | Export transcript to `.dfmc/exports/*.md` |
| `/coach` | Mute/unmute coach notes |
| `/hints` | Show/hide trajectory hints |
| `/intent` | Toggle intent rewrites visibility |
| `/mouse` | Toggle mouse capture |
| `/select` | Activate selection mode |
| `/keylog` | Dump key events to footer (debug) |

---

## 🖥️ Panel-Specific Shortcuts

### Patch (`F3`)

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Next/previous hunk |
| `n / Alt+N` | Next file in diff |
| `b / Alt+B` | Previous file in diff |
| `a / Alt+A` | Apply patch to worktree |
| `c / Alt+C` | Check / dry-run apply (no write) |
| `u / Alt+U` | Undo last conversation step |
| `f / Alt+F` | Focus the current file in Files panel |
| `d / Alt+D` | Reload worktree diff |
| `Alt+L` | Load latest patch from engine |
| `Enter / → / l` | Open action menu |

### Workflow (`F4`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | Move in run list (or scroll TODO tree when run is selected) |
| `g/G` | Top / bottom of run list |
| `Enter / Space` | Select run / expand TODO at cursor |
| `Space` | **Toggle live-follow** — cursor auto-tracks the running TODO; LIVE chip + accent-bold row mark whatever is spinning right now |
| `r` | Open routing editor (provider tag → profile) — only when no run is selected |
| `→` | Open action menu (Stop · Resume · Copy ID · Routing · Refresh) |
| `Esc` | Deselect TODO → run → release live-follow (in that order) |

### Activity (`F5`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `g/G` or `home/end` | Jump to first/last |
| `PgUp/PgDn` | Page scroll |
| `Enter` | Open selected entry |
| `r` | Re-open selected entry |
| `f` | Focus Files panel at selected entry's file |
| `Ctrl+Y` | Copy selected entry to chat composer |
| `p` | Pause/resume live follow |
| `v` | Cycle view mode (all → tools → agents → errors → workflow → context) |
| `1-6` | Filters: All, Tools, Agents, Errors, Workflow, Context |
| `/` | Open search mode |
| `c` | Clear search (clear all when empty) |
| `→` | Open action menu |

### Context (`Shift+F3`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `PgUp/PgDn` | Page scroll |
| `a / f` | Load active context with debug info |
| `e` | Open inline query input |
| `Enter` | Run query |
| `Ctrl+M` | Toggle Context Manager sub-view |
| `c` | Clear everything |
| `→` | Open action menu |

### Files (`F2`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `r / Alt+R` | Refresh file index |
| `p / Alt+P` | Pin/unpin |
| `i` | Add to chat as `[[file:path]]` |
| `e` | Explain file in chat |
| `v` | Review file in chat |
| `c / Alt+C` | Clear active query (show all files) |
| `Enter / →` | Open action menu |

### Providers (`F8`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `g/G` | Jump to first/last |
| `PgUp/PgDn` | Page scroll |
| `Esc / q` | Return to list |
| `Enter` | Open detail menu |
| `→ / l` | Open contextual action menu |

### Memory (`F6`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `t` | Cycle tier: all → episodic → semantic |
| `r` | Refresh from store |
| `/` | Open search mode |
| `c` | Clear search query |
| `d` | Delete highlighted entry |
| `p` | Promote from episodic to semantic |
| `→ / l` | Open action menu |

### Plans (`Shift+F2`)

| Key | Action |
|-----|--------|
| `e` | Open text input (edit mode) |
| `j/k` or `↓/↑` | List navigation |
| `g/G` | Jump to first/last |
| `PgUp/PgDn` | Page scroll |
| `c` | Clear task and result |
| `Enter` | Re-run split |

### Status (`F9`)

| Key | Action |
|-----|--------|
| `h/j/k` or arrow keys | Card grid navigation (left/up/down) |
| `r` | Refresh status snapshot |
| `Enter` | Jump to selected card's detail tab |
| `1` | Jump to Files (Project card) |
| `2` | Jump to Providers |
| `3` | Jump to CodeMap (AST card) |
| `4` | Jump to Memory |
| `5` | Jump to Orchestrate |
| `g/G` | Jump to first/last card |
| `Ctrl+L` | Move right in card grid |
| `→` | Open action menu |

### CodeMap (`F10`)

| Key | Action |
|-----|--------|
| `v` | Cycle view: overview → hotspots → orphans → cycles |
| `r` | Refresh graph |
| `g` | Jump to start |
| `→ / l` | Open action menu |

### Prompts (`Shift+F1`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `g/G` | Jump to first/last |
| `Enter` | Load preview |
| `r` | Refresh templates |
| `/` | Open search mode |
| `c` | Clear search query |
| `→ / l` | Open action menu |

### Conversations (`F7`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `g/G` | Jump to first/last |
| `Enter` | Load preview |
| `L` | Resume / load conversation (capital L) |
| `S` | Deep search across conversations |
| `r` | Refresh list |
| `/` | Open search mode |
| `c` | Clear search query |
| `→ / l` | Open action menu |

### Tools (`F11` · `Alt+I`)

> Most terminals consume `F11` for fullscreen. Use `Alt+I` or `Ctrl+B → tools` if F11 doesn't reach DFMC.

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | Move in tool list |
| `Enter / r / Alt+R` | Run highlighted tool with current params |
| `e / Alt+E` | Enter param editor for highlighted tool |
| `x / Alt+X` | Reset overrides for highlighted tool |
| `Esc` (in editor) | Cancel param edit without saving |
| `Enter` (in editor) | Save params (or clear when blank) |

### Security (`F12`)

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | List navigation |
| `g/G` | Jump to first/last |
| `PgUp/PgDn` | Page scroll |
| `v` | Toggle view: secrets ↔ vulnerabilities |
| `r` | Rescan codebase |
| `i` | Ignore / whitelist highlighted finding |
| `f` | Send "fix this" prompt to chat for highlighted finding |
| `/` | Open search mode |
| `c` | Clear search query |
| `→ / l` | Open action menu |

### Contexts (`Shift+F6`)

> Live snapshot of every concurrently-active agent: main + parked + sub-agents + active drive run.

| Key | Action |
|-----|--------|
| `j/k` or `↓/↑` | Scroll within the panel |
| `g/G` | Top / bottom |
| `PgUp/PgDn` | Page scroll |
| `Esc / q` | Close overlay |

---

## 💡 Quick Tips

- **Open this screen:** `Shift+F5` or `Alt+H`
- **Reach any panel when F-keys fail:** `Ctrl+B` opens the fuzzy panel switcher — type 2-3 letters of the panel name and press enter
- **Jump to Activity:** `Ctrl+G` from Chat; press `F1` (or `Alt+1`) to come back
- **Queue while streaming:** `Ctrl+X` sends composer even during streaming
- **Atomic mentions:** `Ctrl+W` respects `@mention` boundaries — won't split mid-mention
- **Alt keys:** Work in terminals without F-key support
- **Drive cockpit live-follow:** open Workflow (`F4`), press `Space` to lock the cursor onto whichever TODO is currently running
- **Plan mode:** `/plan` flips the agent into read-only investigation; `/code` flips it back. Header shows a `PLAN` badge while it's on
- **Debug key delivery:** `/keylog` (or `DFMC_KEYLOG=1`) dumps every `tea.KeyMsg` into the footer — useful when a key seems to "do nothing"

---

_Last updated: derived from `ui/tui/render_shortcuts.go`, `ui/tui/panel_switcher.go`, `ui/tui/*_key.go`, `ui/tui/*_panel_keys.go`. The `Ctrl+B` panel switcher and per-panel sections (Patch, Workflow, Tools, Security, Contexts) reflect the current handler set in HEAD; if you regenerate this from source, run against the working tree._
