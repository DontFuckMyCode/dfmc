# DFMC — Derin Proje Analiz Raporu

> Tarih: 2026-04-20 | Tarayan: DFMC Copilot | Kaynak: 363 dosya, 5663 graf düğümü
> **Son güncelleme:** H1, H2, H3, M2, M4, L2 (kısmi), L3 çözüldü (2026-04-20)

---

## 1. Proje Özeti

| Metrik | Değer |
|---|---|
| Modül | `github.com/dontfuckmycode/dfmc` |
| Go Versiyonu | 1.25.0 |
| Toplam `.go` dosyası | 200+ |
| Test dosyası | ~70 `_test.go` |
| Ana bağımlılıklar | bubbletea, tree-sitter, bbolt, golang.org/x/net, golang.org/x/time, yaml.v3 |
| Doğrudan dep sayısı | 16 |
| Dolaylı dep sayısı | 12 |
| Graf | 5663 düğüm, 7176 kenar, 0 döngü |
| Son yazar | Ersin KOÇ |

DFMC, LLM tabanlı bir AI kod asistanı motorudur. CLI, TUI (terminal UI) ve web arayüzü sunar. Tree-sitter ile AST analizi, kod grafiği oluşturma, güvenlik taraması, hook sistemi, otonom drive modu ve çoklu provider (OpenAI, Anthropic, Google, Alibaba vb.) desteği içerir.

---

## 2. Mimari Analiz

### 2.1 Katman Yapısı

```
cmd/dfmc/           → Uygulama giriş noktası
internal/engine/    → Çekirdek motor (69 dosya — en büyük modül)
internal/drive/     → Otonom drive döngüsü (17 dosya)
internal/tools/     → Araç motoru + 37 dosya (apply_patch, ast_query, delegate, git, web vb.)
internal/provider/  → LLM provider yönlendirici + throttle + stream
internal/security/  → Gizli anahtar + güvenlik açığı tarayıcı
internal/ast/       → Tree-sitter tabanlı AST motoru
internal/codemap/   → Kod bağımlılık grafiği
internal/config/    → YAML yapılandırma + doğrulama
internal/context/   → Bağlam penceresi yönetimi
internal/conversation/ → Konuşma kalıcılığı
internal/memory/    → Uzun süreli bellek deposu (3 dosya — küçük)
internal/hooks/     → Shell hook dispatch
internal/mcp/       → Model Context Protocol köprüsü
internal/intent/    → Niyet yönlendirici
internal/promptlib/ → Prompt kataloğu
internal/planning/  → Görev bölücü
ui/tui/             → Bubble Tea TUI (96 dosya — ikinci en büyük modül)
```

### 2.2 Kritik Bağımlılık Grafiği

```
Engine → {AST, CodeMap, Context, Conversation, Security, Tools, Provider, Memory, Hooks, Intent}
Tools.Engine → {Config}
Driver (Drive) → {Runner, Store, Publisher}
TUI.Model → {Engine (salt-okunur event tüketici)}
Provider.Router → {Config, Provider interface}
```

**Döngüsel bağımlılık yok** (graf: 0 döngü) — bu mimari açıdan temiz bir işaret.

### 2.3 Engine: Merkezî Yapı

`internal/engine/engine.go:63` — `Engine` struct, 12 ana bileşeni barındırır:
- `Config`, `Storage`, `EventBus`, `ProjectRoot`, `AST`, `CodeMap`, `Context`, `Providers`, `Tools`, `Memory`, `Conversation`, `Security`, `Hooks`, `Intent`
- Lock sıralaması dokümante edilmiş: `agentMu → mu` (deadlock önleme)
- `memoryDegraded` flag: Memory yüklemesi başarısız olursa motor degraded modda çalışır

**State Machine:** `StateCreated → StateInitializing → StateReady → StateServing → StateShuttingDown → StateStopped`

---

## 3. Modül Detayları

### 3.1 internal/engine (69 dosya)

| Alt-modül | Satır | Açıklama |
|---|---|---|
| `engine.go` | 489 | Merkezî yapı, Init, Shutdown, state yönetimi |
| `agent_loop.go` | ~420 | Araç döngüsü, payload trim, stream yardımcıları |
| `agent_loop_native.go` | ~600+ | Native araç çalıştırma, park/resume |
| `agent_loop_parallel.go` | ~200 | Paralel araç çalıştırma + önbellek |
| `agent_loop_phases.go` | ~185+ | Faz yardımcıları (cacheMu parametreli) |
| `agent_autonomy.go` | - | Otonom mod |
| `agent_coach.go` + `_emit.go` | - | Coach geri bildirim |
| `agent_compact.go` | - | Konuşma sıkıştırma |
| `agent_handoff.go` | - | Agent el sıkışma |
| `agent_parking.go` + `_parked.go` | - | Park edilmiş agent yönetimi |
| `subagent.go` | 71 | Subagent çalıştırma arayüzü |
| `approver.go` | - | İnsan-onay kapısı |
| `drive_adapter.go` | - | Drive ↔ Engine adaptörü |
| `eventbus.go` | - | Olay veriyolu |
| `engine_ask.go` | - | Ask giriş noktası |
| `engine_analyze.go` | - | Analiz giriş noktası |
| `engine_context.go` | - | Bağlam yönetimi |
| `engine_intent.go` | - | Niyet yönlendirme |
| `engine_prompt.go` | - | Prompt oluşturma |
| `engine_tools.go` | - | Araç kayıt/yönetim |
| `status_types.go` | - | Durum tip tanımları |

**Gözlem:** `engine.go` 489 satır — başarılı bir "god file" bölme refactoring geçirmiş (commit: `bb6753e5` "split engine.go god file into 7 domain modules"). Modülerlik iyi.

### 3.2 internal/drive (17 dosya)

| Dosya | Açıklama |
|---|---|
| `driver.go` | Ana döngü: plan → dispatch → drain → finalize (786+ satır) |
| `runner.go` | Runner interface: PlannerRequest/Response, ExecuteTodoRequest/Response |
| `planner.go` | Plan oluşturucu |
| `scheduler.go` | TODO DAG çizelgeleyici — `readyBatch` dosya kapsamı çakışma kontrolü yapar |
| `supervision.go` | Denetim mantığı |
| `persistence.go` | BoltDB kalıcılık |
| `events.go` | Olay tanımları |
| `types.go` | Run, Todo, Config, RunStatus tipleri |
| `expansion.go` | Plan genişletme |
| `verification.go` | Sonuç doğrulama |
| `registry.go` | Provider kayıt defteri |

**Gözlem:** `driver.go` en karmaşık dosya — paralel TODO dispatch, drainGraceWindow (2sn), MaxFailedTodos sonlandırma. İyi dokümantasyon.

### 3.3 internal/tools (37 dosya)

| Araç | Dosya |
|---|---|
| Merkez motor | `engine.go` (1062+ satır — en büyük tek dosya) |
| Araç spesifikasyonu | `spec.go`, `builtin_specs.go` |
| apply_patch | `apply_patch.go` |
| ast_query | `ast_query.go` |
| codemap | `codemap.go` |
| delegate | `delegate.go` |
| destructive | `destructive.go` |
| find_symbol | `find_symbol.go` |
| git | `git.go` |
| glob | `glob.go` |
| meta | `meta.go` |
| orchestrate | `orchestrate.go`, `orchestrate_dag.go` |
| web | `web.go` (SSRF koruması) |
| komut | `command.go` |

**`tools/engine.go` Sorun:** 1062 satır — okunabilirlik açısından bölünmeye aday. `normalizeToolParams` (495-607), `compressToolOutput` (737+), `writeFileAtomic` (1062+) gibi fonksiyonlar ayrı dosyalara taşınabilir.

### 3.4 internal/provider (~20 dosya)

Provider arayüzü: `Stream(ctx, messages, tools) <-chan StreamEvent`

| Provider | Dosya |
|---|---|
| OpenAI uyumlu | `openai_compat.go` + `_tools.go` |
| Anthropic | `anthropic.go` + `_tools.go` |
| Google | `google.go` + `_tools.go` |
| Alibaba | `alibaba_test.go` |
| Offline | `offline.go`, `offline_analyzer.go` |
| Router | `router.go` — throttle, fallback, Retry-After |
| Throttle | `throttle.go` |

**Sentinel hatalar:** `ErrProviderUnavailable`, `ErrProviderNotFound`, `ErrContextOverflow`, `ErrProviderThrottled` — iyi tasarlanmış hata hiyerarşisi.

### 3.5 ui/tui (96 dosya)

En büyük ikinci modül. Bubble Tea framework üzerine kurulu.

| Kategori | Dosyalar |
|---|---|
| Ana model | `tui.go`, `update.go` |
| Render | `render_layout.go`, `render_panels.go`, `render_chat_meta.go`, `render_status_helpers.go` |
| Chat | `chat_actions.go`, `chat_commands.go`, `chat_helpers.go`, `chat_key.go` |
| Slash komutları | `slash_handlers.go`, `slash_picker.go`, `slash_picker_modal.go` |
| Tema | `theme.go`, `color.go`, `tui_palette.go` |
| Panel | `context_panel.go`, `codemap.go`, `drive.go`, `memory.go`, `security.go`, `providers.go` |
| Onay | `approver.go` |
| Giriş | `input.go`, `mention.go`, `mention_helpers.go`, `composer_mentions.go` |
| Fark | `diff_sidebyside.go`, `patch_view.go`, `patch_parse.go` |
| Güvenlik | `secret_redact.go` |

---

## 4. Kod Kalitesi Metrikleri

### 4.1 Test Kapsamı

| Modül | Kaynak Dosya | Test Dosyası | Oran |
|---|---|---|---|
| engine | 22 | 47 | **2.1x** (her kaynak dosya başına 2+ test) |
| drive | 11 | 6 | **0.55x** |
| tools | 24 | 13 | **0.54x** |
| tui | ~60 | ~36 | **0.6x** |
| security | 6 | 3 | **0.5x** |
| provider | ~12 | ~8 | **0.67x** |
| ast | 9 | 4 | **0.44x** |
| codemap | 5 | 3 | **0.6x** |
| memory | 1 | 2 | **2.0x** |
| config | 4 | 3 | **0.75x** |
| context | 3 | 3 | **1.0x** |

**Genel test: kaynak oranı ≈ 0.7x** — engine modülü çok iyi kapsanmış, drive ve ast zayıf.

### 4.2 Mutex / Eşzamanlılık

- **16+ mutex alanı** tespit edildi (`sync.RWMutex` veya `sync.Mutex`)
- Tüm `Lock/Unlock` çiftleri `defer` ile korunuyor ✓
- Lock sıralaması engine.go'da dokümante: `agentMu → mu` ✓
- `conversation/manager.go:52-53`: çift mutex (`mu` + `saveMu`) — save işlemleri serileştirilmiş, snapshot tutarlılığı korunuyor ✓

### 4.3 Panic Kullanımı

- **Üretim kodunda sadece 1 `panic()`** çağrısı: `internal/commands/registry.go:236` — `RegistrationError` sarmalama
- Kalan 7 `panic()` hepsi test dosyalarında — kabul edilebilir ✓

### 4.4 context.Background / context.TODO

- Sadece **test dosyalarında** kullanılıyor (10 bulgu) — üretim kodunda yok ✓

### 4.5 init() Fonksiyonları

- **0 adet** `func init()` — temiz ✓

### 4.6 Shutdown / Close Yönetimi

| Bileşen | Metot |
|---|---|
| Engine | `Shutdown()` — aşamalı, hata raporlamalı |
| AST Engine | `Close()` |
| Tools Engine | `Close()` — tüm araçları iteratif kapatır |
| Storage (BoltDB) | `Close()` |
| PluginExec Client | `Close(ctx)` |
| AST Query Tool | `Close()` |
| Codemap Tool | `Close()` |
| Find Symbol Tool | `Close()` |

**Sorun:** Engine.Shutdown() `error` döndürmüyor — hataları `reportShutdownError` ile logluyor ama çağırana bildirmiyor.

---

## 5. Güvenlik Analizi

### 5.1 Güvenlik Tarayıcı

`internal/security/` iki katmanlı tarama sunar:
1. **Regex tarayıcı** (`scanner.go`) — gizli anahtar desenleri + güvenlik açığı desenleri
2. **AST tarayıcı** (`astscan.go` + `_credentials.go`, `_go.go`, `_javascript.go`, `_python.go`) — dile özel AST tabanlı tarama

Desteklenen diller: Go, JavaScript, Python

### 5.2 Koruma Mekanizmaları

| Mekanizma | Konum |
|---|---|
| SSRF koruması | `internal/tools/web.go` |
| Binary dosya koruması | `internal/tools/engine.go` |
| Read-before-mutation guard | `internal/tools/engine.go:398-437` |
| Path traversal koruması | `internal/tools/engine.go:964-1010` (`EnsureWithinRoot`) |
| Dosya kapsamı çakışma koruması | `internal/drive/scheduler.go` |
| Secret redaksiyon | `ui/tui/secret_redact.go` |
| İnsan-onay kapısı | `internal/engine/approver.go` |
| Hook güvenliği | README'de dokümante |

### 5.3 Potansiyel Riskler

1. **`writeFileAtomic`** (`tools/engine.go:1062`): Atomik yazma yapıyor ama geçici dosya yolu `EnsureWithinRoot` kontrolünden geçiyor mu? Doğrulanmalı.
2. **`command.go`**: Shell komut çalıştırma aracı — injection riski var, ancak approver kapısı var.
3. **Web aracı**: SSRF koruması mevcut ama `golang.org/x/net` ile DNS rebinding saldırılarına karşı ek koruma gerekebilir.

---

## 6. Performans ve Verimlilik

### 6.1 Önbellek

| Önbellek | Konum | Boyut |
|---|---|---|
| AST Parse Cache | `internal/ast/engine.go` | 10.000 giriş (LRU) |
| Tree-sitter Parser Pool | `internal/ast/treesitter_cgo.go` | sync.Pool (dil başına) |
| Read Snapshot Cache | `internal/tools/engine.go` | 256 giriş |
| Tool Failure Tracker | `internal/tools/engine.go` | 256 giriş |
| Tool Output Compress | `internal/tools/engine.go` | Dinamik byte limiti |
| Prompt Cache | Engine tarafı | Token bazlı |

### 6.2 Paralellik

- **Drive modu**: `MaxParallel` (varsayılan: 3) çalışan TODO sınırı
- **Tool execution**: `executeToolCallsParallel` — aynı turdaki araç çağrıları paralel yürütülür
- **Arka plan dizinleme**: `StartBackgroundTask` ile goroutine tabanlı

### 6.3 Bellek ve Kaynak

- BoltDB kalıcılık: Drive + Konuşma + Bellek depoları
- `bounded_buffer.go` (tools + hooks): Sınırsız büyüme önleme
- `trimToolPayload` + `truncateRunesWithMarker`: Büyük payload'ları kırparak bellek taşmasını önler
- `compactToolPayload`: Akıllı sıkıştırma (relevans terimlerini korur)

---

## 7. Tespit Edilen Sorunlar ve Öneriler

### 🔴 Yüksek Öncelik

| # | Sorun | Konum | Öneri |
|---|---|---|---|
| H1 | `tools/engine.go` 1062 satır — god file | `internal/tools/engine.go` | ✅ **Çözüldü (2026-04-20)** — `normalizeToolParams` → `params.go`, `compressToolOutput` + helpers → `output.go`, `writeFileAtomic` + `fileContentHash` → `fileutil.go` |
| H2 | `Engine.Shutdown()` error döndürmüyor | `internal/engine/engine.go:306` | ✅ **Çözüldü (2026-04-20)** — `Shutdown() error` imzasına geçti; tüm aşama hataları `errors.Join` ile döndürülüyor, EventBus + stderr bildirimi korundu |
| H3 | Drive modülü test oranı düşük (0.55x) | `internal/drive/` | ✅ **Çözüldü (2026-04-20)** — `scheduler_test.go` eklendi (8 test: scope conflict, unscoped exclusivity, read-only, blocked/skipped deps, parallelism) |

### 🟡 Orta Öncelik

| # | Sorun | Konum | Öneri |
|---|---|---|---|
| M1 | AST modülü test oranı düşük (0.44x) | `internal/ast/` | ✅ **Çözüldü (2026-04-20)** — `backend_test.go` eklendi (BackendStatus, ParseMetrics, metrics tracker reset/last-language testleri) |
| M2 | `commands/registry.go:236` panic kullanımı | `internal/commands/registry.go` | ✅ **Çözüldü (2026-04-20)** — `MustRegister` artık `(string, error)` dönüyor, panik yerine |
| M3 | Memory modülü çok küçük (3 dosya) | `internal/memory/` | Uzun süreli bellek için embedding/semantic search desteği eksik; sadece basit depo |
| M4 | Provider fallback zinciri belirsiz | `internal/provider/router.go` | ✅ **Çözüldü (2026-04-20)** — `ResolveOrder` fonksiyonuna detaylı doc eklendi; fallback sırası + ContextOverflow compaction stratejisi dokümante edildi |

### 🟢 Düşük Öncelik

| # | Sorun | Konum | Öneri |
|---|---|---|---|
| L1 | 40 TODO/FIXME/HACK yorum | Çeşitli | Drive modülü yoğun TODO içeriyor — planlı geliştirme işaretleri, acil değil |
| L2 | TUI modülü büyük (96 dosya) | `ui/tui/` | 🟡 **Kısmi çözüldü (2026-04-20)** — `ui/tui/theme/` alt-paketi oluşturuldu (~1650 satır render kodu `types.go`, `palette.go`, `render.go`'a taşındı); geri kalan dosyalar `Model` tipına sıkı bağlı (panel_states, engine_events) |
| L3 | `writeFileAtomic` güvenlik doğrulaması | `internal/tools/engine.go:1062` | ✅ **Çözüldü (2026-04-20)** — `EnsureWithinRoot` ile symlink escape koruması zaten mevcut; `TestWriteFileAtomic_EscapesViaSymlink` testi eklendi ve geçti |
| L4 | `offline_analyzer.go` statik analiz uyarıları | `internal/provider/` | panic() ve TODO marker tespiti var — bunlar çevrimdışı analiz için, üretim etkisi yok |

---

## 8. Mimari Güçlü Yönler

1. **Sıfır döngüsel bağımlılık** — temiz modüler yapı
2. **Lock sıralaması dokümante** — deadlock riski minimize
3. **Degraded mod desteği** — Memory yüklemesi başarısız olsa bile motor çalışır
4. **Atomik dosya yazma** — veri bütünlüğü korunuyor
5. **Okuma öncesi mutasyon guard** — blind edit önleme
6. **SSRF + path traversal koruması** — güvenlik katmanı güçlü
7. **Paralel TODO dispatch + dosya kapsamı kilidi** — drive modunda veri yarışı önleme
8. **Throttle + Retry-After desteği** — provider hata yönetimi olgun
9. **Sentinel hata hiyerarşisi** — `ErrContextOverflow`, `ErrProviderThrottled` ile tip-güvenli hata işleme
10. **Tree-sitter pool** — CGO parser nesneleri `sync.Pool` ile yönetiliyor, bellek sızıntısı önleniyor

---

## 9. Teknoloji Yığını Değerlendirmesi

| Teknoloji | Değerlendirme |
|---|---|
| Go 1.25 | Güncel, generics + gelişmiş stdlib |
| Bubble Tea | TUI framework olgun, topluluk büyük |
| Tree-sitter (CGO) | Güçlü AST desteği; pool yönetimi kritik |
| BoltDB | Gömülü KV deposu; basit ve güvenilir |
| golang.org/x/time | Rate limiting için standart |
| golang.org/x/net | SSRF koruması için gerekli |
| yaml.v3 | Yapılandırma için yeterli |

---

## 10. Sonuç

DFMC, mimari açıdan **olgun ve iyi yapılandırılmış** bir proje. Döngüsel bağımlılığın olmaması, lock sıralamasının dokümante edilmesi, degraded mod desteği ve kapsamlı güvenlik katmanları, projenin production kalitesinde olduğunu gösteriyor.

**Ana riskler:**
1. `tools/engine.go` god file — okunabilirlik ve bakım riski
2. Drive ve AST modüllerinin test kapsamı düşük — paralel dispatch ve pool yönetimi edge-case'leri yakalanmayabilir
3. `commands/registry.go` panic kullanımı — beklenmeyen crash riski

**Önerilen öncelik sırası:** H1 (tools bölme) → H3 (drive test) → M2 (panic kaldırma) → H2 (Shutdown error) → M1 (AST test)
