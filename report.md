# DFMC — Derin Proje Analiz Raporu

> Tarih: 2026-04-21 | Tarayan: DFMC Copilot | Kaynak: 200+ `.go` dosya, 199 test dosyası, 5663 graf düğümü

---

## 1. Proje Özeti

| Metrik | Değer |
|---|---|
| Modül | `github.com/dontfuckmycode/dfmc` |
| Go Versiyonu | 1.25.0 |
| Kaynak `.go` dosyası | 200+ |
| Test dosyası | 199 `_test.go` |
| Ana bağımlılıklar | bubbletea, tree-sitter, bbolt, golang.org/x/net, golang.org/x/time, yaml.v3 |
| Doğrudan dep sayısı | 16 |
| Dolaylı dep sayısı | 12 |
| Graf | 5663 düğüm, 7176 kenar, 0 döngü |
| Son yazar | Ersin KOÇ |

DFMC, LLM tabanlı bir AI kod asistanı motorudur. CLI, TUI (terminal UI) ve web arayüzü sunar. Tree-sitter ile AST analizi, kod grafiği oluşturma, güvenlik taraması, hook sistemi, otonom drive modu ve çoklu provider (OpenAI, Anthropic, Google, Alibaba, Z.AI, MiniMax vb.) desteği içerir.

---

## 2. Mimari Analiz

### 2.1 Katman Yapısı

```
cmd/dfmc/           → Uygulama giriş noktası
internal/engine/    → Çekirdek motor (~69 dosya — en büyük modül)
internal/drive/     → Otonom drive döngüsü (17 dosya)
internal/tools/     → Araç motoru + 37 dosya (apply_patch, ast_query, delegate, git, web vb.)
internal/provider/  → LLM provider yönlendirici + throttle + stream
internal/security/  → Gizli anahtar + güvenlik açığı tarayıcı (AST + regex)
internal/ast/       → Tree-sitter tabanlı AST motoru (CGO + regex fallback)
internal/codemap/   → Kod bağımlılık grafiği (düğüm/kenar ekleme, döngü tespiti)
internal/config/    → YAML yapılandırma + doğrulama + models.dev senkronizasyonu
internal/context/   → Bağlam penceresi yönetimi + sembol genişletme
internal/conversation/ → Konuşma kalıcılığı + dallanma
internal/memory/    → Uzun süreli bellek deposu (3 dosya — küçük)
internal/hooks/     → Shell hook dispatch (session_start, pre_tool, post_tool)
internal/mcp/       → Model Context Protocol köprüsü
internal/intent/    → Niyet yönlendirici (resume vs. new vs. clarify)
internal/promptlib/ → Prompt kataloğu + istatistik
internal/planning/  → Görev bölücü
internal/supervisor/→ Süpervizör + köprü + yürütücü
internal/tokens/    → Token sayacı
internal/storage/   → bbolt destekli kalıcı depo
internal/skills/    → Yetenek kataloğu
internal/pluginexec/→ Plugin çalıştırıcı
internal/coach/     → Koç geri bildirim motoru
ui/tui/             → Bubble Tea TUI (99 dosya — ikinci en büyük modül)
ui/cli/             → CLI yüzeyi
ui/web/             → Web API + Workbench
pkg/types/          → Paylaşılan tipler (Message, Symbol, SafeGo)
```

### 2.2 Kritik Bağımlılık Grafiği

```
Engine → {AST, CodeMap, Context, Conversation, Security, Tools, Provider, Memory, Hooks, Intent}
Tools.Engine → {Config, SubagentRunner}
Driver (Drive) → {Runner, Store, Publisher}
TUI.Model → {Engine (salt-okunur event tüketici)}
Provider.Router → {Config, Provider interface}
CodeMap → AST
Context → CodeMap
```

**Döngüsel bağımlılık yok** (graf: 0 döngü) — bu, modülerlik için güçlü bir işaret.

### 2.3 Engine Bölünmesi

`internal/engine/` daha önce tek bir god file idi; şimdi 7 alan dosyasına bölündü:

| Dosya | Sorumluluk |
|---|---|
| `engine.go` | Yapı, yaşam döngüsü, durum |
| `engine_tools.go` | ListTools, CallTool, onay + hook + panic-guard |
| `engine_context.go` | Bağlam bütçesi, öneri, sıkıştırma |
| `engine_prompt.go` | Prompt önerileri, sistem blokları |
| `engine_ask.go` | Ask / StreamAsk, geçmiş bütçesi |
| `engine_passthrough.go` | Status, Memory, Conversation, config reload |
| `engine_analyze.go` | Ölü kod tespiti, karmaşıklık puanı |

---

## 3. Güvenlik Değerlendirmesi

### 3.1 Gizli Anahtar Yönetimi ✅

- `.env` ve `.env.local` dosyaları `.gitignore`'da (satır 23-24).
- Config yükleyici, çevresel değişkenleri `.env` dosyasına göre önceliklendirir.
- Security scanner, AST-tetiğli credential tespiti yapar (`astscan_credentials.go`).
- **Uyarı:** `.env.example`'daki `<your-key-here>` yer tutucuları boş string olarak ayrıştırılmaz; `parseDotEnvValue` literal değeri döndürür. Kullanıcı kopyalayıp düzenlemezse karmaşık hata mesajı alır.

### 3.2 Komut Çalıştırma Politikası ✅

- `security.sandbox.allow_command` önerilen yapılandırma.
- Eski `allow_shell` geriye uyumlu ama tüm `run_command` aracını devre dışı bırakır.
- SSRF koruması web_fetch aracında mevcut.

### 3.3 AST-Akıllı Güvenlik Tarayıcı ✅

- Regex tarayıcı + AST tarayıcı çift katmanlı.
- Tüm argümanları literal olan `exec.Command` çağrıları güvenli olarak işaretlenir (false positive azaltma).
- Dil bazlı kurallar: Go, JavaScript, TypeScript, Python.

---

## 4. Kod Kalitesi

### 4.1 Test Kapsamı

| Modül | Kaynak Dosya | Test Dosyası | Oran |
|---|---|---|---|
| engine | ~25 | ~30 | 1.2 |
| tools | ~15 | ~15 | 1.0 |
| drive | 13 | 6 | 0.5 |
| provider | ~12 | ~12 | 1.0 |
| tui | ~60 | ~45 | 0.75 |
| config | 4 | 3 | 0.75 |
| security | 7 | 3 | 0.4 |
| memory | 3 | 3 | 1.0 |
| hooks | 5 | 3 | 0.6 |
| codemap | 4 | 3 | 0.75 |

**Toplam test/kaynak oranı: ~199/200+ ≈ 0.95** — Yüksek.

### 4.2 Zayıf Test Kapsamlı Alanlar

| Öncelik | Modül | Sorun |
|---|---|---|
| **H** | `internal/memory/` | 3 dosya ama küçük — bellek yolsuzluk kurtarma senaryoları eksik |
| **H** | `internal/security/` | AST tarama testleri dil başına yetersiz; `astscan_javascript.go` / `astscan_python.go` test edilmiyor |
| **M** | `internal/hooks/` | PGID/Windows dal testleri sınırlı |
| **M** | `internal/supervisor/` | 3 test dosyası var ama entegrasyon senaryoları zayıf |
| **L** | `internal/mcp/` | Protocol parsing testleri eksik |

### 4.3 Kilitleme Düzeni

Engine'de açık kilitleme sırası belgelenmiş:
```
1. agentMu — agent lifecycle + parked state
2. mu      — general state

Kural: agentMu tutuluyorken mu alınmaz.
```
Bu, deadlock riskini önemli ölçüde azaltır. `engine.go:95-99`

---

## 5. Performans ve Ölçeklenebilirlik

### 5.1 AST Önbellek ✅

- LRU önbellek (`defaultParseCacheSize = 10000`) `internal/ast/engine.go` içinde.
- Hash bazlı invalidation (`fnv.New64()`) dosya değişikliklerinde girdi yeniler.
- Tree-sitter parser havuzu (`sync.Pool`) CGO tahsisini azaltır.

### 5.2 Araç Okuma Anlık Görüntüleri ✅

- `readSnapshots` + `readSnapshotLRU` (max 256) araç motorunda.
- Son okunan dosya içerikleri önbellekte; tekrar okuma I/O atlanır.

### 5.3 Provider Throttle ✅

- `internal/provider/throttle.go` — rate limiting + backoff.
- `golang.org/x/time/rate` promote edilmiş, artık doğrudan kullanılıyor.

### 5.4 Potansiyel Darboğazlar

| Alan | Risk | Açıklama |
|---|---|---|
| CodeMap indeksleme | **M** | Büyük projelerde başlangıç indeksleme uzun sürebilir; ilerleme bildirimi var ama iptal yavaş |
| TUI render | **L** | 99 dosya ile bubbletea Update/Render karmaşıklığı artıyor; ancak chat stream optimize edilmiş |
| bbolt tek yazıcı | **L** | bbolt tek yazıcı kısıtlaması var; ama Engine zaten mutex korumalı |

---

## 6. Yapılandırma ve Çevre

### 6.1 Config Katmanları

```
1. Yerleşik varsayılanlar (internal/config/defaults.go)
2. ~/.dfmc/config.yaml (genel)
3. <project>/.dfmc/config.yaml (proje)
4. Çevresel değişkenler (ANTHROPIC_API_KEY, OPENAI_API_KEY, vb.)
5. .env dosyası (otomatik yüklenir, env vars öncelikli)
6. CLI bayrakları
```

### 6.2 Provider Desteği

| Provider | Protokol | Durum |
|---|---|---|
| Z.AI / GLM | OpenAI-compatible | ✅ Anthropic URL otomatik remap |
| MiniMax | OpenAI-compatible | ✅ |
| Alibaba / Qwen | OpenAI-compatible | ✅ |
| OpenAI | OpenAI-compatible | ✅ |
| Anthropic | Anthropic native | ✅ Tool use destekli |
| Google | Google AI native | ✅ Tool use destekli |
| Offline | Yerel | ✅ Otomatik fallback |

### 6.3 Z.AI URL Remap

`provider/router.go:44-49`: Z.AI Anthropic-uyumlu URL'ler otomatik olarak OpenAI-uyumlu `/api/paas/v4` yüzeyine yeniden yönlendirilir. Kullanıcı yanlış URL yapıştırırsa 404 engellenir.

---

## 7. Otonom Drive Modu

### 7.1 Mimarisi

```
Kullanıcı → dfmc drive "<task>"
  → Planner (LLM çağrısı) → DAG oluşturma (TODO'lar + bağımlılıklar)
  → Scheduler → Hazır TODO'ları sırayla yürütme
  → Runner → Engine.Ask üzerinden her TODO'yu alt konuşma ile çalıştırma
  → Verification → Sonuç doğrulama
  → Persistence → Her durum geçişinde otomatik kayıt
```

### 7.2 TODO Yaşam Döngüsü

```
pending → running → done
                   → blocked (tüm yeniden denemeler bittikten sonra)
pending → skipped (bağımlılık blocked ise)
```

### 7.3 Paralellik Desteği

- Faz 1: Sıralı tek provider
- Faz 2: Dosya kapsamı çakışma tespiti ile paralellik (`FileScope` alanı)
- Faz 3: TODO başına provider yönlendirme (`ProviderTag` alanı — şu an borulanmış ama kullanılmıyor)

---

## 8. TUI (Terminal UI)

### 8.1 Yapısı

99 dosya ile ikinci en büyük modül. Ana bileşenler:

- `tui.go` — Ana model ve giriş noktası
- `chat_*.go` — Sohbet render, tuş bağlamaları, eylemler
- `slash_*.go` — Slash komut işleyicileri
- `codemap.go` — Kod haritası paneli
- `drive.go` — Drive ilerleme paneli
- `security.go` — Güvenlik tarama görünümü
- `context_panel.go` — Bağlam penceresi paneli
- `memory.go` — Bellek yönetimi görünümü
- `approver.go` — Araç onay mekanizması
- `mention*.go` — @mention sistemi (dosya, sembol, provider)
- `patch_*.go` — Patch önizleme ve düzenleme

### 8.2 Özellikler

- Akıcı sohbet kaydırma ve akıllı sarma
- Canlı istatistik paneli (animasyonlu)
- Slash komut kataloğu
- Provider geçişi ve durum çubuğu
- Konuşma dallanma ve dışa aktarma
- Onay rozeti ve onay akışı
- Gizli anahtar sansürü
- Panik koruma
- Fare etkileşim açma/kapama

---

## 9. Son Değişiklikler (Git Geçmişi)

| Commit | Açıklama |
|---|---|
| `bc5ce59` | refactor(tools,tui,engine,commands,provider,ast,drive): H1/H2/H3/M1/M2/M4/L2/L3 çözüldü |
| `aad1acc` | refactor(config,provider,tools): golang.org/x/time promote, maps.Clone, döngü basitleştirme |
| `0647d35` | docs(coach,ast,README): Close method, drive:run:warning event |
| `8360744` | refactor(ast,config,drive): LRU önbellek, project-hook guard, panic recovery, regex hoist |
| `164285b` | refactor(tools): SSRF guard, LRU önbellek fix, binary-file guard |
| `514fd40` | refactor(tools): actionable missing-param errors, non-blocking stream, autonomous park flag |
| `ba72395` | refactor: multi-edge graph bug fix, tree-sitter pool hardening, intent/resume config |
| `bb6753e` | refactor(engine): god file'ı 7 alan modülüne bölme |
| `021d33e` | refactor(engine): runNativeToolLoop → named phase helpers |
| `6bd38f9` | feat(tui): smoother chat scroll, smarter wrap, animated stats panel |

---

## 10. Öneriler

### 🔴 Yüksek Öncelik

| # | Öneri | Gerekçe |
|---|---|---|
| H1 | `.env.example` yer tutucu doğrulama | `<your-key-here>` literal olarak ayrıştırılıyor; kullanıcı karışıklığına neden oluyor. Config yükleyicide `<>` kalıpları reddedilmeli |
| H2 | Security AST tarama test genişletme | JavaScript/Python AST kuralları (`astscan_javascript.go`, `astscan_python.go`) yeterince test edilmiyor |
| H3 | Memory yolsuzluk kurtarma testleri | `memoryDegraded` kod yolu var ama test edilmiyor |

### 🟡 Orta Öncelik

| # | Öneri | Gerekçe |
|---|---|---|
| M1 | Drive Phase 3 provider yönlendirme | `ProviderTag` alanı borulanmış ama kullanılmıyor; TODO başına provider seçimi büyük projelerde verimlilik artırır |
| M2 | Supervisor entegrasyon testleri | Köprü ve yürütücü birim testleri var ama uçtan uca senaryolar eksik |
| M3 | Hook PGID/Windows testleri | Platform-specific dallar yetersiz test edilmiş |
| M4 | CodeMap indeksleme ilerleme | Büyük projelerde ilerleme bildirimi iyileştirilmeli; iptal gecikmeli olabilir |

### 🟢 Düşük Öncelik

| # | Öneri | Gerekçe |
|---|---|---|
| L1 | MCP protocol parsing testleri | Basit protocol ama test yok |
| L2 | TUI dosya sayısının kontrol altına alınması | 99 dosya ile en büyük modül; düzenleme zorluğu artıyor |
| L3 | bbolt yedekleme mekanizması | Veri kaybı riski minimal ama yedekleme yok |
| L4 | Config validator genişletme | Geçersiz provider profilleri için daha fazla doğrulama |

---

## 11. Özet Matrisi

| Kategori | Durum | Not |
|---|---|---|
| Mimari | ✅ İyi | Modüler, döngüsüz, açık bağımlılık grafiği |
| Güvenlik | ✅ İyi | .env gitignore, SSRF guard, AST scanner |
| Test Kapsamı | ✅ İyi | %95 test/kaynak oranı |
| Performans | ✅ İyi | LRU önbellek, parser havuzu, throttle |
| Hata Yönetimi | ✅ İyi | Graceful degradation, panic guard, event bus |
| Yapılandırma | ⚠️ İyi | .env yer tutucu sorunu dışında sağlam |
| Drive Modu | ⚠️ İyi | Phase 3 tamamlanmamış |
| TUI | ⚠️ İyi | Fonksiyonel ama dosya sayısı yüksek |

**Genel Değerlendirme:** Proje mimari olarak sağlam, güvenlik önlemleri yeterli, test kapsamı yüksek. Ana risk alanları: .env yer tutucu doğrulama, security AST test coverage ve drive Phase 3 tamamlama.

---

*Rapor otomatik olarak DFMC Copilot tarafından üretilmiştir.*
