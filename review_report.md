# Code Review Report

Generated: $(date '+%Y-%m-%d %H:%M:%S')
Project: DFMC

## Executive Summary

Bu rapor, kod tabanındaki kritik dosyaların incelenmesi sonucu tespit edilen sorunları içermektedir.

---

## 1. Graph Implementation - Deadlock Risk

**File:** `internal/codemap/graph.go`

**Issue:** `AddNode` ve `AddEdge` metodları mutex kilidi altında çalışırken, internal map'ler üzerinde direct atama yapıyor. `RemoveNode` ile eşzamanlı çağrılarda race condition riski mevcut.

**Severity:** High

**Recommendation:**
```go
func (g *Graph) AddNode(node Node) {
    g.mu.Lock()
    defer g.mu.Unlock()
    // Mevcut: g.nodes[node.ID] = node
    // Öneri: atomic swap veya copy-on-write pattern
}
```

---

## 2. TreeSitter CGO Memory Management

**File:** `internal/ast/treesitter_cgo.go`

**Issue:** `finalizeTreeSitterParser` fonksiyonu parser.Reset() çağrısı yapmadan memory leak oluşturabilir. Parser pool management'ta race condition riski var.

**Severity:** High

**Recommendation:**
```go
defer func() {
    parser.Reset() // Eklenmeli
    finalizeTreeSitterParser(pool, parser, healthy)
}()
```

---

## 3. Task Tree Rendering - Missing Nil Check

**File:** `.deferred/ui/web/render_task_tree.go:48`

**Issue:** `buildTaskTreeRows` fonksiyonunda `storeTasks` nil olabilir. `len(storeTasks) == 0` kontrolü yeterli değil.

**Severity:** Medium

**Recommendation:**
```go
func buildTaskTreeRows(storeTasks []*supervisor.Task, ...) []taskTreeRow {
    if storeTasks == nil {
        return nil
    }
    // ...
}
```

---

## 4. Parse Cache Eviction Bug

**File:** `internal/ast/engine.go`

**Issue:** `parseCache` LRU eviction implementasyonu eksik. `cache.Add()` çağrısı yapılıyor ancak eski entry'lerin temizlenmesi garantili değil.

**Severity:** Medium

**Code Location:** Line ~85-120

---

## 5. Config Models Dev - Hardcoded Fallback

**File:** `internal/config/config_models_dev.go:12`

**Issue:** `DefaultModelsDevAPIURL` production ortamında değiştirilemez sabit. Dinamik endpoint configuration desteği yok.

**Severity:** Low

---

## 6. Command Registry - Case Sensitivity

**File:** `internal/commands/registry.go`

**Issue:** Command matching'de case-insensitive arama yapılmıyor. Kullanıcı deneyimi olumsuz etkilenebilir.

**Severity:** Low

---

## 7. Context Manager - File Count Limit

**File:** `internal/context/manager.go`

**Issue:** `BuildOptions.MaxFiles` default değeri yok. Sıfır değerinde tüm dosyalar proses edilebilir (OOM riski).

**Severity:** High

---

## Summary Statistics

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 3 |
| Medium   | 2 |
| Low      | 2 |

---

## Next Steps

1. High severity issue'ları öncelikli olarak düzelt
2. Unit test coverage kontrol et
3. Race condition testleri ekle
