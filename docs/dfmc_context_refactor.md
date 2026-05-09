# DFMC Context Refactor — Context Window System Analysis

**Tarih:** 2026-05-02  
**Konu:** Context window composition, provider abstraction, internal state management  

---

## Durum Özeti

Mevcut DFMC context window sistemi üç katmanda çalışıyor:

| Katman | Sorumlu | Kötümser Alan |
|--------|---------|---------------|
| Provider (LLM) | Token bütçesi yönetimi, `max_tokens` | Model family farklılıkları (o3 vs GPT-4) |
| Internal Engine | Tool call parsing, tool result char limits | Agent loop recursion derinliği |
| UI/TUI | Render, truncate, activity log | Büyük dosya okumaları `maxActivityEntries = 2000` |

---

## 1. Context Kaynakları — Mevcut Durum

### 1.1 Dosya İçerikleri (read_file)

```go
// internal/tools/builtin_read.go
const maxFileSize = 10 << 20  // 10 MB hard limit
const readFileBinaryCheckBytes = 512
```

- **Encoding detection:** UTF-8, UTF-16LE/BE BOM destekli
- **Binary filter:** İlk 512 byte'a bakarak karar veriliyor
- **Satır aralığı:** `line_start`/`line_end` ile segment okuma mevcut
- **Sorun:** 10 MB üstü dosyalar için hiçbir fallback yok

### 1.2 Tool Result Composition

```go
// internal/engine/agent_loop_limits.go
const (
    defaultMaxNativeToolTokens      = 250000
    defaultMaxNativeToolResultChars = 3200
    defaultMaxNativeToolDataChars   = 1200
    elasticToolTokensRatio          = 0.60
    elasticToolResultCharsRatio     = 1.0 / 40.0
    elasticToolDataCharsRatio       = 1.0 / 100.0
    toolRoundSoftCap                = 15
    toolRoundHardCap                = 30
)
```

**Formül:**
```
actual_chars = min(limit, budget * ratio)
```

### 1.3 Activity/Chat Timeline

```go
// ui/tui/activity.go
const maxActivityEntries = 2000
const activityDefaultRenderHeight = 24
```

- Activity log **2000 entry** tutuyor
- Render height **24 satır** ile sınırlı
- Activity kind'ler: `info`, `agent`, `tool`, `stream`, `error`, `context`, `index`

---

## 2. Sorunlu Noktalar

### 2.1 Context Sadece Dosyalardan İbaret Değil

Mevcut analiz sadece `read_file` üzerinden dosya bazlı context görüyor. Oysa context şunları da içermeli:

| Kaynak | Durum | Eksik |
|--------|-------|-------|
| `codemap` output | Çalışıyor | Sembol graph'ı token aşıyor |
| `grep_codebase` results | Çalışıyor | Snippet uzunluğu sınırsız |
| Tool call history | Var | Son N tool sonucu tutuluyor mu? |
| Agent loop state | Var | Recursive context stack'i yok |
| UI state snapshot | Yok | TUI state'i context'e dahil değil |

### 2.2 Provider Abstraction Çatlakları

```go
// Potansiyel provider-specific logic
switch model {
case "o3-pro", "o3-high":
    // Reasoning token ayrı bütçe
case "gpt-4o", "gpt-4o-2024-05-13":
    // Standard context window
case "claude-3-5-sonnet":
    // Vision token ayrı
}
```

Mevcut kodda bu provider-specific logic **yok** — tek bir `max_tokens` limiti var.

### 2.3 Activity Log Memory

```go
// activity.go
const maxActivityEntries = 2000
```

2000 entry × ortalama 500 char = **~1 MB raw activity data**

Sorun: Entry başı meta (timestamp, kind, target) + payload + render buffer = **2-3 MB per session**

---

## 3. Önerilen Refactor Yolu

### 3.1 Katman 1: Context Budget Controller

```go
type ContextBudget struct {
    TotalBudget     int // model max_context_window
    SystemPrompt    int // sistemin kullandığı token
    AvailableForTurn int // Kullanıcı + Assistant mesajları için
}

func (b *ContextBudget) Allocate(msgType string, size int) int {
    // Her message type için pay ayır
}
```

### 3.2 Katman 2: Source-Agnostic Context Items

```go
type ContextItem interface {
    Size() int           // Token tahmini
    Priority() int       // 0-100, düşük = daha çıkarılabilir
    Source() string      // "file", "codemap", "tool_result", "activity"
    Content() string
}
```

### 3.3 Katman 3: Provider-Specific Adaptation

```go
type ProviderAdapter interface {
    AdjustForModel(model string, budget ContextBudget) ContextBudget
    EncodeSpecialTokens(text string) int // Markdown, code blocks
    SplitMessage(text string, maxTokens int) []string
}
```

---

## 4. Öncelik Sırası

| Öncelik | İş | Etki | Risk |
|---------|-----|------|------|
| 🔴 P0 | `codemap` output token budget | Yüksek | Orta (backward compat) |
| 🔴 P0 | `grep_codebase` result limit | Yüksek | Düşük |
| 🟡 P1 | Activity log eviction policy | Orta | Düşük |
| 🟡 P1 | Provider model family detection | Orta | Yüksek (new feature) |
| 🟢 P2 | TUI state snapshot context | Düşük | Düşük |

---

## 5. İlgili Dosyalar

```
internal/tools/builtin_read.go      — Dosya okuma, encoding, limit
internal/tools/codemap.go           — Symbol graph, potential token bomb
internal/tools/snapshot_cache.go   — Read cache, eviction
internal/engine/agent_loop_limits.go — Tool token/char limits
ui/tui/activity.go                 — Activity log management
ui/tui/chat_timeline_format.go     — Render formatting
ui/tui/tool_introspect.go          — Tool description/completion
ui/tui/tool_runtime_helpers.go     — Tool execution helpers
ui/tui/chat_commands_keys_io.go     — Config persistence
```

---

## 6. Açık Sorular

1. **Context eviction policy:** LRU, FIFO, priority-based?
2. **Codemap granularity:** Symbol summary mı, full signature mu?
3. **Activity log:** Sadece son N entry mi, yoksa importance-based mi?
4. **Provider detection:** Model name parsing veya explicit config?
5. **Token estimation:** `cl100k_base` vs provider API vs approximate?

---

*Generated from codebase analysis. Son güncelleme: 2026-05-02*
