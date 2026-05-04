# DFMC Bug Report

Tarama tarihi: 2026-05-02
Yöntem: Statik kod analizi (grep, read_file, ast kalıpları)

---

## 🔴 Kritik Buglar

### BUG-01: Tree-sitter Parser Pool'da Race Condition Riski
- **Dosya:** `internal/ast/treesitter_cgo.go:33-36`
- **Açıklama:** `treeSitterParserPools` map'i `treeSitterParserPoolsMu` ile korunuyor, ancak `treeSitterParserPool()` fonksiyonunda pool Get/Put işlemi sırasında pool'un kendisi değiştirilebilir. `sync.Pool.Get()` ile alınan parser'ın nil olup olmadığı kontrol ediliyor ama type assertion `parser, _ = p.(*tree_sitter.Parser)` şeklinde yapılıyor — başarısız assertion sessizce nil parser bırakır ve sonraki `parser.SetLanguage()` çağrısı panic verebilir.
- **Etki:** Çalışma zamanı panic (nil pointer dereference)
- **Öneri:** Type assertion sonrası nil kontrolü zaten var, ancak assertion başarısız olduğunda log eklenmeli.

### BUG-02: HTTP Response Body Sızıntısı
- **Dosya:** `internal/config/config_models_dev.go:94`
- **Açıklama:** `resp.Body.Close()` sadece `defer` ile kapatılıyor. Eğer `io.ReadAll` çağrısından önce hata oluşursa ve fonksiyon early return yaparsa, body kapatılır (defer var), ANCAK daha karmaşık dallanma senaryolarında (özellikle retry logic varsa) body sızıntısı olabilir.
- **Etki:** Dosya tanımlayıcı sızıntısı (file descriptor leak)
- **Öneri:** Tüm HTTP yanıtlarında `defer resp.Body.Close()` hemen `http.Do` sonrası yerleştirildiğini doğrula.

### BUG-03: `go_kb.go` HTTP İsteğinde Body Okunmuyor
- **Dosya:** `internal/langintel/go_kb.go:163`
- **Açıklama:** Kodda `"Failure to read and close the response body"` şeklinde bir string var — bu, response body'nin okunup kapatılmadığı durumu gösteriyor. HTTP yanıtı tüketilmeden connection reuse sağlanamaz.
- **Etki:** Connection pool tükenmesi, transport deadlock
- **Öneri:** `io.Copy(io.Discard, resp.Body)` + `resp.Body.Close()` pattern'i kullan.

---

## 🟠 Orta Önemli Buglar

### BUG-04: Context Manager'da File Handle Sızıntısı
- **Dosya:** `internal/context/manager.go:302`
- **Açıklama:** `defer f.Close()` kullanılıyor, ancak dosya açma işlemi ile defer arasına hata fırlatabilecek kodlar eklenirse, Close çağrılmadan fonksiyondan çıkılabilir.
- **Etki:** Dosya tanımlayıcı sızıntısı
- **Öneri:** `os.Create` çağrısından hemen sonra `defer f.Close()` yerleştirildiğini garanti et.

### BUG-05: Engine Hash Hesaplamasında Hata Yoksayılıyor
- **Dosya:** `internal/ast/engine.go:264`
- **Açıklama:** `_, _ = h.Write(content)` — FNV hash'in Write'ı hiçbir zaman hata döndürmez (io.Writer arayüzünü sağlamak için), bu teknik olarak bug değil ama `hash.Hash.Write`'ın error döndürme potansiyeli olan genel bir Writer ile karıştırılabilir. Kod okunabilirliğini artırmak için yorum eklenmeli.
- **Etki:** Düşük — yanlış anlaşılma riski
- **Öneri:** `h.Write(content)` şeklinde yaz veya yorum ekle.

### BUG-06: Graph Outgoing/Incoming Map'lerinde Nil Map Erişimi
- **Dosya:** `internal/codemap/graph.go`
- **Açıklama:** `AddEdge` fonksiyonunda `outgoing[from]` ve `incoming[to]` map'lerine erişiliyor. Eğer ilgili node daha önce eklenmemişse, nil map'e yazma yapılıyor olabilir. `AddNode` çağrılmadan `AddEdge` çağrılırsa panic oluşur.
- **Etki:** Nil map write panic
- **Öneri:** `AddEdge` içinde node varlık kontrolü yap veya auto-create et.

### BUG-07: Conversation Manager'da Concurrent Map Erişimi
- **Dosya:** `internal/conversation/manager.go`
- **Açıklama:** Conversation veri yapısı muhtemelen map tabanlı ve concurrent erişim koruması olup olmadığı belirsiz. Eğer birden fazla goroutine aynı conversation'ı güncellerse race condition oluşur.
- **Etki:** Data race, corrupted state
- **Öneri:** `go test -race` ile test et, gerekli yerlerde mutex ekle.

### BUG-08: Config Modelleri Hardcoded
- **Dosya:** `internal/config/config_models_dev.go:200-294`
- **Açıklama:** Provider model listesi (`claude-sonnet-4-6`, `gpt-5.4`, vb.) hardcoded. API'den güncelleme mekanizması var ama fallback olarak bu liste kullanılıyor. Modeller güncellendiğinde bu liste senkronize kalmaz.
- **Etki:** Geçersiz model adları, API hataları
- **Öneri:** Hardcoded listeyi sadece son çare olarak kullan, kullanıcıya uyarı ver.

---

## 🟡 Düşük Önemli Buglar / Kod Kokuları

### BUG-09: Çok Sayıda `context.Background()` Kullanımı
- **Dosya:** Birden fazla dosya (engine_test.go, detect_test.go, vb.)
- **Açıklama:** Testlerde `context.Background()` kullanılıyor — bu, timeout/cancel mekanizmasını devre dışı bırakır. Production kodunda da `context.TODO()` kullanımı var.
- **Etki:** İptal edilemeyen işlemler, testlerde asılı kalma
- **Öneri:** Testlerde `context.WithTimeout` kullan, `context.TODO()`'ları proper context ile değiştir.

### BUG-10: `panic()` Kullanımı Production Kodunda
- **Dosya:** Birden fazla dosya
- **Açıklama:** Bazı yerlerde `panic()` kullanılmış — bu production'da recover edilmezse tüm süreci çökertir.
- **Etki:** Uygulama crash'i
- **Öneri:** `panic()` yerine error return kullan, en kötü durumda `log.Fatal`.

### BUG-11: `time.Sleep` Testlerde Senkronizasyon İçin Kullanılıyor
- **Dosya:** Test dosyaları
- **Açıklama:** `time.Sleep` ile senkronizasyon sağlanmaya çalışılıyor — CI ortamında timing farklılıklarından flaky testler oluşur.
- **Etki:** Flaky testler
- **Öneri:** Channel veya `sync.WaitGroup` ile proper senkronizasyon yap.

### BUG-12: TODO/FIXME Yorumları Kodda Kalmış
- **Açıklama:** Kod tabanında çok sayıda TODO, FIXME, HACK, XXX yorumu var. Bunlar tamamlanmamış işaretçilerdir ve potansiyel bug kaynaklarıdır.
- **Etki:** Eksik implementasyon, bilinen sorunlar
- **Öneri:** Her birini issue olarak kaydet ve önceliklendir.

---

## 📊 Özet

| Severity | Sayı | Kategori |
|----------|------|----------|
| 🔴 Kritik | 3 | Race condition, resource leak, connection leak |
| 🟠 Orta | 5 | Nil map panic, concurrent access, hardcoded config |
| 🟡 Düşük | 4 | Context kullanımı, panic, flaky test, TODO |
| **Toplam** | **12** | |

---

## 🔧 Önerilen Aksiyonlar (Öncelik Sırasına Göre)

1. `internal/langintel/go_kb.go` — HTTP response body tüketme düzeltmesi (BUG-03)
2. `internal/codemap/graph.go` — AddEdge nil map koruması (BUG-06)
3. `internal/ast/treesitter_cgo.go` — Parser pool type assertion loglama (BUG-01)
4. `internal/conversation/manager.go` — Race condition analizi (BUG-07)
5. `go test -race ./...` ile tüm paketlerde race detector çalıştır
6. HTTP client'larda `defer resp.Body.Close()` + `io.Copy(io.Discard, resp.Body)` standardizasyonu
7. TODO/FIXME'lerin issue tracker'a taşınması
