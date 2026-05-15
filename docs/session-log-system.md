# DFMC Session Log Sistemi — Tam Dokümantasyon

## 1. Depolama Yapısı

```
~/.dfmc/                                          ← UserHomeDir()
└── userhome/
    └── {project}/
        └── logs/
            ├── {session-id-1}.jsonl              ← Session A
            ├── {session-id-2}.jsonl              ← Session B
            └── {session-id-3}.jsonl              ← Session C
```

**Path Builder** — *was* `pkg/session/path_utils.go` (removed; the package
held three skeleton helpers that nothing else ever wired). If/when
session log persistence is implemented, build path helpers off
`config.UserConfigDir()` directly:

```go
// Sketch — pkg/session was deleted; reintroduce when the rest of the
// system below actually consumes a log path.
func sessionLogDir(project string) string {
    return filepath.Join(config.UserConfigDir(), "userhome", project, "logs")
}

func sessionLogPath(project, sessionID string) string {
    return filepath.Join(sessionLogDir(project), sessionID+".jsonl")
}
```

**Örnek:**
```
project    = "my-api"
sessionID  = "sess-abc-123"
→ ~/.dfmc/userhome/my-api/logs/sess-abc-123.jsonl
```

---

## 2. Mevcut Durum (2026-05-13)

| Bileşen | Dosya | Durum |
|---------|-------|-------|
| Path utils | *deleted* (`pkg/session/`) | ❌ removed — never wired |
| Storage dizini | `~/.dfmc/userhome/{project}/logs/` | ✅ Hazır |
| Session struct | `internal/session/session.go` | ✅ Var |
| Agent tree | `internal/session/agent.go` | ✅ Var |
| Attention bus | `internal/session/attention.go` | ✅ Var (memory'de) |
| **Persistent log dosyası** | `internal/session/session.go` | ❌ **YOK** |

### Session Struct (Mevcut — line 19-47)

```go
type Session struct {
    mu           sync.RWMutex
    engine       EngineProvider
    agents       map[AgentID]*Agent
    root         AgentID         // = 1 (root agent)
    attention    *SharedAttention // Event bus (memory)
    activeAgent  AgentID
    nextID       AgentID         // Agent ID counter
    depthCap     int             // = 5
    // ⚠️ logFile *os.File       ← YOK
    // ⚠️ logMu   sync.Mutex     ← YOK
    // ⚠️ logPath string          ← YOK
}
```

### Agent Struct

```go
type Agent struct {
    id       AgentID
    name     string
    parent   AgentID         // Tree edge
    children []AgentID       // Sub-agents
    
    conversation *conversationRef
    context      ContextManagerHandle
    
    model  string
    budget Budget
    
    inbox  chan DelegationTask
    status AgentStatus
    
    engine EngineProvider
}
```

---

## 3. Session Event'leri (Memory'de Var, Dosyada Yok)

### 3.1 Event Türleri

| Event | Açıklama | Struct |
|-------|----------|--------|
| `agent:spawn` | Yeni agent oluşturuldu | — |
| `agent:status` | Agent durumu değişti | `StatusEvent` |
| `agent:kill` | Agent sonlandırıldı | — |
| `task:delegation` | Task delegation yapıldı | `DelegationTask` |
| `attention:event` | Cross-agent event | `AttentionEvent` |
| `tool:call` | Tool çağrıldı | — |
| `tool:result` | Tool sonucu döndü | — |

### 3.2 Agent Durumları (`types.go`)

| Durum | Değer | Açıklama |
|-------|-------|----------|
| `StatusIdle` | `iota` | Başlamamış |
| `StatusRunning` | — | Aktif çalışıyor |
| `StatusWaitingDelegation` | — | Task bekliyor |
| `StatusWaitingUserInput` | — | Kullanıcı inputu bekliyor |
| `StatusParked` | — | Duraklatıldı |
| `StatusDone` | — | Temiz bits |
| `StatusFailed` | — | Hata ile bits |

### 3.3 AttentionEvent Yapısı (`types.go`)

```go
type AttentionEvent struct {
    From    AgentID        `json:"from"`
    Type    AttentionType  `json:"type"`
    Payload []byte         `json:"payload,omitempty"`
}
```

**AttentionType:**
| Tip | String |
|-----|--------|
| `AttentionToolResult` | `"tool_result"` |
| `AttentionFileCreated` | `"file_created"` |
| `AttentionError` | `"error"` |
| `AttentionInfo` | `"info"` |
| `AttentionQuestion` | `"question"` |
| `AttentionDelegationSent` | `"delegation_sent"` |
| `AttentionDelegationDone` | `"delegation_done"` |

### 3.4 DelegationTask Yapısı (`types.go`)

```go
type DelegationTask struct {
    ID           uuid.UUID     `json:"id"`
    From         AgentID       `json:"from"`
    Task         string        `json:"task"`
    SystemPrompt string        `json:"system_prompt,omitempty"`
    Autonomy     AutonomyLevel `json:"autonomy"`
    Status       TaskStatus    `json:"status"`
    CreatedAt    time.Time     `json:"created_at"`
}
```

### 3.5 AutonomyLevel

| Seviye | Açıklama |
|--------|----------|
| `AutonomyFull` | Hiç sormaz, limit aşınca park |
| `AutonomyLimited` | Limit aşınca sorar |
| `AutonomyBlocked` | Durdurulur |

---

## 4. Attention Bus Mekanizması

### 4.1 SharedAttention (`attention.go`)

```go
type SharedAttention struct {
    mu       sync.RWMutex
    handlers map[string][]Handler  // topic → handlers
}

func (sa *SharedAttention) Publish(e AttentionEvent) {
    sa.mu.RLock()
    defer sa.mu.RUnlock()
    
    topic := "agent:" + strconv.Itoa(int(e.From))
    for _, h := range sa.handlers[topic] {
        h(e)
    }
    // ⚠️ Session log dosyasına YAZMIYOR
}

func (sa *SharedAttention) Subscribe(topic string, h Handler) {
    sa.mu.Lock()
    defer sa.mu.Unlock()
    sa.handlers[topic] = append(sa.handlers[topic], h)
}
```

**Not:** Attention event'leri şu anda sadece memory'de tutuluyor — dosyaya yazılmıyor.

---

## 5. Eksik Olan Bileşenler

### 5.1 logFile Field

Session struct'ta `logFile *os.File` **yok**.

### 5.2 writeEvent() Metodu

JSONL formatında event yazacak metod **yok**.

### 5.3 Close() Metodu

Session'ı temiz şekilde kapatacak metot **yok**.

### 5.4 Event Logging Çağrıları

| Fonksiyon | Event | Satır | Durum |
|-----------|-------|-------|-------|
| `New()` | `session:start` | ~73 | ❌ Yok |
| `SpawnAgent()` | `agent:spawn` | ~162 | ❌ Yok |
| `killLocked()` | `agent:kill` | ~370 | ❌ Yok |
| `setStatus()` | `agent:status` | — | ❌ Yok |
| `executeTool()` | `tool:call/result` | — | ❌ Yok |
| `Attention.Publish()` | `attention:event` | — | ❌ Yok |

---

## 6. Implementasyon Planı

### 6.1 Struct'a Field'lar Ekle

```go
// internal/session/session.go
type Session struct {
    // ... mevcut field'lar ...
    
    logFile *os.File     // ← EKLE
    logMu   sync.Mutex   // ← EKLE
    logPath string       // ← EKLE
}
```

### 6.2 writeEvent() Metodu Ekle

```go
// internal/session/session.go
func (s *Session) writeEvent(event string, fields map[string]any) {
    if s.logFile == nil {
        return
    }
    
    rec := map[string]any{
        "ts":    time.Now().UTC().Format(time.RFC3339Nano),
        "event": event,
    }
    for k, v := range fields {
        rec[k] = v
    }
    
    line, err := json.Marshal(rec)
    if err != nil {
        return
    }
    
    s.logFile.Write(append(line, '\n'))
}
```

### 6.3 New()'e Log Açma Ekle

```go
// internal/session/session.go — New() fonksiyonu sonuna ekle
func New(project, sessionID string) *Session {
    // ... mevcut kod ...
    
    // Log dosyasını aç
    s.logPath = path_utils.GetLogPath(project, sessionID)
    if err := os.MkdirAll(filepath.Dir(s.logPath), 0755); err == nil {
        f, err := os.OpenFile(s.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
        if err == nil {
            s.logFile = f
            s.writeEvent("session:start", map[string]any{
                "session_id": sessionID,
                "project":    project,
            })
        }
    }
    
    return s
}
```

### 6.4 Close() Metodu Ekle

```go
// internal/session/session.go
func (s *Session) Close() {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Tüm agent'ları sonlandır
    for id := range s.agents {
        s.killLocked(id)
    }
    
    // Session close event
    if s.logFile != nil {
        s.writeEvent("session:close", map[string]any{
            "total_agents": len(s.agents),
        })
        s.logFile.Close()
        s.logFile = nil
    }
}
```

### 6.5 SpawnAgent()'e Log Ekle

```go
// internal/session/session.go — ~line 165, fonksiyon sonuna ekle
func (s *Session) SpawnAgent(...) {
    // ... mevcut kod ...
    
    s.writeEvent("agent:spawn", map[string]any{
        "agent_id": id,
        "parent_id": parentID,
        "task": task,
        "autonomy": autonomy,
    })
    
    return agent
}
```

### 6.6 killLocked()'e Log Ekle

```go
// internal/session/session.go — ~line 375, fonksiyon içine ekle
func (s *Session) killLocked(id AgentID) {
    // ... mevcut kod ...
    
    s.writeEvent("agent:kill", map[string]any{
        "agent_id": id,
        "reason":   reason,
    })
}
```

---

## 7. Hedef Event Formatları (JSONL)

### session:start
```json
{"ts":"2026-05-13T14:30:00.000Z","event":"session:start","session_id":"sess-abc-123","project":"my-api"}
```

### session:close
```json
{"ts":"2026-05-13T16:45:00.000Z","event":"session:close","total_agents":4}
```

### agent:spawn
```json
{"ts":"2026-05-13T14:31:00.000Z","event":"agent:spawn","agent_id":2,"parent_id":1,"task":"refactor module","autonomy":"full"}
```

### agent:status
```json
{"ts":"2026-05-13T14:32:00.000Z","event":"agent:status","agent_id":2,"from":"waiting_delegation","to":"running"}
```

### agent:kill
```json
{"ts":"2026-05-13T15:00:00.000Z","event":"agent:kill","agent_id":3,"reason":"task_complete"}
```

### task:delegation
```json
{"ts":"2026-05-13T14:33:00.000Z","event":"task:delegation","from_agent_id":1,"to_agent_id":2,"task":"validate API","autonomy":"full"}
```

### attention:event
```json
{"ts":"2026-05-13T14:35:00.000Z","event":"attention:event","source_agent_id":2,"type":"tool_result","data":{"tool":"read_file"}}
```

### tool:call
```json
{"ts":"2026-05-13T14:37:00.000Z","event":"tool:call","agent_id":2,"tool":"read_file","step":1,"params":{"path":"internal/user.go"}}
```

### tool:result
```json
{"ts":"2026-05-13T14:37:00.015Z","event":"tool:result","agent_id":2,"tool":"read_file","success":true,"duration_ms":12}
```

---

## 8. Retrieval Komutları

### Session Log Okuma
```bash
# Tüm eventleri oku
cat ~/.dfmc/userhome/my-api/logs/sess-abc-123.jsonl | jq .

# Sadece tool call'ları çek
grep '"event":"tool:call"' ~/.dfmc/userhome/my-api/logs/sess-abc-123.jsonl | jq .

# Agent başına eventleri say
jq -r '.agent_id // empty' ~/.dfmc/userhome/my-api/logs/sess-abc-123.jsonl | sort | uniq -c

# Error olan eventleri bul
grep '"event":"error"' ~/.dfmc/userhome/my-api/logs/sess-abc-123.jsonl | jq '.error'
```

### Timeline Oluşturma
```bash
jq -s 'sort_by(.ts) | .[] | "\(.ts) [\(.event)] agent=\(.agent_id // .from_agent_id // "-")"' \
  ~/.dfmc/userhome/my-api/logs/sess-abc-123.jsonl
```

---

## 9. Karşılaştırma: Session Log vs Diğer Log Türleri

| Özellik | Session Log | ToolHistory | ProviderLog | Applog |
|---------|-------------|-------------|-------------|--------|
| **Format** | JSONL | JSONL | JSONL | JSONL |
| **Path** | `userhome/{proj}/logs/` | `data/toolcalls/` | `data/provider_calls/` | `data/app/` |
| **Granularity** | Tüm event'ler | Her tool call | Her LLM isteği | Her log mesajı |
| **Agent ID** | ✅ | ✅ | ✅ | ❌ |
| **Cross-agent** | ✅ (attention) | ❌ | ❌ | ❌ |
| **Tool params** | ✅ | ✅ | — | — |
| **Replay** | Tam | Kısmi | Hayır | Hayır |
| **Durum** | ❌ Implement edilmemiş | ✅ Var | ✅ Var | ✅ Var |

---

## 10. Sonuç

| Durum | Açıklama |
|-------|----------|
| Path utils | ✅ Hazır |
| Storage dizini | ✅ Hazır |
| Session struct | ✅ Var |
| Memory event bus | ✅ Var |
| **Persistent log dosyası** | ❌ **Implement edilmemiş** |

**Eksik olan 3 şey:**
1. `logFile *os.File` field — Session struct'a ekle
2. `writeEvent()` metodu — JSONL yazma
3. `Close()` metodu — Session kapatma

Bu 3'ü eklendiğinde session log sistemi `~/.dfmc/userhome/{project}/logs/{session-id}.jsonl` olarak çalışır.
