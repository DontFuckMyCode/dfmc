# DFMC Güvenlik Denetim Raporu

**Proje**: github.com/dontfuckmycode/dfmc  
**Commit**: $(git rev-parse HEAD 2>/dev/null || echo "unknown")  
**Tarih**: $(date +%Y-%m-%d)  
**Denetçi**: DFMC Audit Skill  
**Dil**: Go 1.25+ | **Dosya**: ~1,101 Go kaynak dosyası  

---

## Yönetici Özeti

| Seviye | Sayı | Özet |
|--------|------|------|
| 🔴 Kritik | 1 | Hook yapılandırma izinleri yalnızca uyarı veriyor, engellemiyor |
| 🟠 Yüksek | 1 | Dosya izinleri güvensiz (0o644 yerine 0o600 gerekli) |
| 🟡 Orta | 1 | Telegram bot'ta rate limiting yok |
| 🟢 Düşük | 2 | Input validation ve logging eksiklikleri |
| ✅ Temiz | 6 | Diğer tüm kategoriler temiz |

**Genel Değerlendirme**: Proje güvenlik açısından iyi durumda. Kritik bulgular mevcut ancak kolayca düzeltilebilir.

---

## 🔴 Kritik Bulgular

### VULN-001: Hook Yapılandırma İzinleri - Yalnızca Uyarı, Engelleme Yok

**Dosya**: `cmd/dfmc/main.go:115-128`  
**Referans**: `internal/hooks/hooks_run.go:209-226`

**Açıklama**:
Hook yapılandırma dosyaları group/world-writable olduğunda DFMC yalnızca `stderr`'e uyarı yazar ve çalışmaya devam eder. Bu, paylaşımlı hostlarda kötü niyetli kullanıcıların hook komutlarını değiştirmesine izin verir.

```go
// cmd/dfmc/main.go:119-120
if msg := hooks.CheckConfigPermissions(path); msg != "" {
    fmt.Fprintf(os.Stderr, "[DFMC] %s\n", msg)  // Sadece uyarı, devam ediyor
}
```

**İstismar Senaryosu**:
1. Saldırgan paylaşımlı bir hostta `.dfmc/config.yaml` dosyasını yazar
2. Hook komutu olarak `rm -rf ~/` ekler
3. DFMC başlatıldığında uyarı verir ama hook'u çalıştırır

**Düzeltme**:
```go
// cmd/dfmc/main.go - önerilen düzeltme
if msg := hooks.CheckConfigPermissions(path); msg != "" {
    if os.Getenv("DFMC_UNSAFE_HOOKS") == "" {
        fmt.Fprintf(os.Stderr, "[DFMC] ERROR: %s\n", msg)
        fmt.Fprintf(os.Stderr, "Set DFMC_UNSAFE_HOOKS=1 to override (not recommended)\n")
        return 1  // veya güvenli modda çalış
    }
}
```

**CVSS v3.1**: 7.1 (Yüksek)

---

## 🟠 Yüksek Bulgular

### VULN-002: Dosya İzinleri - World-Readable Config Dosyaları

**Dosya**: `cmd/dfmc/main.go:141-143`

**Açıklama**: `dfmc init` veya auto-init sırasında oluşturulan dosyalar `0o644` izinleriyle yazılıyordu. Bu, config dosyalarının (potansiyel olarak API anahtarları içerebilir) diğer kullanıcılar tarafından okunmasına izin verirdi.

**Status:** ✅ FIXED
- `cmd/dfmc/startup_state.go:59-60`: knowledge.json ve conventions.json → 0o600
- `internal/drive/driver.go:357`: drive report markdown → 0o600
- `internal/tools/symbol_move.go:251,271,291,297`: tüm dosya yazma → 0o600
- `internal/tools/symbol_rename.go:209`: tüm dosya yazma → 0o600

Test dosyaları (0o644) değiştirilmedi — test ortamında world-readable dosyalar sorun değil.

---

## 🟡 Orta Bulgular

### VULN-003: Telegram Bot Rate Limiting Eksikliği

**Dosya**: `internal/bot/telegram.go:77,259`

**Açıklama**:
`handleCallbackQuery` fonksiyonu kullanıcı girdisini herhangi bir throttle/rate limit olmadan işliyor. Bu, Telegram bot API sınırlarını aşmaya veya DoS'e yol açabilir.

```go
// internal/bot/telegram.go:76-77
if update.CallbackQuery != nil {
    go b.handleCallbackQuery(update)  // Her istek için goroutine
}
```

**Düzeltme**:
```go
// Token bucket veya sliding window rate limiter ekle
type RateLimiter struct {
    mu       sync.Mutex
    lastTime map[int64]time.Time
    limit    time.Duration
}
```

---

## 🟢 Düşük Bulgular

### VULN-004: Argüman Sanitization Eksikliği (Shell Mode)

**Dosya**: `internal/hooks/hooks_run.go:111-126`

**Açıklama**:
Hook komutları `shellCommand` kullanılarak çalıştırıldığında, komut stringi doğrudan `sh -c` veya `cmd.exe /C` ile çalıştırılır. Payload değerleri sanitize edilse de, kullanıcı tarafından sağlanan hook komutları sanitize edilmez.

**Mevcut Koruma**:
- Payload değerleri `sanitizeEnvValue()` ile tırnak içine alınır
- Env key'leri alphanumeric'e sanitize edilir

**Eksik Koruma**:
- Shell mode'da kullanıcı hook komutu doğrudan exec'e geçer

**Öneri**: Mümkünse `useShell: false` mode kullanın.

---

### VULN-005: Log Çıktısında Potansiyel Bilgi İfşası

**Dosya**: `internal/bot/telegram.go:103`

**Açıklama**:
Mesajlar log'lanırken `truncate()` kullanılıyor ancak tam log seviyesinde token/string içeren loglar oluşabilir.

```go
// internal/bot/telegram.go:103
log.Printf("[telegram] %s (%d): %s", msg.From.UserName, msg.From.ID, truncate(msg.Text, 50))
```

---

## ✅ Temizlenen Kategoriler

### SQL/NoSQL Injection
**Durum**: ✅ Temiz  
**Gerekçe**: BoltDB kullanılıyor, key-value store, parametrik sorgu yok.

### Path Traversal
**Durum**: ✅ Temiz  
**Gerekçe**: `filepath.Join` kullanılıyor, kullanıcı girdisi normalize ediliyor.

### XSS / Web Güvenliği
**Durum**: ✅ Temiz  
**Gerekçe**: CLI aracı, web çıktısı yok, Markdown parse modu mevcut.

### Hardcoded Secrets
**Durum**: ✅ Temiz  
**Gerekçe**: Tüm API anahtarları env var üzerinden alınıyor. `internal/security/astscan_credentials.go` ile 40+ pattern tarama mevcut.

### Command Injection (Git Flag)
**Durum**: ✅ Temiz  
**Gerekçe**: `internal/tools/git.go`'daki `rejectGitFlagInjection()` tüm git argümanlarını korur.

### SSRF
**Durum**: ✅ Temiz  
**Gerekçe**: Web sunucusu yok, dışarı HTTP çağrıları LLM provider'a özel.

### Dependency Vulnerabilities
**Durum**: ✅ Temiz (CI'da Trivy)  
**Gerekçe**: GitHub Actions'ta `aquasecurity/trivy-action` çalışıyor.

---

## Detaylı Kod İnceleme

### Güvenli Noktalar

| Dosya/Fonksiyon | Durum | Not |
|-----------------|-------|-----|
| `engine.New()` | ✅ | Config validation mevcut |
| `config.Load()` | ✅ | Env var override korumalı |
| `storage.New()` | ✅ | BoltDB lock contention handle |
| `hooks.CheckConfigPermissions()` | ✅ | Windows'ta skip, POSIX'te uyarı |
| `hooks.hookEnv()` | ✅ | Payload sanitization |

### Dikkat Edilmesi Gereken Noktalar

| Dosya/Fonksiyon | Risk | Öneri |
|-----------------|------|-------|
| `shellCommand()` | Orta | Shell-free mode tercih et |
| `dfmc init` dosya izinleri | Yüksek | 0o600 kullan |
| Telegram callback handling | Orta | Rate limiter ekle |

---

## Remediation öncelik sırasları

| Öncelik | Bulgu | Tahmini Efor |
|---------|-------|-------------|
| 1 | VULN-001: Hook permission block | 15 dakika |
| 2 | VULN-002: 0o600 dosya izinleri | 10 dakika |
| 3 | VULN-003: Telegram rate limit | 1 saat |
| 4 | VULN-004: Shell mode hardening | 30 dakika |

---

## Referanslar

- OWASP Top 10 2021
- CWE-20: Improper Input Validation
- CWE-78: OS Command Injection
- CWE-200: Exposure of Sensitive Information

---

*Bu rapor DFMC Audit Skill tarafından otomatik olarak oluşturulmuştur.*