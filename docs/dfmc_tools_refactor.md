# DFMC Tools Raporu ve Refactor Planı

> **Oluşturulma tarihi:** Otomatik olarak DFMC tool catalog taraması ile üretildi.
> **Kapsam:** Tüm kayıtlı backend toolları, parametreleri, amaçları, işlevsellik değerlendirmesi ve aktif/pasif refactor planı.

---

## İçindekiler

1. [Yönetici Özeti](#1-yönetici-özeti)
2. [Tool Kategorileri ve Tam Envater](#2-tool-kategorileri-ve-tam-envater)
   - [Okuma / Arama (Read / Search)](#21-okuma--arama)
   - [Düzenleme / Yazma (Edit / Write)](#22-düzenleme--yazma)
   - [Çalıştırma / Doğrulama (Execute / Verify)](#23-çalıştırma--doğrulama)
   - [Git Versiyon Kontrol](#24-git-versiyon-kontrol)
   - [Spec / Planlama](#25-spec--planlama)
   - [Diğer / Meta](#26-diğer--meta)
3. [Tool Başına Detaylı Analiz](#3-tool-başına-detaylı-analiz)
4. [İşlevsellik Değerlendirmesi](#4-işlevsellik-değerlendirmesi)
5. [Aktif / Pasif Yönetimi Refactor Planı](#5-aktif--pasif-yönetimi-refactor-planı)
6. [Uygulama Yol Haritası](#6-uygulama-yol-haritası)

---

## 1. Yönetici Özeti

DFMC (Development Flow Management Copilot) toplamda **32 backend tool** sunmaktadır. Bu toollar 6 ana kategoriye ayrılmıştır:

| Kategori | Tool Sayısı | Risk Seviyesi | Durum |
|---|---|---|---|
| Okuma / Arama | 9 | read (idempotent) | ✅ Aktif |
| Düzenleme / Yazma | 5 | write | ✅ Aktif |
| Çalıştırma / Doğrulama | 3 | execute | ✅ Aktif |
| Git Versiyon Kontrol | 7 | read/write karışık | ✅ Aktif |
| Spec / Planlama | 3 | read | ✅ Aktif |
| Diğer / Meta | 5 | read/execute karışık | ✅ Aktif |

**Toplam:** 32 tool — hepsi şu anda aktif durumda.

---

## 2. Tool Kategorileri ve Tam Envater

### 2.1 Okuma / Arama

Bu kategori kod tabanını keşfetmek ve okumak için kullanılır. Tüm toollar `read` risk seviyesindedir (idempotent, yan etkisiz).

| # | Tool Adı | Amaç | Maliyet | Önerilen Öncelik |
|---|---|---|---|---|
| 1 | `grep_codebase` | Regex ile proje içi arama | io-bound | 🔴 Kritik |
| 2 | `read_file` | Dosya okuma (satır aralığı destekli) | cheap | 🔴 Kritik |
| 3 | `ast_query` | AST tabanlı sembol/imports analizi | io-bound | 🟡 Yüksek |
| 4 | `find_symbol` | Sembol bulma + tam kapsamını döndürme | io-bound | 🔴 Kritik |
| 5 | `glob` | Glob desenine uyan dosyaları listeleme | io-bound | 🟢 Orta |
| 6 | `codemap` | Proje genelinde imza özeti | io-bound | 🟡 Yüksek |
| 7 | `list_dir` | Dizin içeriğini listeleme | io-bound | 🟢 Orta |
| 8 | `semantic_search` | AST düğüm arama | io-bound | 🟡 Yüksek |
| 9 | `project_info` | Proje metadata (Go versiyon, dependency, vb.) | io-bound | 🟢 Orta |

#### Okuma Tool'ları Hiyerarşisi (Maliyet Sırası)

```
grep_codebase (en ucuz) → codemap → find_symbol → read_file (en pahalı)
```

**Kullanım deseni:**
1. `grep_codebase` ile keşif → `find_symbol` ile sembol bulma → `read_file` ile tam bağlam
2. `codemap` ile proje oryantasyonu → detay için `find_symbol` / `read_file`

---

### 2.2 Düzenleme / Yazma

Bu kategori dosya ve kod değişiklikleri için kullanılır. `write` risk seviyesindedir.

| # | Tool Adı | Amaç | Maliyet | Önerilen Öncelik |
|---|---|---|---|---|
| 1 | `edit_file` | Tek nokta string değiştirme | io-bound | 🔴 Kritik |
| 2 | `write_file` | Yeni dosya oluşturma / tam yeniden yazma | io-bound | 🔴 Kritik |
| 3 | `apply_patch` | Unified diff ile çoklu hunk uygulama | io-bound | 🔴 Kritik |
| 4 | `symbol_move` | Sembolü başka dosyaya taşıma | cpu-bound | 🟡 Yüksek |
| 5 | `symbol_rename` | Sembolü proje genelinde yeniden adlandırma | cpu-bound | 🟡 Yüksek |

**Seçim rehberi:**
- Tek satır değişikliği → `edit_file`
- Çoklu hunk / birden fazla dosya → `apply_patch`
- Yeni dosya veya >%50 yeniden yazma → `write_file`
- Sembol taşıma → `symbol_move`
- Sembol yeniden adlandırma → `symbol_rename`

---

### 2.3 Çalıştırma / Doğrulama

| # | Tool Adı | Amaç | Maliyet | Önerilen Öncelik |
|---|---|---|---|---|
| 1 | `run_command` | Build/test/lint komutları çalıştırma | network | 🔴 Kritik |
| 2 | `benchmark` | Go benchmark çalıştırma | read | 🟡 Yüksek |
| 3 | `patch_validation` | Patch dry-run + build doğrulama | execute | 🟡 Yüksek |

---

### 2.4 Git Versiyon Kontrol

| # | Tool Adı | Amaç | Risk | Önerilen Öncelik |
|---|---|---|---|---|
| 1 | `git_commit` | Stage + commit | write | 🔴 Kritik |
| 2 | `git_diff` | Working tree diff | read | 🔴 Kritik |
| 3 | `git_status` | Working tree durumu | read | 🔴 Kritik |
| 4 | `git_log` | Commit geçmişi | read | 🟢 Orta |
| 5 | `git_branch` | Branch listesi + HEAD | read | 🟢 Orta |
| 6 | `git_blame` | Satır bazlı yazarlık bilgisi | read | 🟢 Orta |
| 7 | `git_worktree_add` | Yeni worktree oluşturma | write | ⚪ Düşük |
| 8 | `git_worktree_list` | Worktree listesi | read | ⚪ Düşük |
| 9 | `git_worktree_remove` | Worktree silme | write | ⚪ Düşük |

> **Not:** Git worktree toolları (add/list/remove) özel kullanım senaryolarına hitap eder. Günlük kullanımda nadiren ihtiyaç duyulur.

---

### 2.5 Spec / Planlama

| # | Tool Adı | Amaç | Maliyet | Önerilen Öncelik |
|---|---|---|---|---|
| 1 | `spec_parse` | Markdown spec dosyası okuma | read | 🟡 Yüksek |
| 2 | `spec_validate` | Spec doğrulama (broken links, vb.) | read | 🟡 Yüksek |
| 3 | `spec_to_todo` | Spec checklist → TODO dönüşümü | read | 🟡 Yüksek |

---

### 2.6 Diğer / Meta

| # | Tool Adı | Amaç | Risk | Önerilen Öncelik |
|---|---|---|---|---|
| 1 | `web_fetch` | HTTP GET ile URL içeriği çekme | execute | 🟢 Orta |
| 2 | `test_discovery` | Test dosyalarını keşfetme | read | 🟡 Yüksek |
| 3 | `think` | Akıl yürütme kaydı (yan etki yok) | read | ⚪ Düşük |
| 4 | `tool_search` | Tool keşfi (meta) | read | 🔴 Kritik |
| 5 | `tool_help` | Tool şema okuma (meta) | read | 🔴 Kritik |
| 6 | `tool_call` | Tekil tool çağırma (meta) | — | 🔴 Kritik |
| 7 | `tool_batch_call` | Paralel tool çağırma (meta) | — | 🔴 Kritik |

---

## 3. Tool Başına Detaylı Analiz

### 3.1 `grep_codebase`

| Özellik | Değer |
|---|---|
| **Amaç** | Proje dosyaları içinde regex araması |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |
| **Regex Motoru** | Go RE2 (PCRE değil) |
| **İstisna Dizinler** | `.git`, `node_modules`, `vendor`, `bin`, `dist`, `.dfmc`, `.venv` |

**Parametreler:**
- `pattern` (zorunlu, string) — Regex deseni
- `path` (opsiyonel, string) — Alt dizin kısıtlaması
- `max_results` (opsiyonel, int) — Maks sonuç sayısı
- `case_sensitive` (opsiyonel, bool) — Büyük/küçük harf duyarlılığı
- `context_before` (opsiyonel, int) — Önceki bağlam satırları
- `context_after` (opsiyonel, int) — Sonraki bağlam satırları
- `include` (opsiyonel, string) — Dahil edilen dosya deseni
- `exclude` (opsiyonel, string) — Hariç tutulan dosya deseni

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — En çok kullanılan keşif aracı. Kod tabanında herhangi bir şey bulmak için ilk başvurulacak tool.

---

### 3.2 `read_file`

| Özellik | Değer |
|---|---|
| **Amaç** | Dosya içeriğini okuma (satır aralığı desteği) |
| **Risk** | read (idempotent) |
| **Maliyet** | cheap |

**Parametreler:**
- `path` (zorunlu, string) — Dosya yolu
- `line_start` (opsiyonel, int) — Başlangıç satırı
- `line_end` (opsiyonel, int) — Bitiş satırı

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Temel okuma aracı. Mutation öncesi okuma zorunluluğu ile entegre.

---

### 3.3 `ast_query`

| Özellik | Değer |
|---|---|
| **Amaç** | Kaynak dosyanın AST'sini çözümleme, sembol/import dökümü |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |
| **Desteklenen Diller** | Go, JavaScript, TypeScript, Python (tree-sitter) |

**Parametreler:**
- `path` (zorunlu, string) — Dosya yolu
- `kind` (opsiyonel, string) — Sembol türü filtresi (function, class, struct, ...)
- `name_contains` (opsiyonel, string) — İsme göre filtre (case-insensitive)

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — Dosyanın tamamını okumadan yapısal bakış. Tree-sitter olmadan regex fallback mevcut.

---

### 3.4 `find_symbol`

| Özellik | Değer |
|---|---|
| **Amaç** | Fonksiyon/sınıf/metot bulma ve tam kapsamını döndürme |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |
| **Desteklenen Diller** | Go, JS/TS/JSX/TSX, Python, Java, Rust, C/C++, C#, PHP, Swift, Kotlin, Scala, Ruby, HTML/XML |

**Parametreler:**
- `name` (zorunlu, string) — Sembol adı
- `kind` (opsiyonel, string) — Sembol türü (function, class, method, html_id, ...)
- `path` (opsiyonel, string) — Alt dizin kısıtlaması
- `max_results` (opsiyonel, int) — Maks sonuç (default 5, max 20)
- `include_body` (opsiyonel, bool) — Gövde dahil mi (default true)
- `body_max_lines` (opsiyonel, int) — Gövde satır limiti (default 200)
- `language` (opsiyonel, string) — Dil filtresi

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — İsim bazlı sembol bulma. grep + read_file kombinasyonunun tek çağrıda halledilmesi.

---

### 3.5 `glob`

| Özellik | Değer |
|---|---|
| **Amaç** | Glob desenine uyan dosyaları listeleme |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |

**Parametreler:**
- `pattern` (zorunlu, string) — Glob deseni (`**/*.go` gibi)
- `path` (opsiyonel, string) — Alt dizin kısıtlaması
- `max_results` (opsiyonel, int) — Maks sonuç (default 200, max 2000)

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — Dosya keşfi için hızlı ve güvenilir. İçerik arama için `grep_codebase` ile birleştirilmeli.

---

### 3.6 `codemap`

| Özellik | Değer |
|---|---|
| **Amaç** | Proje genelinde imza bazlı dosya özeti |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |

**Parametreler:**
- `path` (opsiyonel, string) — Alt dizin
- `max_files` (opsiyonel, int) — Maks dosya sayısı (default 200)
- `languages` (opsiyonel, []string) — Dil filtresi

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Yeni bir oturumda oryantasyon için vazgeçilmez. Dosya bazında imzalar + satır numaraları.

---

### 3.7 `list_dir`

| Özellik | Değer |
|---|---|
| **Amaç** | Dizin içeriğini listeleme |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Basit dizin listesi. `glob` ve `codemap` varsa kullanım alanı daralır.

---

### 3.8 `semantic_search`

| Özellik | Değer |
|---|---|
| **Amaç** | AST düğüm türü ve isim deseni ile arama |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — `grep_codebase`'in semantik versiyonu. Yapısal arama ihtiyaçlarında değerli.

---

### 3.9 `project_info`

| Özellik | Değer |
|---|---|
| **Amaç** | Proje metadata (modül, Go versiyon, dependency sayısı, dosya istatistikleri) |
| **Risk** | read (idempotent) |
| **Maliyet** | io-bound |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Oturum başında bir kez kullanışlı. Tekrarlayan kullanım ihtiyacı düşük.

---

### 3.10 `edit_file`

| Özellik | Değer |
|---|---|
| **Amaç** | Dosyada tam string eşleştirme ile değiştirme |
| **Risk** | write |
| **Maliyet** | io-bound |

**Parametreler:**
- `path` (zorunlu, string) — Dosya yolu
- `old_string` (zorunlu, string) — Bulunacak metin (benzersiz olmalı)
- `new_string` (zorunlu, string) — Yeni metin
- `replace_all` (opsiyonel, bool) — Tüm eşleşmeleri değiştir

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Cerrahi edits için varsayılan araç. `old_string` benzersizlik kontrolü ile güvenli.

---

### 3.11 `write_file`

| Özellik | Değer |
|---|---|
| **Amaç** | Yeni dosya oluşturma veya tamamen yeniden yazma |
| **Risk** | write |
| **Maliyet** | io-bound |

**Parametreler:**
- `path` (zorunlu, string) — Dosya yolu
- `content` (zorunlu, string) — Tam dosya içeriği
- `create_dirs` (opsiyonel, bool) — Üst dizinleri oluştur

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Yeni dosya oluşturmak için tek seçenek. Mevcut dosyalarda `edit_file`/`apply_patch` tercih edilmeli.

---

### 3.12 `apply_patch`

| Özellik | Değer |
|---|---|
| **Amaç** | Unified diff formatında çoklu hunk uygulama |
| **Risk** | write |
| **Maliyet** | io-bound |

**Parametreler:**
- `patch` (zorunlu, string) — Unified diff (git stili)
- `dry_run` (opsiyonel, bool) — Sadece önizleme, yazma (default false)

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Birden fazla dosya/ bölgeyi tek atomik operasyonda değiştirmek için en iyi araç.

---

### 3.13 `symbol_move`

| Özellik | Değer |
|---|---|
| **Amaç** | Sembolü başka dosyaya taşıma ve tüm referansları güncelleme |
| **Risk** | write |
| **Maliyet** | cpu-bound |

**Parametreler:**
- `from` (zorunlu, string) — Taşınacak sembol adı
- `to_file` (zorunlu, string) — Hedef dosya yolu
- `to` (opsiyonel, string) — Yeni sembol adı
- `kind` (opsiyonel, string) — func | type | var | const | method | all
- `dry_run` (opsiyonel, bool) — Önizleme (default true)
- `skip_tests` (opsiyonel, bool) — Test dosyalarını atla (default false)

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — Refactoring operasyonlarında çok güçlü. Etki analizi (dry_run) ile güvenli.

---

### 3.14 `symbol_rename`

| Özellik | Değer |
|---|---|
| **Amaç** | Sembolü dosya veya proje genelinde yeniden adlandırma |
| **Risk** | write |
| **Maliyet** | cpu-bound |

**Parametreler:**
- `from` (zorunlu, string) — Mevcut sembol adı
- `to` (zorunlu, string) — Yeni sembol adı
- `file` (opsiyonel, string) — Dosya kısıtlaması (yoksa proje geneli)
- `dry_run` (opsiyonel, bool) — Önizleme (default true)

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — Toplu yeniden adlandırma için etkili. Scope algılama ile güvenli.

---

### 3.15 `run_command`

| Özellik | Değer |
|---|---|
| **Amaç** | Sandbox içinde binary çalıştırma (build/test/lint) |
| **Risk** | execute |
| **Maliyet** | network |

**Parametreler:**
- `command` (zorunlu, string) — Binary adı (argv[0])
- `args` (zorunlu, string | []string) — Argümanlar
- `dir` (opsiyonel, string) — Çalışma dizini
- `timeout_ms` (opsiyonel, int) — Zaman aşımı

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Build, test, lint için zorunlu. Shell yok — sadece direkt binary çalıştırma.

---

### 3.16 `benchmark`

| Özellik | Değer |
|---|---|
| **Amaç** | Go benchmark çalıştırma ve yapılandırılmış sonuç döndürme |
| **Risk** | read |
| **Maliyet** | — |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Sadece Go projeleri için. Performans regresyon testlerinde değerli.

---

### 3.17 `patch_validation`

| Özellik | Değer |
|---|---|
| **Amaç** | Patch dry-run + build/test doğrulama |
| **Risk** | execute |
| **Maliyet** | — |

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — `apply_patch` sonrası doğrulama için güvenlik ağı.

---

### 3.18 `git_commit`

| Özellik | Değer |
|---|---|
| **Amaç** | Belirtilen dosyaları stage edip commit oluşturma |
| **Risk** | write |

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Versiyon kontrol için vazgeçilmez.

---

### 3.19 `git_diff`

| Özellik | Değer |
|---|---|
| **Amaç** | Working tree veya revision bazlı diff |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Değişiklik incelemesi için kritik.

---

### 3.20 `git_status`

| Özellik | Değer |
|---|---|
| **Amaç** | Working tree durumu (porcelain v1) |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐⭐⭐ (5/5) — Her operasyon öncesi durum kontrolü için gerekli.

---

### 3.21 `git_log`

| Özellik | Değer |
|---|---|
| **Amaç** | Son commit'leri listeleme (hash, author, subject) |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Geçmiş incelemesi için faydalı ama günlük kullanımda az.

---

### 3.22 `git_branch`

| Özellik | Değer |
|---|---|
| **Amaç** | Branch listesi ve mevcut HEAD |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Branch yönetimi ihtiyaçlarında kullanışlı.

---

### 3.23 `git_blame`

| Özellik | Değer |
|---|---|
| **Amaç** | Satır bazlı yazarlık bilgisi (hash, author, time, subject) |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Kod arkeolojisi ve sorumluluk tespiti için.

---

### 3.24 `git_worktree_add`

| Özellik | Değer |
|---|---|
| **Amaç** | Yeni bağlı worktree oluşturma |
| **Risk** | write |

**İşlevsellik:** ⭐⭐ (2/5) — Paralel çalışma akışlarında nadiren kullanılır.

---

### 3.25 `git_worktree_list`

| Özellik | Değer |
|---|---|
| **Amaç** | Mevcut worktree'leri listeleme |
| **Risk** | read |

**İşlevsellik:** ⭐⭐ (2/5) — `git_worktree_add` ile birlikte kullanılır.

---

### 3.26 `git_worktree_remove`

| Özellik | Değer |
|---|---|
| **Amaç** | Worktree silme ve ayırma |
| **Risk** | write |

**İşlevsellik:** ⭐⭐ (2/5) — Temizlik operasyonlarında kullanılır.

---

### 3.27 `spec_parse`

| Özellik | Değer |
|---|---|
| **Amaç** | Markdown spec dosyasını okuma ve heading + task-list indeksi |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — Spec-driven development için temel araç.

---

### 3.28 `spec_validate`

| Özellik | Değer |
|---|---|
| **Amaç** | Spec dosyasında bozuk link/anchor, hatalı format tespiti |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Spec kalite kontrolü. Belirli senaryolarda kullanışlı.

---

### 3.29 `spec_to_todo`

| Özellik | Değer |
|---|---|
| **Amaç** | Markdown spec checklist'lerini TODO objelerine dönüştürme |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Spec → görev takibi entegrasyonu.

---

### 3.30 `web_fetch`

| Özellik | Değer |
|---|---|
| **Amaç** | HTTP GET ile URL içeriğini çekme (HTML → text) |
| **Risk** | execute |

**İşlevsellik:** ⭐⭐⭐ (3/5) — Dış kaynak erişimi. Belirli ihtiyaçlarda kullanışlı.

---

### 3.31 `test_discovery`

| Özellik | Değer |
|---|---|
| **Amaç** | Kaynak dosya/dizin/sembol için test dosyalarını ve test fonksiyonlarını keşfetme |
| **Risk** | read |

**İşlevsellik:** ⭐⭐⭐⭐ (4/5) — Test coverage analizi ve refactoring doğrulaması için önemli.

---

### 3.32 `think`

| Özellik | Değer |
|---|---|
| **Amaç** | Akıl yürütme adımını tool trace'e kaydetme (yan etki yok) |
| **Risk** | read |

**İşlevsellik:** ⭐⭐ (2/5) — Loglama amaçlı. Operasyonel katkısı sınırlı.

---

## 4. İşlevsellik Değerlendirmesi

### 4.1 Kritik Tools (Her Zaman Aktif Olmalı)

Bu toollar DFMC'nin temel işlevselliğini oluşturur. **Asla pasif edilmemelidir.**

| Tool | Neden Kritik |
|---|---|
| `grep_codebase` | Kod keşfinin ilk adımı, alternatifi yok |
| `read_file` | Dosya okuma, mutation guard'ın parçası |
| `edit_file` | Cerrahi kod değişikliklerinin tek yolu |
| `write_file` | Yeni dosya oluşturma, alternatifi yok |
| `apply_patch` | Çoklu dosya değişiklikleri, atomik operasyon |
| `find_symbol` | Sembol bulma + kapsam, grep'in semantik versiyonu |
| `run_command` | Build/test/lint, doğrulama zincirinin parçası |
| `git_commit` | Versiyon kontrol temeli |
| `git_diff` | Değişiklik incelemesi |
| `git_status` | Working tree durumu |
| `tool_search` | Tool keşif mekanizması |
| `tool_help` | Tool şema erişimi |
| `tool_call` | Tool çağırma altyapısı |
| `tool_batch_call` | Paralel çağırma optimizasyonu |

### 4.2 Yüksek Öncelikli Tools (Genellikle Aktif)

| Tool | Neden Yüksek |
|---|---|
| `ast_query` | Yapısal analiz, `find_symbol` ve `codemap` destekçisi |
| `codemap` | Proje oryantasyonu, oturum başında kritik |
| `symbol_move` | Refactoring operasyonları için benzersiz yetenek |
| `symbol_rename` | Toplu yeniden adlandırma, güvenli kapsam algılama |
| `semantic_search` | Yapısal arama, `grep_codebase`'i tamamlar |
| `benchmark` | Performans regresyon testi (Go projeleri) |
| `patch_validation` | Patch güvenlik ağı |
| `test_discovery` | Test coverage ve doğrulama |
| `spec_parse` | Spec-driven workflow temeli |
| `spec_validate` | Spec kalite güvencesi |
| `spec_to_todo` | Spec → görev dönüşümü |

### 4.3 Orta Öncelikli Tools (Duruma Bağlı)

| Tool | Ne Zaman Aktif |
|---|---|
| `glob` | Dosya keşfi ihtiyacı olduğunda |
| `list_dir` | Dizin yapısı incelemesi |
| `project_info` | Oturum başında orientasyon |
| `git_log` | Commit geçmişi incelemesi |
| `git_branch` | Branch yönetimi |
| `git_blame` | Kod arkeolojisi |
| `web_fetch` | Dış kaynak erişimi |

### 4.4 Düşük Öncelikli Tools (Nadiren Kullanılır)

| Tool | Ne Zaman Aktif |
|---|---|
| `git_worktree_add` | Paralel çalışma akışı |
| `git_worktree_list` | Worktree durumu |
| `git_worktree_remove` | Worktree temizliği |
| `think` | Akıl yürütme kaydı |

---

## 5. Aktif / Pasif Yönetimi Refactor Planı

### 5.1 Mevcut Durum

Şu anda **tüm 32 tool aktif** durumdadır. Her tool her zaman çağrılabilir ve kullanılabilir. Bu durum:
- ✅ **Avantaj:** Tam esneklik, her senaryoda tüm araçlar mevcut
- ❌ **Dezavantaj:** Tool keşif sürecinde gürültü, gereksiz token tüketimi, karmaşık tool seçimi

### 5.2 Önerilen Refactor: Katmanlı Aktifleştirme Sistemi

#### Katman 1: Her Zaman Aktif (Core Layer)

**Koşul:** Hiçbir koşul yok. DFMC başladığında bu toollar otomatik aktif.

```
grep_codebase, read_file, edit_file, write_file, apply_patch,
find_symbol, run_command, git_commit, git_diff, git_status,
tool_search, tool_help, tool_call, tool_batch_call
```

**Toplam:** 14 tool

#### Katman 2: Skill-Bazlı Aktifleştirme (Skill Layer)

Her DFMC skill'i (refactor, debug, test, vb.) kendi aktif tool setini tanımlar:

| Skill | Ek Aktif Tools |
|---|---|
| `refactor` | `symbol_move`, `symbol_rename`, `ast_query`, `codemap`, `patch_validation` |
| `debug` | `benchmark`, `test_discovery`, `ast_query` |
| `test` | `test_discovery`, `benchmark`, `patch_validation` |
| `review` | `git_blame`, `git_log`, `semantic_search` |
| `generate` | `glob`, `codemap`, `semantic_search` |
| `doc` | `codemap`, `find_symbol`, `ast_query` |
| `explain` | `codemap`, `ast_query`, `find_symbol`, `semantic_search` |
| `audit` | `grep_codebase`, `semantic_search`, `git_blame` |
| `onboard` | `codemap`, `project_info`, `list_dir` |

#### Katman 3: İsteğe Bağlı (On-Demand Layer)

Varsayılan olarak pasif. Kullanıcı veya skill açıkça istediğinde aktifleşir:

```
list_dir, glob, project_info, git_log, git_branch, git_blame,
web_fetch, think, spec_parse, spec_validate, spec_to_todo,
git_worktree_add, git_worktree_list, git_worktree_remove
```

#### Katman 4: Koşullu (Conditional Layer)

Belirli koşullar sağlandığında otomatik aktifleşir:

| Tool | Aktifleşme Koşulu |
|---|---|
| `benchmark` | Proje Go dili ise |
| `spec_parse` / `spec_validate` / `spec_to_todo` | Projede `.md` spec dosyası varsa |
| `git_worktree_*` | Kullanıcı worktree talep ederse |

### 5.3 Refactor Uygulama Planı

#### Aşama 1: Tool Profil Tanımlayıcısı Oluştur

Her tool için bir profil yapısı oluştur:

```yaml
# Örnek tool profil yapısı
tools:
  grep_codebase:
    category: read_search
    priority: critical
    layer: core
    default_active: true
    skills: [all]
    conditions: []
    
  git_worktree_add:
    category: git
    priority: low
    layer: on_demand
    default_active: false
    skills: []
    conditions: [user_request]
    
  benchmark:
    category: execute_verify
    priority: high
    layer: conditional
    default_active: false
    skills: [debug, test]
    conditions: [language=go]
```

#### Aşama 2: Skill → Tool Mapping Modülü

Her skill başlangıcında aktif tool setini belirleyen bir mapping modülü:

```python
# Sözde kod
SKILL_TOOL_MAP = {
    "refactor": CORE_TOOLS + ["symbol_move", "symbol_rename", "ast_query", "codemap", "patch_validation"],
    "debug": CORE_TOOLS + ["benchmark", "test_discovery", "ast_query"],
    # ...
}

def activate_tools_for_skill(skill_name):
    active_tools = set(SKILL_TOOL_MAP.get(skill_name, CORE_TOOLS))
    # Koşullu toolları kontrol et
    for tool in CONDITIONAL_TOOLS:
        if check_conditions(tool.conditions):
            active_tools.add(tool.name)
    return active_tools
```

#### Aşama 3: Pasif Tool Guard Mekanizması

Pasif tool çağrıldığında net bir hata mesajı:

```
Tool "git_worktree_add" şu anda pasif durumda.
Aktifleştirmek için: skill=refactor ile çalıştırın veya kullanıcı talep edin.
Mevcut aktif toollar: [list...]
```

#### Aşama 4: Runtime Tool Keşfi Optimizasyonu

`tool_search` sonuçlarını mevcut aktif set ile filtreleme:
- Aktif toollar → tam detay ile göster
- Pasif toollar → sadece isim ve kısa açıklama, "pasif" etiketi
- Koşullu toollar → aktifleşme koşulu ile göster

#### Aşama 5: Token Bütçesi Entegrasyonu

Aktif tool sayısına göre token bütçesini optimize et:
- Core Layer aktifken: tool_search sonuçları minimum (14 tool)
- Skill Layer aktifken: ortalama ~20 tool
- On-demand dahil: tüm 32 tool

### 5.4 Pasif Edilecek Tools ve Gerekçeleri

| Tool | Pasif Edilme Gerekçesi | Aktifleşme Yolu |
|---|---|---|
| `git_worktree_add` | Çok özel kullanım senaryosu | Kullanıcı talebi |
| `git_worktree_list` | Worktree ile birlikte kullanılır | `git_worktree_add` ile otomatik |
| `git_worktree_remove` | Temizlik operasyonu | Worktree varsa otomatik |
| `think` | Operasyonel katkısı sınırlı | Her zaman çağrılabilir ama keşif sonuçlarında gösterilmez |
| `spec_validate` | Sadece spec dosyaları mevcutken | Spec dosyası tespiti |
| `spec_to_todo` | Spec workflow parçası | Spec parse sonrası otomatik |
| `project_info` | Oturum başında bir kez yeterli | Skill başlangıcı veya kullanıcı talebi |

---

## 6. Uygulama Yol Haritası

### Sprint 1: Envanter ve Profil (1-2 gün)
- [ ] Tüm 32 tool için YAML profil dosyaları oluştur
- [ ] Kategori, öncelik, katman, beceri eşlemesi tanımla
- [ ] Mevcut tool kullanım istatistiklerini topla (varsa)

### Sprint 2: Katmanlı Aktifleştirme Altyapısı (2-3 gün)
- [ ] Core Layer sabit setini tanımla ve uygula
- [ ] Skill → Tool mapping modülünü implement et
- [ ] Aktif/pasif runtime kontrol mekanizmasını ekle
- [ ] Pasif tool guard hata mesajlarını oluştur

### Sprint 3: Koşullu Aktifleştirme (1-2 gün)
- [ ] Proje dili tespiti → `benchmark` otomatik aktifleştirme
- [ ] Spec dosyası tespiti → spec toollarını aktifleştirme
- [ ] Worktree talebi → worktree toollarını aktifleştirme

### Sprint 4: Optimizasyon ve Doğrulama (1-2 gün)
- [ ] `tool_search` filtreleme entegrasyonu
- [ ] Token bütçesi optimizasyonu
- [ ] Skill bazlı test senaryoları yaz
- [ ] Geriye dönük uyumluluk kontrolü

### Sprint 5: Dokümantasyon ve Rollout (1 gün)
- [ ] Tool katalog dokümantasyonunu güncelle
- [ ] Migration rehberi yaz
- [ ] Eski davranışa dönüş (fallback) mekanizmasını test et

---

## Ek: Hızlı Referans Tablosu

| Tool | Kategori | Risk | Katman | İşlevsellik |
|---|---|---|---|---|
| `grep_codebase` | Read/Search | read | Core | ⭐⭐⭐⭐⭐ |
| `read_file` | Read/Search | read | Core | ⭐⭐⭐⭐⭐ |
| `ast_query` | Read/Search | read | Skill | ⭐⭐⭐⭐ |
| `find_symbol` | Read/Search | read | Core | ⭐⭐⭐⭐⭐ |
| `glob` | Read/Search | read | On-Demand | ⭐⭐⭐⭐ |
| `codemap` | Read/Search | read | Skill | ⭐⭐⭐⭐⭐ |
| `list_dir` | Read/Search | read | On-Demand | ⭐⭐⭐ |
| `semantic_search` | Read/Search | read | Skill | ⭐⭐⭐⭐ |
| `project_info` | Read/Search | read | On-Demand | ⭐⭐⭐ |
| `edit_file` | Edit/Write | write | Core | ⭐⭐⭐⭐⭐ |
| `write_file` | Edit/Write | write | Core | ⭐⭐⭐⭐⭐ |
| `apply_patch` | Edit/Write | write | Core | ⭐⭐⭐⭐⭐ |
| `symbol_move` | Edit/Write | write | Skill | ⭐⭐⭐⭐ |
| `symbol_rename` | Edit/Write | write | Skill | ⭐⭐⭐⭐ |
| `run_command` | Execute | execute | Core | ⭐⭐⭐⭐⭐ |
| `benchmark` | Execute | read | Conditional | ⭐⭐⭐ |
| `patch_validation` | Execute | execute | Skill | ⭐⭐⭐⭐ |
| `git_commit` | Git | write | Core | ⭐⭐⭐⭐⭐ |
| `git_diff` | Git | read | Core | ⭐⭐⭐⭐⭐ |
| `git_status` | Git | read | Core | ⭐⭐⭐⭐⭐ |
| `git_log` | Git | read | On-Demand | ⭐⭐⭐ |
| `git_branch` | Git | read | On-Demand | ⭐⭐⭐ |
| `git_blame` | Git | read | On-Demand | ⭐⭐⭐ |
| `git_worktree_add` | Git | write | On-Demand | ⭐⭐ |
| `git_worktree_list` | Git | read | On-Demand | ⭐⭐ |
| `git_worktree_remove` | Git | write | On-Demand | ⭐⭐ |
| `spec_parse` | Spec | read | Conditional | ⭐⭐⭐⭐ |
| `spec_validate` | Spec | read | Conditional | ⭐⭐⭐ |
| `spec_to_todo` | Spec | read | Conditional | ⭐⭐⭐ |
| `web_fetch` | Other | execute | On-Demand | ⭐⭐⭐ |
| `test_discovery` | Other | read | Skill | ⭐⭐⭐⭐ |
| `think` | Meta | read | On-Demand | ⭐⭐ |
| `tool_search` | Meta | read | Core | ⭐⭐⭐⭐⭐ |
| `tool_help` | Meta | read | Core | ⭐⭐⭐⭐⭐ |
| `tool_call` | Meta | execute | Core | ⭐⭐⭐⭐⭐ |
| `tool_batch_call` | Meta | execute | Core | ⭐⭐⭐⭐⭐ |

---

*Bu rapor DFMC tool catalog taraması ile otomatik olarak oluşturulmuştur.*
*Toplam incelenen tool: 32 | Kategori: 6 | Refactor planı aşama: 5 sprint*
