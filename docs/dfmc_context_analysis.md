# DFMC Context Window Yönetimi — Analiz Raporu

**Tarih:** 2026-05-02
**Kapsam:** `internal/context/`, `internal/engine/engine_context*.go`, `internal/tokens/`, `internal/engine/agent_compact*.go`
**Dosya sayısı:** context=28, engine=143, tokens=2

---

## 1. Genel Mimari

Context window yönetimi **4 katmanlı** bir pipeline üzerinde çalışıyor:

```
Soru
  │
  ▼
┌─ Trajectory Detection ────────────── task tipini tanı
│   (trajectory_detect.go, trajectory.go)
│
├─── Budget Preview ────────────────── kaç token alabiliriz?
│   (engine_context.go, engine_context_helpers_reserve.go)
│
├─── Build Options ────────────────── retrieval stratejisi belirle
│   (manager_build.go → BuildOptions)
│
├─── Reserve Breakdown ──────────────── Prompt/History/Response/Tool paylaştır
│   (contextReserveBreakdown)
│
├─── Context Assembly ──────────────── chunks, history, prompt birleştir
│   (manager_build.go, prompt_render.go)
│
├─── Budget Trim ────────────────────── token aşımı varsa kes
│   (budget_trimmer.go)
│
└─── Compact (gerekiyorsa) ──────────── history özetle
    (agent_compact_rounds.go, agent_compact_summary.go)
```

---

## 2. Trajectory Detection (Görev Tipi Tespiti)

**Dosya:** `internal/context/trajectory.go` (248 satır) + `trajectory_detect.go` (272 satır)

### Görev Profilleri

Sistem **6 farklı trajectory tipi** tanıyor:

| Tip | Açıklama | Budget yaklaşımı |
|-----|----------|-----------------|
| `plan` | Planlama, strateji oluşturma | Orta-yüksek history |
| `act` | Dosya okuma/yazma, tool kullanımı | Düşük history, yüksek file context |
| `debug` | Hata ayıklama | Orta history + hata trace |
| `explain` | Kod açıklama | Düşük history, yüksek doc/file |
| `review` | Code review | Orta history + file context |
| `onboard` | Codebase walkthrough | Yüksek history, geniş kapsam |

### Detection Mekanizması

```go
// trajectory_detect.go — pattern-based detection
detectRepeatedCalls(allTraces)      // aynı tool tekrar tekrar çağrılıyor mu?
detectRepeatedFailures(allTraces)  // tekrar eden başarısızlıklar
countUnvalidatedMutations(trace)   // doğrulanmamış dosya değişiklikleri
buildRoundSummary(fresh)           // son round'dan özet çıkar
```

Detection sonucu `TrajectoryHints` üretiyor — **max 2 hint/turn**, her biri 1-2 cümle ("minik dokunuşlar").

> Tasarım notu: Hint'ler **observable facts** üzerinden gider (tool name, arg values, output size, error text). Yorum yok, hallucinasyon yok.

### Coach Emit Sistemi

`agent_coach_emit_hints.go` trajectory hints'leri LLM prompt'una enjekte ediyor. Hint'ler stateless per-turn — caller dedup yapmakla sorumlu (`recentHints` tracking).

---

## 3. Tokenizasyon

**Dosya:** `internal/tokens/counter.go` (231 satır)

### Counter Interface

```go
type Counter interface {
    Count(text string) int
    CountMessages(msgs []Message) int
}
```

### Implementasyonlar

| Tip | Durum | Not |
|-----|-------|-----|
| **Heuristic (default)** | ✅ Aktif | Zero-dependency, model-agnostic. Go-based approximation |
| **Anthropic count_tokens API** | 🔌 Takılabilir | `Counter` interface üzerinden register edilebilir |
| **tiktoken** | 🔌 Takılabilir | OpenAI modelleri için |

### Heuristic Counter Detayları

```go
// Model-agnostic approximation:
// - Unicode-aware rune counting
// - Synchronized singleton (sync.Once)
// - No external dependencies
```

**Değerlendirme:** Gerçek tiktoken/Cl100k_base değil. Bu demek ki budget hesaplamaları **mutlak doğru değil**, sadece yaklaşık. Dar window'larda (32K-128K) sorun olabilir; çok büyük window'larda (1M) fark tolere edilebilir.

---

## 4. Context Budget Hesaplaması

**Dosya:** `internal/engine/engine_context.go` (313 satır)

### Constants

```go
const (
    defaultContextTotalCapTokens  = 16000   // varsayılan cap
    minContextTotalBudgetTokens   = 512     // minimum budget
    minContextPerFileTokens       = 96      // dosya başına minimum
    minContextFiles               = 2       // en az kaç dosya
    maxContextFiles               = 64      // maksimum dosya sayısı
    defaultResponseReserveTokens  = 2048    // response için ayrılan
)
```

### Reserve Breakdown

Toplam provider limitinden şu kesintiler yapılıyor:

| Alan | Minimum | Maksimum | Açıklama |
|------|---------|----------|----------|
| Base Prompt | ~900 | ~900 | Sabit, değişmez |
| History | 4096 | 32768 | Task tipine göre adaptive |
| Response | 2048 | 16384 | Proaktif response space ayırma |
| Tool | 512 | 512 | Tool call overhead |

**Hesaplama akışı:**

```
providerMaxContext()
    ↓
contextReserveBreakdown() → totalReserve
    ↓
available = providerLimit - totalReserve
    ↓
trimBundleToBudget(available)
```

### Dynamic Floor (budget_trimmer.go)

```go
// trimBundleToBudget — budama sırası:
// 1. Dynamic floor = max(25% * budget, 180 token, actualDynamicSize)
//    (user query + per-request context korunur)
// 2. Cacheable prefix (policy text) daha agresif kesilir
// 3. Her dosya minimum 96 token alır
```

**Tasarım kararı:** Dynamic section'lar (user query, per-request context) kesilmez — kaybetmek tüm prompt'u anlamsız kılar. Cacheable section'lar (policy, system prompt) agresif kesilir.

---

## 5. Context Retrieval & Assembly

**Dosya:** `internal/context/manager_build.go`

### BuildOptions Yapısı

```go
type BuildOptions struct {
    TaskType        TaskType          // plan/act/debug/explain/review/onboard
    Mode            RetrievalMode     // none/standard/aggressive
    MaxTokens       int               // token bütçesi
    MaxFiles        int               // maksimum dosya sayısı
    SymbolAware     bool              // codemap symbol graph kullan
    GraphDepth      int               // transitive closure derinliği
    ExplicitMentions []string         // [[file:path]] marker'ları
}
```

### Retrieval Modları

| Mod | Davranış |
|-----|----------|
| `none` | Sadece explicit mentions + history |
| `standard` | Semantic search + codemap traverse |
| `aggressive` | Dar window'larda maximum relevance, minimum noise |

### Explicit Mentions

`[[file:path]]` formatındaki marker'lar budget cap'i bypass ediyor. Doğrudan context'e ekleniyor, token hesabına tabi değil.

---

## 6. History Yönetimi ve Compaction

**Dosya:** `internal/engine/agent_compact*.go` (4 dosya)

### Round Kavramı

Bir "round" = 1 assistant turn + takip eden consecutive user tool_result mesajları.

### Compaction Trigger Noktaları

```
maybeCompact()    → koşullu, budget kritikse
proactiveCompact()→ agent devam ederken otomatik tetiklenir
forceCompact()    → kullanıcı zorlar
```

### Compact Mekanizması (agent_compact_rounds.go)

```go
// 1. Round'ları split et (tool_round_splitting)
// 2. patchUnresolvedToolUses():
//    - Assistant tool_call'lerin yanında tool_result yoksa
//      → synthetic ToolError inject et
//    - Anthropic rejection'ı önlemek için gerekli
// 3. Eski round'ları summary'ye dönüştür
//    → agent_compact_summary.go: per-round line + per-tool-call line + result excerpt
```

### History Budget Genişlemesi

Eski tasarım: **1200-2048 token** (history amnesia problemi vardı)
Yeni tasarım: **4096-32768 token** (task profile'a göre adaptive)

> 📌 Önemli iyileştirme: History amnesia problemi çözülmüş.

---

## 7. Compression Stratejileri

**Dosya:** `internal/context/compress.go`

| Seviye | Kullanım | Yaklaşım |
|--------|----------|----------|
| `none` | Büyük window, yeterli alan | Kesme yok |
| `standard` | Normal operation | Orta düzey budama |
| `aggressive` | Dar window, kritik budget | Maximum relevance, minimum noise |

---

## 8. Inspector — Context İnceleme

**Dosya:** `internal/context/inspector.go`

`PromptBundle` yapısını token/byte bazında breakdown ediyor:

- Cacheable section boyutu
- Dynamic section boyutu
- Dosya başına ortalama token
- Toplam chunk sayısı

Inspector output'u TUI'da `ContextInStatus` olarak gösteriliyor.

---

## 9. Context Events (UI Feedback)

**Dosya:** `ui/tui/activity.go` — `activityKindCtx`

TUI'da anlık context durumu gösteriliyor:
- Token usage
- Dosya sayısı
- Compression durumu
- Budget remaining

---

## 10. Güçlü Yanlar

| Özellik | Dosya | Değerlendirme |
|---------|-------|---------------|
| **Adaptive budget scaling** | `trajectory.go` | Task'e göre 4096-32K token allocate |
| **Dynamic floor koruması** | `budget_trimmer.go` | Query kesilmez, sadece policy budanır |
| **Explicit mentions** | `symbol_expand.go` | `[[file:path]]` budget bypass |
| **Codemap symbol graph** | `codemap/graph*.go` | Transitive closure ile ilgili dosyaları çeker |
| **History compaction** | `agent_compact*.go` | Amnezi problemi çözülmüş (4096-32K) |
| **Provider abstraction** | `internal/provider/` | Her modelin MaxContext()'i ayrı tanımlı |
| **Coach hints** | `agent_coach*.go` | Observable facts, max 2 hint/turn |
| **Multi-mode compression** | `compress.go` | Dar window'larda aggressive mod |

---

## 11. Riskler ve İyileştirme Alanları

### 🔴 Yüksek Öncelik

| Risk | Açıklama | Öneri |
|------|----------|-------|
| **Heuristic tokenization** | Gerçek tiktoken değil, budget hesabı yaklaşık | Provider için gerçek `count_tokens` API kullan |
| **Trajectory detection accuracy** | Yanlış task sınıflandırması → yanlış budget | Test coverage artırılmalı |
| **History compaction threshold** | Tetikleme noktası net değil | `maybeCompact()` koşulları explicit docs ile belgelenmeli |

### 🟡 Orta Öncelik

| Risk | Açıklama | Öneri |
|------|----------|-------|
| **int overflow ( clampInt)** | Büyük token değerlerinde | `int` yerine `int64` kullanımı kontrol edilmeli |
| **Codemap parse maliyeti** | 1037 dosya, 10392 symbol — ilk yükleme | Lazy loading veya background pre-parse |
| **Tool result truncation** | `elasticToolTokensRatio = 0.60` — dinamik kesme | Kesme noktası daha predictable olmalı |

### 🟢 Düşük Öncelik

| Risk | Açıklama | Öneri |
|------|----------|-------|
| **Provider-specific quirks** | Her provider'ın farklı tokenization'ı | Provider başına token counter register |
| **Snapshot overhead** | Her request için snapshot üretimi | Debug mode'a conditional tut |

---

## 12. Mimari Temizlik Önerileri

### Yapısal Temizlik

1. **`tokens/` paketi eksik** — `pkg/tokens` dizini var ama içi boş (0 dosya). Token counter'lar `internal/tokens/counter.go`'da. Tek bir tutarlı paket altında toplanabilir.

2. **Naming tutarsızlığı:**
   - `contextReserveBreakdown` → `ReserveBreakdown` (struct olarak)
   - `trajectory_detect.go` aslında detection helpers değil, **rule engine input helpers**

3. **Dosya boyutu dağılımı:**
   - `engine/` altında 143 dosya — tekil sorumluluk var ama grouping net değil
   - `context/` altında 28 dosya — iyi organize

### Kanonik Akış Belgeleme

```
Question
  │
  ▼ detectContextTask() ──────────────────┐
  │   trajectory.go: contextTaskProfile   │
  ▼                                      │
  │ contextBuildOptionsWithRuntime()     │
  │   manager_build.go: BuildOptions      │
  ▼                                      │
  │ providerMaxContextForRuntime()        │
  │   engine_context_helpers_reserve.go   │
  ▼                                      │
  │ contextReserveBreakdown()             │
  │   → Prompt/History/Response/Tool       │
  ▼                                      │
  │ BuildPrompt()                         │
  │   manager_build.go: assembleChunks    │
  │   prompt_render.go: render            │
  ▼                                      │
  │ trimBundleToBudget() ← budget aşımı   │
  │   budget_trimmer.go                   │
  ▼                                      │
  │ [Agent Loop]                          │
  ▼                                      │
  │ maybeCompact() ← history > threshold  │
  │   agent_compact_rounds.go             │
  ▼                                      │
  Response + updated history
```

---

## 13. Sonuç

**Context window yönetimi genel olarak olgun ve iyi tasarlanmış.** Katmanlı yapı, adaptive budget, dynamic floor koruması ve history compaction ile ciddi bir mimari ortaya çıkmış.

**En kritik iyileştirme:** Token counter'ın heuristic yerine provider-specific (tiktoken/Cl100k_base, Anthropic count_tokens) implementasyona geçirilmesi. Bu olmadan budget hesaplamaları her zaman yaklaşık kalacak — özellikle 32K gibi dar window'larda ciddi sapmalar olabilir.

**İkinci öncelik:** Trajectory detection rule'larının test coverage'ı — yanlış görev sınıflandırması tüm pipeline'ı etkiler.