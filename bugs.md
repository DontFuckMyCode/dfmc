# Tool Calling Hataları ve Çözümleri

Bu dosya, DFMC oturumlarında karşılaşılan tool calling hatalarını ve bunların çözümlerini belgeler.

---

## 1. `edit_file` — "old_string not found" Hatası

**Hata Mesajı:**
```
old_string not found in <dosya_yolu>
```

**Kök Neden Zinciri:**

1. **CRLF Mismatch**: Dosya Windows CRLF (`\r\n`) satır sonları kullanıyor. `old_string` parametresinde sadece LF (`\n`) var — bu yüzden tam eşleşme bulunamıyor.

2. **Eksik Whitespace/Indent**: Kod bloklarında indentation farklılıkları (tab vs space, veya eklenmiş boşluklar) eşleşmeyi bozuyor.

3. **TUI Truncation** (`ui/tui/activity_format.go:66`): Error mesajları 42 karaktere kesiliyordu.
   ✅ **DÜZELTİLDİ**: 42 → 256 karakter.

**Örnek Dosya:** `internal/codemap/graph.go` — 4 yerde `} (×3)` syntax hatası vardı:

```go
// Hataya sebep olan pattern:
delete(g.outgoing, edge.From)
} (×3)        // ← syntax error: tek başına statement değil
```

**Çözümler:**

| # | Çözüm | Not |
|---|-------|-----|
| A | `write_file` ile dosyayı tamamen yeniden yaz | Riskli — mevcut değişiklikler kaybolabilir |
| B | read_file → temiz string → edit_file | Önerilen — önce oku, sonra düzenle |

TUI truncation sorunu (42 char) zaten çözüldü → 256 char.

**edit_file Kullanımı İçin Doğru Pattern:**

```go
// 1. read_file ile dosyayı OKU (satır aralığı daralt)
// 2. Satırları direkt kopyala — edit_file parametresine YAPIŞTIR
// 3. CR/LF veya whitespace farkı varsa manually düzelt
// 4. Sadece o satırı hedefle — minimum diff kullan
```

---

## 2. `run_command` — Windows'ta Shell Kullanımı

**Tasarım Notu:**

`run_command` bir shell interpreter çağırmaz — doğrudan `exec.CommandContext` ile binary çalıştırır. Bu Windows'ta çalışır, ancak shell özellikleri (`&&`, `|`, `cd` vb.) desteklenmez.

**Bilinen Sınırlama:**

Windows'ta `bash`, `sh`, `pwsh` gibi shell interpreter'ları `command` veya `args` içinde verilirse, bu interpreter'ların kendisi çalışmaz (argv[0] olarak geçirilir).

**Önemli Kurallar:**

| Kural | Açıklama |
|-------|----------|
| `command` = argv[0] | Sadece binary adı, not shell komutu |
| `args` = argv[1:] | Binary argümanları |
| `dir` ile çalışma dizini | `cd` kullanma, `dir` parametresi kullan |
| Ayrı çağrılar | `&&`, `|` için ayrı tool_call'lar |

**Doğru Kullanım:**

```json
{
  "command": "go",
  "args": ["build", "./..."],
  "dir": "internal/tools"
}
```

**Yanlış Kullanım (Shell Syntax):**

```json
{
  "command": "cd internal/tools && go build ./..."
}
```

---

## 3. `apply_patch` — Parametre Yapısı

**Hata Mesajı:**
```
apply_patch call missing required field 'path' in args
```

**Açıklama:**

`apply_patch` farklı parametreler kullanır — `edit_file`'den farklı olarak `path` gerektirir, `old_string`/`new_string` değil.

**Doğru Kullanım:**

```json
{
  "name": "apply_patch",
  "args": {
    "path": "internal/tools/command.go",
    "patches": [
      {
        "old_string": "// TODO: eski kod",
        "new_string": "// YENİ: güncel kod"
      }
    ]
  }
}
```

**Not:** `edit_file` ile `apply_patch` farklı araçlardır:
- `edit_file`: tek dosyada tek/çoklu string değişikliği
- `apply_patch`: unified diff formatında çoklu hunks

---

## 4. `edit_file` — CRLF Auto-Normalization

**Tasarım:**

`edit_file` otomatik CRLF normalizasyonu yapar:
1. Dosyadaki `\r\n` → `\n` normalize edilir (matching için)
2. Eski content korunur — line endings değişmez

**Sınırlama:**

Auto-normalization sadece line ending farkını düzeltir. Whitespace veya indentation farkı varsa, eşleşme yine de başarısız olur.

**Çözüm:**

1. `read_file` ile dosyayı oku
2. Satırın exact text'ini kopyala
3. Whitespace farkı varsa (tab vs space) manuel düzelt

---

## 5. Syntax Error — `graph.go` Gibi Dosyalarda

**Durum: ✅ DÜZELTİLDİ**

`} (×3)` gibi orphaned closing brace'lar Go dosyalarında BULUNAMADI. Bu sorun önceki bir oturumda düzeltilmiş.

**Geçmişte Yaşanan:**
```go
// Hatalı:
delete(g.outgoing, edge.From)
} (×3)

// Doğru:
delete(g.outgoing, edge.From)
}
```

**Bulgular:**
- Hiçbir .go dosyasında `×3` pattern'u bulunamadı
- graph.go ve diğer dosyalar clean

---

## Genel Best Practice

### edit_file Öncesi Kontrol Listesi

- [ ] `read_file` ile hedef satırı **mutlaka oku**
- [ ] Satır numarasını **exact** belirt (line_start/line_end)
- [ ] `old_string` → **direkt kopyala**, type etme
- [ ] Whitespace/indent farkı varsa `grep_codebase` ile kontrol et
- [ ] Büyük değişiklikler için `apply_patch` + `dry_run: true` kullan

### Tool Seçimi Matrisi

| İhtiyaç | Önerilen Tool | Not |
|---------|---------------|-----|
| Küçük string değişikliği | `edit_file` | Tek dosya, basit değişiklik |
| Çoklu hunk değişikliği | `apply_patch` | Unified diff formatı |
| Yeni dosya oluşturma | `write_file` | Tam dosya içeriği |
| Dosya okuma | `read_file` | Satır aralığı belirt |
| Pattern arama | `grep_codebase` | İçerik arama |
| Build/test kontrol | `run_command` | Tüm platformlarda çalışır |


---

## Düzeltilmesi Gereken Dosyalar

| Dosya | Sorun | Durum |
|-------|-------|-------|
| `internal/codemap/graph.go` | 4 yerde `} (×3)` syntax error | ✅ Düzeltildi (2026-04-28) |
| `ui/tui/activity_format.go:66` | 42 char truncation, error mesajları kesiliyor | ✅ Düzeltildi (42 → 256) |
