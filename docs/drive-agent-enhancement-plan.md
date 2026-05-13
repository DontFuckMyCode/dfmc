# Drive Agent İyileştirme Planı

> Oluşturuldu: 2026-05-14 | Modül: internal/drive | Dosya sayısı: 40

## Durum Özeti

| Modül | Durum | Kritik Sorun |
|-------|-------|-------------|
| planner.go | ✅ Çalışıyor | Context injection yok, fallback yok |
| run_executor.go | ✅ Çalışıyor | Parallel dispatch doğru |
| run_executor_outcome.go | ✅ Çalışıyor | Static step budget |
| scheduler.go | ✅ Çalışıyor | File-scope conflict detection iyi |
| supervision.go | ✅ Çalışıyor | External supervisor'a bağımlı |
| config.go | ⚠️ Eksik | Exponential backoff yok, circuit breaker yok |

---

## Öncelik Matrisi

| Etki / Effort | Yüksek | Düşük |
|---------------|--------|-------|
| **Yüksek** | P1, E1 | P2, P3, E2 |
| **Düşük** | S1, G1 | P4, P5, E3, E4, S2 |

---

## 🔴 Öncelik 1 — Kritik (Yüksek Etki)

### P1: Planner Context Injection
**Dosya:** `planner.go`
**Mevcut:** `plannerSystemPrompt` hardcoded, planner sadece task görür
**Sorun:** Büyük kod tabanlarında Planner bağlam bilmez, kötü TODO DAG'ları üretir

```go
// EKLENECEK: PlannerContext interface
type PlannerContextProvider interface {
    GetContext(task string) (PlannerContext, error)
}

type PlannerContext struct {
    RepoSummary   string
    KeyFiles      []string
    TechStack     string
    Constraints   []string
    RecentChanges string
}
```

**Uygulama adımları:**
1. `PlannerContext` struct tanımı (`planner.go`)
2. `PlannerContextProvider` interface tanımı
3. `DefaultPlannerContextProvider` struct
4. `runPlanner` fonksiyonuna context injection
5. `Config.PlannerContextProvider` alanı ekle

---

### P2: Adaptive Step Budget
**Dosya:** `driver_helpers.go:78-100`
**Mevcut:** Statik lane-based (discovery=6, review=7, verify=8/10, synthesize=6, default=12/14)
**Sorun:** Basit/karmaşık task aynı budget alır

```go
// DEĞİŞTİR: executorStepBudgetFor — adaptive version
func executorStepBudgetFor(todo Todo, runStats *RunStats) int {
    base := calculateBaseBudget(todo)
    
    if todo.Confidence > 0 && todo.Confidence < 0.7 {
        base = int(float64(base) * 1.3)
    }
    if runStats != nil && runStats.SuccessRate > 0.8 {
        base = int(float64(base) * 0.85)
    }
    if len(todo.FileScope) >= 5 {
        base = int(float64(base) * 0.9)
    }
    return clamp(base, 3, 25)
}
```

---

### P3: Planner Fallback Chain
**Dosya:** `planner.go`
**Mevcut:** Tek model — başarısız olursa run başarısız

```go
func runPlanner(ctx context.Context, runner Runner, task string, models []string) ([]Todo, error) {
    var lastErr error
    for i, model := range models {
        todos, err := runPlannerSingle(ctx, runner, task, model)
        if err == nil { return todos, nil }
        if isNonRetryable(err) { return nil, err }
        lastErr = err
        if i < len(models)-1 {
            time.Sleep(time.Duration(1<<uint(i)) * time.Second)
        }
    }
    return nil, fmt.Errorf("all planner models failed: %w", lastErr)
}

// Config'e ekle:
PlannerModels []string // e.g. ["claude-sonnet", "claude-haiku"]
```

---

## 🟡 Öncelik 2 — Yüksek Etki, Düşük Effort

### P4: Retry Policy + Exponential Backoff
**Dosya:** `config.go` — `Retries: 1` — immediate retry, no backoff

RetryPolicy struct with MaxAttempts, InitialDelay, MaxDelay, Multiplier. Default: 3 attempts, 1s initial, 30s cap, 2x multiplier.

### P5: Circuit Breaker
**Dosya:** `config.go` veya `circuit_breaker.go`

CircuitBreaker struct: failures, threshold (default 5), timeout (default 30s), state (Closed/Open/HalfOpen).

---

## Referans: Mevcut Kod Yapısı

**Step Budget (driver_helpers.go:78-100):**
```go
func executorStepBudgetFor(todo Todo) int {
    if todo.Budget > 0 { return todo.Budget }
    switch todoLane(todo) {
    case "discovery": return 6
    case "review":    return 7
    case "verify":
        if strings.EqualFold(strings.TrimSpace(todo.Verification), "deep") { return 10 }
        return 8
    case "synthesize": return 6
    default:
        if len(todo.FileScope) >= 3 { return 14 }
        return 12
    }
}
```

**Config (config.go):**
```go
type Config struct {
    MaxTodos, MaxFailedTodos, Retries, MaxParallel int
    MaxWallTime, DrainGraceWindow time.Duration
    PlannerModel string
    Routing map[string]string
    AutoApprove, AutoVerify, AutoSurvey bool
}
```

**Planner Output Shape:**
```json
{
  "todos": [
    {
      "id": "T1",
      "title": "...",
      "detail": "...",
      "depends_on": [],
      "file_scope": [],
      "provider_tag": "code",
      "worker_class": "coder",
      "skills": ["debug"],
      "allowed_tools": ["read_file", "grep_codebase"],
      "verification": "required",
      "confidence": 0.84
    }
  ]
}
```

---

## Doğrulama

```bash
go test ./internal/drive/... -v -count=1
go vet ./internal/drive/...
```
