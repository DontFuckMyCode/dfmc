# DFMC — `.env` İnceleme Raporu

**Tarih:** 2026-04-18  
**Kapsam:** `.env` dosyası (30 satır)  
**İlgili dosyalar:** `.env.example`, `internal/config/config.go`, `internal/config/config_test.go`

---

## Düzeltme Gerekli (Must-fix)

### 1. KIMI/MOONSHOT belirsiz öncelik — harita iterasyon sırası tanımsız

**Dosya:** `internal/config/config.go:660-672`

`applyEnv` fonksiyonu `providerAPIEnvVars` haritasını (`map`) iterasyonla dolaşıyor. Go'da map iterasyon sırası nondeterministic olduğundan, hem `KIMI_API_KEY` hem `MOONSHOT_API_KEY` `.env` dosyasında tanımlıysa hangisinin `c.Providers.Profiles["kimi"]` değerini kazanacağı belirsiz.

**Etki:** Kullanıcı iki anahtarı da ayarladığında, her çalıştırmada farklı sonuç çıkabilir.

**Öneri:** `applyEnv` içinde iterasyon öncesi env var isimlerini sırala, veya birden fazla env var'a sahip provider'lar için deterministik bir çözümleme adımı ekle.

```go
// Önerilen düzeltme örneği:
keys := slices.Sorted(maps.Keys(providerAPIEnvVars))
for _, envName := range keys {
    providerName := providerAPIEnvVars[envName]
    // ... mevcut mantık
}
```

---

## İyileştirme Önerileri (Should-fix)

### 2. KIMI/MOONSHOT bölümünde açıklama eksik

**Dosya:** `.env:28-29`

```
# Kimi / Moonshot provider
KIMI_API_KEY=
MOONSHOT_API_KEY=
```

Her iki anahtar da aynı `"kimi"` provider profiline eşleniyor (`config.go:627-628`). `EnvVarForProvider` fonksiyonu `KIMI_API_KEY`'yi döndürüyor (`config.go:641`). Kullanıcının hangisini tercih etmesi gerektiğine dair bir açıklama yok.

**Öneri:** Açıklama satırı ekle:

```
# Kimi / Moonshot provider (KIMI_API_KEY öncelikli; yalnızca biri yeterli)
```

### 3. `.env` ile `.env.example` arasında yorum tutarsızlığı

**Dosya:** `.env:2` vs `.env.example:2`

- `.env:2`: `# Fill in your API keys below.`
- `.env.example:2`: `# Copy this file to .env and fill in your keys.`

`.env.example` kritik talimatı ("Copy this file to .env") içerirken `.env` bu bilgiyi kaybediyor. Kullanıcı `.env` dosyasını ilk kez gördüğünde bağlam eksik kalır.

---

## Güvenlik Değerlendirmesi

### 4. `.env` dosyası git izleme riski

`.gitignore:23-24` satırlarında `.env` ve `.env.local` listeleniyor. Dosya şu anda **izlenmiyor** (untracked), bu doğru. Ancak `git add .` gibi bir komutla yanlışlıkla sahnelenme riski var.

**Öneri:**  
- `.env` dosyasına açıklayıcı yorum ekleyerek kullanıcıyı uyar: `# BU DOSYAYI ASLA COMMIT ETMEYİN`
- Veya pre-commit hook ile `.env` dosyasının sahnelenmesini engelle.

---

## Eksik Testler

### 5. Boş `.env` değerleri regresyon testi yok

**Dosya:** `internal/config/config_test.go`

Mevcut testler `.env` dosyasına doldurulmuş değerlerle test yapıyor (satır 108, 137, 165). `KEY=` (boş değer) durumunun doğru şekilde yoksayıldığını test eden bir senaryo yok.

**Önerilen test:**

```go
func TestLoadWithOptions_DotEnvEmptyValues(t *testing.T) {
    // .env içeriği: ZAI_API_KEY=\n  (boş değer)
    // Beklenen: cfg.Providers.Profiles["zai"].APIKey == "" (ayarlanmamış)
    // Boş string'in yanlışlıkla set edilmediğini doğrula
}
```

### 6. KIMI/MOONSHOT çift-anahtar öncelik testi yok

Hem `KIMI_API_KEY` hem `MOONSHOT_API_KEY` aynı anda `.env` dosyasında bulunduğunda hangisinin kazanacağını test eden senaryo mevcut değil. Bu, yukarıdaki Must-fix #1 ile ilişkili — nondeterministik davranış tespit edildiğinden test de nondeterministik olur.

---

## Özet Tablosu

| Öncelik | Bulgu | Dosya:Satır |
|---------|-------|-------------|
| **Must-fix** | KIMI/MOONSHOT nondeterministic öncelik — map iterasyon sırası tanımsız | `config.go:660-672` |
| Should-fix | KIMI/MOONSHOT bölümünde öncelik açıklaması eksik | `.env:28-29` |
| Should-fix | `.env` ile `.env.example` yorum tutarsızlığı | `.env:2` |
| Güvenlik | `.env` yanlışlıkla commit riski — ek koruma mekanizması yok | `.env:1-4` |
| Test | Boş `.env` değerleri doğru yoksayılıyor (regresyon koruması yok) | `config_test.go` |
| Test | Çift KIMI/MOONSHOT `.env` önceliği (şu an nondeterministic) | `config_test.go` |
