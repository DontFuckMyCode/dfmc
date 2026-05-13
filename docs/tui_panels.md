# DFMC TUI Panel Envanteri

Bu rapor `ui/tui` kodundan çıkarılmıştır. Ana kaynaklar:
`ui/tui/tui_lifecycle.go`, `ui/tui/update_keypress_shortcuts.go`,
`ui/tui/update_keypress.go`, `ui/tui/render_layout.go`,
`ui/tui/panel_switcher.go`, `ui/tui/panel_overlay.go`,
`ui/tui/theme/stats_panel.go`.

## Mimari Özet

TUI üç ana yüzey ailesine ayrılıyor:

| Yüzey | Nerede çiziliyor | Ne zaman görünür |
|---|---|---|
| First-class tabs | Ana gövde | `Chat`, `Files`, `Patch`, `Workflow`, `Activity`, `Memory`, `Conversations`, `Providers` |
| Demoted overlays | Aktif tab üstüne tam gövde overlay | `Status`, `CodeMap`, `Tools`, `Security`, `Prompts`, `Plans`, `Context`, `Orchestrate`, `Shortcuts`, `Contexts`, `ProviderLog`, `Telegram`, `ToolStatus` |
| Chat yan panelleri | Chat tab içinde sağ taraf veya floating panel | Stats panel, Tasks panel, help overlay, pickers |

Kalıcı tab listesi `NewModel` içinde şu sırada kuruluyor:
`Chat`, `Files`, `Patch`, `Workflow`, `Activity`, `Memory`, `Conversations`, `Providers`.

Overlay yönlendirmesi `demotedPanelKinds` ile yapılır. Bir overlay açıkken `Esc` veya `q` kapatır.

## Global Kısayollar

| Tuş | Etki |
|---|---|
| `Ctrl+C`, `Ctrl+Q` | Aktif paste/stream varsa iptal eder; aksi halde çıkış |
| `Ctrl+U` | Chat input temizle |
| `Ctrl+H` | Help overlay toggle |
| `Alt+H` | Help overlay toggle |
| `Alt+0`, `Alt+9` | Help overlay toggle |
| `Esc` | Önce help/panel/tasks/stats focus kapatır; stream varsa iptal eder |
| `Ctrl+P` | Chat slash palette açar (`/`) |
| `Ctrl+B` | Panel switcher overlay |
| `Ctrl+S` | Chat sağ stats panel show/hide |
| `PgUp`, `PgDn` | Chat stats panel görünüyorsa stats scroll |
| `Alt+X` | Chat selection mode toggle |
| `Tab`, `Shift+Tab` | Chat dışında first-class tablar arasında gezinir |
| `Ctrl+G` | Activity tab |
| `Ctrl+O` | Providers tab |

Navigasyon hedef politikası:

- Paneller arası geçişin ana yolu `Ctrl+B` panel switcher ve `F1..F12` / `Shift+F1..F8` olmalı.
- Her yerden Chat'e dönüş için sabit yol `F1`.
- Panel içinde normal harf tuşları panelden çıkarmamalı; `Enter`, `Space`, ok tuşları, `Esc`, gerekirse `Ctrl+...`, `Alt+...`, `Ctrl+Alt+...` tercih edilmeli.
- Mevcut single-letter accelerator'lar hâlâ kodda var; bunlar geriye dönük davranış. Temiz refactor hedefi, panel içi single-letter kullanımını action menu veya modifier'lı tuşlara taşımak.

## Panel Switcher

`Ctrl+B` ile açılır. Panel adı/hint filtrelenir, `Up/Down` seçer, `Enter` veya `Tab` açar, `Esc`/`Ctrl+B` kapatır.

Kaynak listedeki paneller:

| Panel | Switcher hint | Not |
|---|---|---|
| Chat | `F1`, `Alt+1` | Composer + transcript |
| Files | `F2`, `Alt+2` | Dosya listesi + preview |
| Patch | `F3`, `Alt+3` | Worktree diff + assistant patch |
| Workflow | `F4`, `Alt+4` | Drive cockpit |
| Activity | `F5`, `Alt+5` | Event firehose |
| Memory | `F6`, `Alt+6` | Memory tiers |
| Conversations | `F7`, `Alt+7` | Kaydedilmiş konuşmalar |
| Providers | `Alt+P` | Chat stats provider status: active provider, fallbacks, token window |
| Provider Config | `F8`, `Alt+8`, `Ctrl+O` | Advanced provider catalog + keys |
| Status | `F9`, `Ctrl+I` | Engine/provider snapshot |
| CodeMap | `F10` | Symbol/dependency graph |
| Tools | `F11`, `Alt+I` | Tool registry |
| Security | `F12` | Scanner |
| Prompts | `Shift+F1` | Prompt catalog |
| Plans | `Shift+F2`, `Ctrl+Y` | Task split preview |
| Context | `Shift+F3`, `Ctrl+W` | Context preview/manager |
| Orchestrate | `Shift+F4`, `Alt+R` | Agent hierarchy |
| Shortcuts | `Shift+F5` | Cheat sheet |
| Contexts | `Shift+F6` | Live contexts |
| ProviderLog | `Shift+F7`, `Ctrl+L` | Provider call log |
| Telegram | `Shift+F8` | WIP telegram panel |
| ToolStatus | `Alt+T` | Tool call history |

Not: `Ctrl+B` listesi tüm panel overlaylerini kapsıyor; `Shift+F8/F20` Telegram overlay'e bağlandı.

## First-Class Tablar

### Chat

| Alan | İçerik |
|---|---|
| Açılış | `F1`, `Alt+1`, panel switcher |
| Ana içerik | Transcript, composer, slash menu, mention picker, next actions, stream/tool timeline |
| Gönderme | Kodda default `Enter` submit eder; `Alt+Enter` newline ekler; `Ctrl+X` de submit/queue |
| Editor | `Ctrl+W` word delete, `Ctrl+K` line-end delete, `Ctrl+U` input clear, `Ctrl+A/Home`, `Ctrl+E/End`, `Ctrl+Left/Right` |
| Scroll | `PgUp/PgDn`, `Shift+PgUp/PgDn`, `Shift+Up/Down` |
| Pickers | `/` slash menu, `Ctrl+P` command palette, `@` veya `Ctrl+T` file mention picker |
| Kapatma/iptal | `Esc` picker/resume/next-actions kapatır; stream iptali global `Ctrl+C` |

Chat içinde özel yüzeyler:

| Yüzey | Açılış | Tuşlar |
|---|---|---|
| Slash menu | `/`, `Ctrl+P` | `Up/Down`, `Tab`, `Enter`, `Esc` |
| Command picker | `/provider`, `/model`, `/tool`, `/read`, `/run`, `/grep`; ayrıca `Alt+M` model picker, `Alt+Shift+P` provider picker | `Up/Down`, `Tab`, `Enter`, `Ctrl+S` persist hint toggle, type-to-filter, `Esc` |
| File mention picker | `@`, `/file`, `Ctrl+T` | `Up/Down`, `Tab`/`Enter` insert `[[file:...]]`, `Esc` |
| Help overlay | `Ctrl+H`, `Alt+H`, `/help`, `/shortcuts`, `/keys` | Chat input filter gibi çalışır; `Esc` filter temizler veya kapatır |
| Tasks panel | `/tasks`, `/tasks open`; `/tasks close` kapatır | `j/k`, `Up/Down`, `Enter/Right` expand, `Left` collapse, `Home/End`, `Esc/q` close |
| Stats panel | Default açık; `Ctrl+S` show/hide | `Alt+A/S/D/F/P` mode seçer, aynı mode tekrar focus/lock davranışı verir, `PgUp/PgDn` scroll |

### Files

| Alan | İçerik |
|---|---|
| Açılış | `F2`, `Alt+2` |
| İçerik | Proje dosya listesi, seçili dosya preview, pinned file state |
| Gezinme | `j/k`, `Up/Down` |
| Action menu | `Enter`, `Right`, `l` |
| Direkt aksiyon | `r` reload, `p` pin/unpin, `i` mention insert, `e` explain prompt, `v` review prompt |
| Menü içeriği | Open preview, pin/unpin, insert `[[file:...]]`, explain, review, reload index |

### Patch

| Alan | İçerik |
|---|---|
| Açılış | `F3`, `Alt+3` |
| İçerik | Worktree diff, latest assistant patch, file/hunk cursor |
| Action menu | `Enter`, `Right`, `l` |
| Direkt aksiyon | `d` reload worktree diff, `Alt+L` latest patch reload, `n/b` next/prev file, `j/k` next/prev hunk, `f` focus file, `c` dry-run, `a` apply, `u` undo turn |
| Menü içeriği | Apply patch, apply current hunk, check current hunk, check patch, undo last turn, next/previous file, next/previous hunk, focus file, reload worktree, reload latest assistant patch |

### Workflow

| Alan | İçerik |
|---|---|
| Açılış | `F4`, `Alt+4` |
| İçerik | Drive runs, selected run TODO tree, routing editor |
| Gezinme | `j/k`, `Up/Down`, `g/G` |
| Run açma | Run list'te `Enter`/`o` seçer |
| TODO tree | Run seçiliyken `Enter`/`o` TODO expand/detail toggle |
| Action menu | `Right`, `l` |
| Direkt aksiyon | `r` routing editor, `Esc` selected TODO/run/editor geri, `Space` live-follow toggle |
| Menü içeriği | Open run/TODO tree, stop run, resume run, copy run ID, deselect run, routing editor, refresh runs |
| Routing editor | `r` veya menüden açılır; `j/k` satır/profile seçer, `Enter` edit/commit, `+` add, `d` delete, `Esc` save+close |

### Activity

| Alan | İçerik |
|---|---|
| Açılış | `F5`, `Alt+5`, `Ctrl+G` |
| İçerik | Engine event firehose, filtered timeline, inspector/detail |
| Gezinme | `j/k`, `Up/Down`, `PgUp/PgDn`, `g/G` |
| Action menu | `Right`, `l` |
| Direkt aksiyon | `Enter/o` open selected detail, `r` open raw/detail, `f` focus file, `y` copy to composer, `p` live follow toggle, `/` search, `c` clear query/all, `v` cycle view, `1..6` filter modes |
| Filter modes | All, tools, agents, errors, workflow, context |
| Search mode | `/` açar; yazı filter; `Enter` confirm, `Esc` cancel |

### Memory

| Alan | İçerik |
|---|---|
| Açılış | `F6`, `Alt+6` |
| İçerik | Working/episodic/semantic/all memory rows |
| Gezinme | `j/k`, `Up/Down`, `PgUp/PgDn`, `g/G` |
| Action menu | `Right`, `l` |
| Direkt aksiyon | `Enter` row expand/collapse, `t` tier cycle, `r` reload, `/` search, `c` clear query, `d` delete, `p` promote episodic to semantic |
| Menü içeriği | Cycle tier, refresh, search, clear query, delete highlighted, promote highlighted |

### Conversations

| Alan | İçerik |
|---|---|
| Açılış | `F7`, `Alt+7` |
| İçerik | Conversation list, branch/preview |
| Gezinme | `j/k`, `Up/Down`, `PgUp/PgDn`, `g/G` |
| Action menu | `Right`, `l` |
| Direkt aksiyon | `Enter` load preview, `L` resume/load conversation as active and jump Chat, `r` refresh, `/` search, `S` deep search, `c` clear search |
| Menü içeriği | Load preview, resume conversation, refresh, search, deep search, clear search |

### Provider Config / Providers Tab

| Alan | İçerik |
|---|---|
| Açılış | `F8`, `Alt+8`, `Ctrl+O`, `/providers` panel command, `Ctrl+B` -> `Provider Config` |
| İçerik | Configured provider list, provider detail, model list, catalog, tiers, skill routes, pipeline editor |
| List gezinme | `Up/Down`, `PgUp/PgDn`, `Home/End` |
| Search | `/` veya `Ctrl+F`; `Enter` accept, `Esc` clear |
| Action menu | List/detail/pipeline içinde `Enter`, `Right`, `Space` |
| List menüsü | Open details, make primary, test connection, delete provider, sync models.dev catalog, add provider from catalog, add custom provider, tier matrix, skill routes, reset keys, refresh, search |
| Detail | `Esc/Left` back, `/` model search, `Up/Down/Home/End/Pg` model nav, `Enter/Right/Space` detail menu |
| Detail menüsü | Use selected model for session, set primary, toggle fallback, add model from models.dev, add custom model id, edit provider/API key, make primary, test connection, delete model, back |
| Model picker | Catalog picker: `Up/Down/Home/End/Pg`, `Enter` add, `Esc` cancel, `Space` manual mode; manual mode types custom id |
| Pipeline subpanel | `Esc/Left` back, nav keys, `Enter/Right/Space` action menu |
| Pipeline menüsü | Activate, edit, delete, new pipeline, back |

## Demoted Overlay Paneller

Bu paneller ana tab değil; aktif gövdeyi overlay olarak kaplar. `Esc` veya `q` kapatır.

| Panel | Açılış | İçerik | Tuşlar / Alt paneller |
|---|---|---|---|
| Status | `F9`, `Ctrl+I` | Engine/project/provider/AST/memory/subagent kartları | `h/l/j/k`, arrows, `g/G`, `r`, `Enter` selected detail paneline jump, `Right` action menu |
| CodeMap | `F10` | Symbol/dependency graph, hotspots, orphans, cycles, visual tree | `j/k`, `PgUp/PgDn`, `g/G`, `v` view cycle, `r` reload, `Right/Enter/l` menu; visual view'da `Right/Enter/l` expand, `Left/h` collapse |
| Tools | `F11`, `Alt+I` | Tool registry, param editor, run output | `j/k`, `e` edit params, `x` reset, `Enter/r` run, `Right/l` menu; edit mode `Enter` save, `Esc` cancel |
| Security | `F12` | Secrets/vulns scan results | `j/k`, `PgUp/PgDn`, `g/G`, `v` secrets/vulns, `r` rescan, `/` search, `c` clear, `i` ignore, `f` fix prompt to Chat, `Right/Enter/l` menu |
| Prompts | `Shift+F1`/`F13` | Prompt template catalog + preview | `j/k`, `PgUp/PgDn`, `g/G`, `Enter` preview, `r` reload, `/` search, `c` clear, `Right/l` menu |
| Plans | `Shift+F2`/`F14`, `Ctrl+Y` | Offline task splitter / plan preview | `e` edit query, `Enter` rerun, `c` clear, `j/k`, `PgUp/PgDn`, `g/G`, `Right/l` menu |
| Context | `Shift+F3`/`F15`, `Ctrl+W` outside Chat | Context budget/build preview, active context, context manager | `e` edit query, `Enter` preview, `a/f` active full context, `c` clear, `m` manager, scroll keys, `Right/l` menu |
| Orchestrate | `Shift+F4`/`F16`, `Alt+R` | Main agent, subagents, todos, drive, tokens, recent activity | Read-only scroll: `j/k`, `PgUp/PgDn`, `g/G` |
| Shortcuts | `Shift+F5`/`F17` | Cheat sheet | Read-only scroll: `j/k`, `PgUp/PgDn`, `g/G` |
| Contexts | `Shift+F6`/`F18` | Main/parked/subagent/drive active contexts | Render-only overlay; close with `Esc/q` |
| ProviderLog | `Shift+F7`/`F19`, `Ctrl+L` | Provider calls, model, tokens, prompt/reply preview | Read-only scroll: `j/k`, `PgUp/PgDn`, `g/G` |
| Telegram | `Shift+F8`/`F20` | Telegram bot status/messages, WIP build tag panel | Render-only overlay; close with `Esc/q` |
| ToolStatus | `Alt+T` | Detailed tool call history, params/results/errors | `j/k`, `PgUp/PgDn`, `g/G`, `Esc/q` |

## Context Panel Alt Görünümleri

| Alt görünüm | Açılış | Ne gösterir | Tuşlar |
|---|---|---|---|
| Context preview | `Shift+F3`, sonra `e` query, `Enter` run | Seçili query için context build/budget preview | scroll, `c` clear, `a/f` active context |
| Active full context | Context içinde `a` veya `f` | Son engine context snapshot | scroll |
| Context Manager | Context içinde `m` | Aktif conversation mesajları, ID/role/token/tool bilgisi, silme seçimi | `j/k`, `PgUp/PgDn`, `g/G`, `Space` mark, `a` all, `x/d` delete marked confirm, `D` delete one, `Enter` confirm/toggle, `Esc` back |
| Context Manager action menu | Manager aktifken `Right/l` | Mark/select/delete/back menüsü | Ortak action menu tuşları |

İlgili slash komutları: `/context`, `/context show`, `/context full|budget`, `/context why|recommend`, `/context messages`, `/context drop <id>`, `/context gc`, `/context gc run`.

## Sağ Stats Panel

Chat tabında görünür. Varsayılan açık. `Ctrl+S` kapat/aç.

| Mode | Açılış | Bölümler |
|---|---|---|
| Overview | `Alt+A` | `PROVIDER`, `NEXT`, `CONTEXT`, `TOKENS`, `TOOL LOOP`, `TOOLS`, `WORKFLOW`, opsiyonel `GIT`, `SESSION` |
| Todos | `Alt+S` | `TODO STATE`, `NEXT`, `LIVE LOOP`, `CONTEXT` |
| Tasks | `Alt+D` | `TASK GRAPH`, `DRIVE`, `NEXT`, `ORCHESTRATION MAP`, `LIVE LOOP` |
| Subagents | `Alt+F` | `SUBAGENTS`, `NEXT`, `LIVE LOOP`, `RECENT` |
| Providers | `Alt+P`, `Ctrl+B` -> `Providers` | `ACTIVE`, `ROUTING`, `PROVIDERS`, `CONTEXT`, `SESSION` |

Davranış:

- `Alt+A/S/D/F/P` mode değiştirir. Panel boost/focus state'e girebilir.
- Focus locked durumunda `Esc` unlock eder.
- Providers mode focus locked ise `j/k`, `Up/Down`, `g/G`, `Enter`, `m`, `f`, `s` provider satır handler'ına gider:
  - `Enter` provider/model seçimini uygular.
  - `m` selected provider model cycle/edit mode.
  - `f` fallback cycle/edit mode.
  - `s` seçimi user config'e kaydeder.
- `PgUp/PgDn` stats panel scroll.

## Ortak Action Menu

Birçok panelde `Right` veya `l`, bazı panellerde `Enter`, ortak action menu açar.

| Tuş | Etki |
|---|---|
| `j/k`, `Up/Down` | Menü satırı seç |
| `g/Home`, `G/End` | İlk/son aksiyon |
| `Enter` | Seçili aksiyonu çalıştır |
| `Esc`, `Left`, `h` | Menüyü kapat |
| Aksiyon accelerator'ı | Menü açıkken doğrudan o aksiyon |

Bu menü Files, Patch, Workflow, Activity, Memory, Conversations, Providers, Status, CodeMap, Tools, Security, Prompts, Plans, Context ve Context Manager'da kullanılıyor.

## Slash Komutları ve Panel İlişkileri

| Komut | Panel/Yüzey ilişkisi |
|---|---|
| `/help`, `/shortcuts`, `/keys`, `/cheatsheet` | Help overlay açar |
| `/providers` | `runPanelCommand` içinde Providers paneli açar; `runProviderCommand` içinde liste yazdırma davranışı da var, dispatcher sırasına göre panel command kullanılıyor |
| `/provider` | Provider command picker veya direkt provider/model switch |
| `/model` | Model picker veya direkt model switch |
| `/models` | Aktif provider model listesini chat'e yazar |
| `/tools` | Chat içi tool strip toggle; `/tools list` tool catalog yazar |
| `/tool` | Tool picker/spec/run yüzeyi |
| `/tasks` | Floating Tasks panel toggle |
| `/tasks list`, `/tasks tree`, `/tasks show <id>`, `/tasks roots` | Chat içine task raporu basar, panel açmaz |
| `/tasks clear` | Non-drive task store temizler |
| `/workflow`, `/todos`, `/subagents`, `/stats`, `/status`, `/log` | Chat içine diagnostic snapshot basar |
| `/reload` | Runtime config reload + status update |
| `/file` | File mention picker açar |
| `/context ...` | Context summary/messages/drop/preview komut ailesi |
| `/drive ...` | Workflow/drive run yönetimi; Workflow tabında görselleşir |

## Single-Letter Temizlik Listesi

Panel içinde panelden çıkaran veya ağır aksiyon yapan single-letter davranışları aşamalı kaldırılmalı/taşınmalı:

| Yer | Bugünkü davranış | Hedef |
|---|---|---|
| Files `i/e/v` | Chat'e geçip composer'a mention/prompt yazar | Action menu veya `Ctrl+Enter`/modifier'lı aksiyon |
| Patch `f` | Files tabına geçer | Action menu aksiyonu olarak kalsın, direkt single-letter kalksın |
| Activity `Enter/o/f/y/r` | Detay açar, başka panele atlar veya kopyalar | `Enter/Right` action menu, `Ctrl+...` explicit aksiyon |
| Status `Enter` | İlgili detay paneline atlar | `Right` action menu + explicit seçim |
| Security `f` | Fix prompt'u Chat'e taşır | Action menu ya da modifier |
| Conversations `L` | Conversation load edip Chat'e atlar | Action menu explicit load |
| Workflow action "copy ID" | Chat'e geçip composer'a ID yazar | Composer'a yazıp panelde kal veya action sonrası notice |
