# DFMC Roadmap v1 — Teknik Yapılacak İşler

> **Tarih:** 2026-04-26  
> **Proje:** DFMC (Don't Fuck My Code)  
> **Dil:** Go  
> **Kapsam:** `D:\Codebox\PROJECTS\DFMC`

---

## 0. Mevcut Durum Özeti

| Paket | Test Durumu | Test Coverage |
|-------|-------------|---------------|
| `internal/drive` | ✅ PASS (planner + validation testleri) | — |
| `internal/planning` | ✅ PASS | — |
| `internal/context` | ✅ PASS | — |
| `internal/codemap` | ✅ PASS | — |
| `internal/engine` | ❌ FAIL (database lock timeout) | — |
| `ui/cli` | — | 48.1% → hedef 65% |
| `ui/tui` | — | 58.9% → hedef 65% |
| `internal/mcp` | — | 52.3% → hedef 65% |
| `internal/pluginexec` | — | 49.5% → hedef 65% |
| `cmd/dfmc` | — | 44.1% → hedef 60% |

---

## 1. P0 — Test Coverage İyileştirmeleri (GA Blocker)

> Tüm testler yerel terminalde çalıştırılmalı:
> ```bash
> go test ./... -count=1 -timeout=60s
> ```

### P0.1 `ui/cli` Coverage (48.1% → 65%+)

**Tahmini Süre:** 6 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `ui/cli/cli_*_test.go`

**Teknik Gereksinimler:**
- CLI flag parsing testleri (`--model`, `--provider`, `--config` vb.)
- `ui/cli/interactive.go` — interactive mode edge case testleri
- Error recovery path testleri (network timeout, invalid JSON, empty response)
- Command registration testleri (`commands/registry.go` ile entegrasyon)
- ANSI color output testleri (tty detection mock)

**Test Yapısı:**
```go
// Örnek test case yapısı
func TestCLI_FlagParsing(t *testing.T) {
    tests := []struct {
        name    string
        args    []string
        wantErr bool
    }{
        {"valid_model_flag", []string{"--model", "gpt-4"}, false},
        {"invalid_provider", []string{"--provider", "unknown"}, true},
    }
    // ...
}
```

### P0.2 `ui/tui` Coverage (58.9% → 65%+)

**Tahmini Süre:** 4 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `ui/tui/*_test.go`

**Teknik Gereksinimler:**
- Event loop input handling (`tcell.Events`)
- Resize handling (SIGWINCH simulation)
- Search/filter logic testleri
- Cursor movement keyboard navigation
- Panel visibility toggle edge cases
- Theme switching (dark/light/auto)

**Kritik Edge Cases:**
- Buffer overflow simulation (çok uzun dosya adları)
- Null byte handling in user input
- Terminal resize during render
- Concurrent event dispatch during state transition

### P0.3 `internal/mcp` Coverage (52.3% → 65%+)

**Tahmini Süre:** 5 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `internal/mcp/*_test.go`

**Teknik Gereksinimler:**
- JSON-RPC 2.0 protocol parsing (valid/invalid requests)
- `internal/mcp/protocol.go` — message framing testleri
- Server-client handshake sequence
- Error response construction per JSON-RPC spec
- Notification handling (no response expected)
- Batch request processing
- Connection timeout scenarios
- Invalid JSON handling with error codes: `-32700`, `-32600`, `-32601`

**JSON-RPC 2.0 Compliance Test Matrisi:**
```
| Method | Valid Req | Invalid JSON | Wrong Version | No Method | Params Type |
|--------|-----------|--------------|---------------|----------|-------------|
| tools/list | ✅ | -32700 | -32600 | -32600 | -32600 |
| tools/call | ✅ | -32700 | -32600 | -32600 | -32600 |
| resources/* | ✅ | -32700 | -32600 | -32600 | -32600 |
```

### P0.4 `internal/pluginexec` Coverage (49.5% → 65%+)

**Tahmini Süre:** 4 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `internal/pluginexec/*_test.go`

**Teknik Gereksinimler:**
- Plugin manifest parsing (`PluginManifest` struct validation)
- Plugin lifecycle: `load → init → execute → unload`
- Sandbox isolation verification (filesystem access deny)
- Environment variable injection
- `internal/pluginexec/wasm.go` — WASM plugin loading path
- Process spawning with resource limits (`ulimit` equivalent in Go)
- Zombie process cleanup on plugin crash

**WASM Plugin Wire-Up (P1.3 için altyapı):**
```go
// internal/pluginexec/manager.go mevcut case:
case "wasm":
    return loadWasmPlugin(ctx, manifest)
// Bu case handler'ı test edilmeli
```

### P0.5 `cmd/dfmc` Coverage (44.1% → 60%+)

**Tahmini Süre:** 2 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `cmd/dfmc/*_test.go`

**Teknik Gereksinimler:**
- Main entry point flag initialization
- Config file auto-discovery (`.dfmc.yaml`, `.dfmc.yml`, `~/.dfmc/config`)
- Environment variable override precedence
- Subcommand routing (`ask`, `drive`, `codemap`, `update`, `version`)
- Version flag output format
- Help text completeness

---

## 2. P1 — Eksik Özellikler (GA Kalitesi)

### P1.1 Google Gemini Provider Entegrasyonu

**Tahmini Süre:** 4 saat  
**Bağımlılık:** Provider interface mevcut  
**Dosyalar:**
- `internal/provider/google.go` (yeni veya genişletilecek)
- `internal/config/defaults.go`
- `internal/provider/router.go`

**Teknik Gereksinimler:**
- `provider.Message` → Gemini API format dönüşümü
- `types.RoleUser/types.RoleAssistant` → `user/model` role mapping
- Tool calling format: Gemini function calling JSON schema
- System prompt injection via `system_instruction`
- Response parsing: `Candidate.Content.Parts[0].Text`
- Model selection: `gemini-1.5-pro`, `gemini-1.5-flash`, `gemini-2.0-flash`
- Rate limiting headers: `x-ratelimit-*` header parsing
- Error mapping: Gemini-specific error codes → `provider.APIError`

**Mevcut Router Yapısı (genişletilecek):**
```go
// internal/provider/router.go
switch p.provider {
case "openai":
    return openAIProvider.Ask(ctx, req)
case "anthropic":
    return anthropicProvider.Ask(ctx, req)
case "google", "gemini":
    return geminiProvider.Ask(ctx, req)  // ← Eklenecek
}
```

### P1.2 Codemap Bbolt Serialization

**Tahmini Süre:** 8 saat  
**Bağımlılık:** Yok  
**Dosyalar:**
- `internal/codemap/graph.go`
- `internal/codemap/engine.go`
- `internal/ast/engine.go` (cache backend)

**Teknik Gereksinimler:**
- `Graph` struct serialization to bbolt bucket format:
  ```
  bucket: "nodes" → key=Node.ID, value=json.Marshal(Node)
  bucket: "edges" → key=From+"|"+Type, value=json.Marshal(Edge)
  ```
- Incremental update: sadece değişen node/edge'leri yaz
- Cache invalidation: file hash değişince mark dirty
- Background compaction goroutine
- `internal/ast/engine.go`'daki `ParseResult` cache → bbolt backend
- `types.Symbol` → bbolt key encoding (prefix-free, sortable)
- Query performance: range scan vs point lookup optimizasyonu

**Mevcut `Engine` Yapısı (genişletilecek):**
```go
// internal/ast/engine.go
type Engine struct {
    extToLang map[string]string
    cache     *parseCache         // ← Mem-only cache
    metrics   *parseMetricsTracker
}
// Bbolt için:
// cache *bboltBackend  // veya cache ParseCache interface
```

### P1.3 WASM Plugin Wiring

**Tahmini Süre:** 3 saat  
**Bağımlılık:** P0.4 (pluginexec coverage)  
**Dosyalar:**
- `internal/pluginexec/manager.go`
- `internal/pluginexec/wasm.go`

**Teknik Gereksinimler:**
- `wazero` runtime initialization (sandboxed execution)
- WASM module loading from `PluginManifest.Path`
- Host function export: Go → WASM ABI
- Tool result serialization (JSON → WASM linear memory)
- Memory allocation strategy: Grow or pre-allocate?
- Plugin manifest `wasm` type detection:
  ```go
  case "wasm":
      return loadWasmPlugin(ctx, manifest)
  ```
- Memory safety verification (out-of-bounds access detection)
- WASM execution timeout (per-plugin budget enforcement)

**Test Stratejisi:**
```go
// Test için minimal WASM binary:
// func main() {} → compiled to .wasm
// veya wazero ile test bytecode
```

---

## 3. P2 — Cleanup (Kod Kalitesi)

### P2.1 Tüm TODO/FIXME/XXX Marker Temizliği

**Tahmini Süre:** 6 saat  
**Bağımlılık:** Yok  
**Dosyalar:** Tüm `.go` dosyaları (test olmayan)

**Komut:**
```bash
grep -rn "TODO\|FIXME\|XXX\|BUG\|HACK" --include="*.go" . \
    | grep -v "_test.go" | grep -v vendor > /tmp/todos.txt
```

**Marker Kategorileri:**
- `TODO(user):` — kullanıcıya yönelik, task oluştur
- `TODO(impl):` — implementasyon açığı, yüksek öncelik
- `FIXME:` — bug, mutlaka düzeltilmeli
- `XXX:` — bilinen sorun, mutlaka düzeltilmeli
- `BUG:` — açık bug, öncelik 1

**Toplam:** ~224 marker (konuşma geçmişinden)

### P2.2 Incremental File Watcher

**Tahmini Süre:** 5 saat  
**Bağımlılık:** P1.2 (codemap bbolt cache)  
**Dosyalar:**
- `internal/ast/watcher.go`
- `internal/codemap/engine.go`

**Teknik Gereksinimler:**
- `fsnotify.Watcher` ile file system monitoring
- Debounce strategy: 100ms cooldown (configurable)
- Dirty set tracking: changed files → re-parse queue
- Parse queue prioritization: foreground tab files first
- Graceful degradation: watch failure → full rescan
- `codemap/engine.go` incremental update API

**Partial Implementation:** v2.16'da kısmi var — tamamlanması gerekenlar:
- Error recovery: watcher goroutine crash → restart
- Symlink handling: resolve or skip
- Large project mode: ignore `node_modules`, `.git`, `vendor`

### P2.3 Auto Self-Update (Self-Replacing Binary)

**Tahmini Süre:** 4 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `internal/commands/update.go`

**Teknik Gereksinimler:**
- Update URL: `https://github.com/dontfuckmycode/dfmc/releases/latest`
- Checksum verification: SHA256
- Atomic swap strategy:
  ```
  1. Download new binary → /tmp/dfmc-new
  2. Verify checksum
  3. chmod +x /tmp/dfmc-new
  4. os.Rename("/tmp/dfmc-new", old_path)  // atomic on POSIX
  5. Restart
  ```
- Windows fallback: `Update.exe` swap mechanism
- Rollback strategy on failure
- Channel support: `stable`, `beta`, `nightly`
- Version comparison: semver parsing

**Partial Implementation:** v20.2'de kısmi var

---

## 4. P3 — Polish (Good to Have)

### P3.1 Homebrew Tap Publish

**Tahmini Süre:** 2 saat  
**Bağımlılık:** P1.1 (Gemini provider, version stable)  
**Dosyalar:** `shell/Formula.rb`

**Teknik Gereksinimler:**
- `Formula.rb` auto-generated from `version` variable
- SHA256 checksum in formula
- Brew test case definition:
  ```ruby
  test do
    system "#{bin}/dfmc", "version"
  end
  ```
- Bottle definition (pre-built binary)

### P3.2 Shell Completions Full Test Coverage

**Tahmini Süre:** 1 saat  
**Bağımlılık:** Yok  
**Dosyalar:** `shell/completions_test.go`

**Teknik Gereksinimler:**
- Bash completion: `dfmc completion bash`
- Zsh completion: `dfmc completion zsh`
- Fish completion: `dfmc completion fish`
- PowerShell completion: `dfmc completion powershell`
- Completion output validation (valid shell syntax)
- Dynamic completions for subcommands

### P3.3 Man Page Full Set

**Tahmini Süre:** 2 saat  
**Bağımlılık:** Yok  
**Dosyalar:**
- `shell/gen_man.go`
- `man/` directory

**Teknik Gereksinimler:**
- `dfmc-ask(1)` — interactive ask command
- `dfmc-drive(1)` — autonomous drive command
- `dfmc-codemap(1)` — code navigation
- `dfmc-config(1)` — configuration guide
- Section structure: NAME, SYNOPSIS, DESCRIPTION, OPTIONS, EXAMPLES, EXIT STATUS

---

## 5. Kalan Açık İşler (Bu Proje Oturumundan)

| # | Görev | Öncelik | Durum | Dosya |
|---|-------|---------|-------|-------|
| 1 | `internal/engine` database lock timeout düzeltmesi | 🔴 Yüksek | Bekliyor | `internal/engine/*_test.go` |
| 2 | `context/manager.go` — `StrategyRefactor` implementasyon detayı | 🟡 Orta | İyileştirme | `internal/context/manager.go:41-42` |
| 3 | `driver.go` → `Engine` tam entegrasyon doğrulaması | 🟡 Orta | Kontrol edilecek | `internal/drive/driver.go` |

### 5.1 `internal/engine` Database Lock Fix

**Sorun:** `~/.dfmc/data/dfmc.db` başka process tarafından kilitli (`SQLITE_BUSY`)

**Çözüm Adımları:**
1. Database dosyasını kilitleyen process'i bul:
   ```bash
   # Windows
   handle.exe dfmc.db
   # Linux
   lsof ~/.dfmc/data/dfmc.db
   ```
2. `go test ./internal/engine/...` çalıştırmadan önce dfmc process'lerini öldür:
   ```bash
   pkill dfmc  # Linux
   taskkill /F /IM dfmc.exe  # Windows
   ```
3. Test isolation: her test için isolated temporary database:
   ```go
   tmpDir, _ := os.MkdirTemp("", "dfmc-test-*")
   defer os.RemoveAll(tmpDir)
   cfg := config.Default()
   cfg.DataDir = tmpDir
   ```

### 5.2 `context/manager.go` StrategyRefactor

**Mevcut Kod (satır ~41-42):**
```go
case StrategyRefactor:
    // TODO(impl): use codemap graph to identify refactoring opportunities
```

**Detay:** `codemap/graph.go` → `Graph` API kullanarak refactoring opportunity detection:
- Dead code detection (orphaned functions)
- Circular dependency detection
- Naming convention violations

### 5.3 Driver-Engine Entegrasyonu

**Kontrol Edilecek:**
```go
// internal/drive/driver.go — mevcut yapı
func (d *Driver) runStep(...) {
    // Engine kullanımı doğrulanmalı
    // Response parsing, error recovery, retry logic
}
```

---

## 6. Öncelik Sırası

```
1. go test ./internal/engine/... → FIX (P0 kritik)
2. P0 coverage tasks → 5 paralel workstream
3. P0 bitti → P1.1 (Gemini), P1.2 (codemap bbolt), P1.3 (WASM)
4. P1 bitti → P2 tasks (cleanup paralel)
5. P3 tasks (polish, bağımsız)
```

---

## 7. Sonraki Adımlar

- [ ] `go test ./internal/engine/...` — database lock fix
- [ ] P0.1–P0.5 coverage testleri paralel başlat
- [ ] P1.1 Gemini provider wire-up
- [ ] P2.1 TODO marker temizliği başlat