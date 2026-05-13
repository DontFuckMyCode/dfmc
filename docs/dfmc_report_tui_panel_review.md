# DFMC TUI Panel İnceleme Raporu

Kapsam: `ui/tui` odaklıdır. CLI ve web bilinçli olarak dışarıda bırakıldı. Amaç, TUI ekranlarında gerçekten kullanıcıyı yoran, yanlış aksiyona götüren, ekrandan taşan veya zihinsel modeli bozan noktaları çıkarmak.

## Kısa Sonuç

TUI tarafı artık basit bir terminal arayüzü değil, kendi içinde büyük bir uygulama olmuş: `ui/tui` altında 316 dosya var. Temel sorun tek tek panellerin kötü olması değil; panellerin aynı kurallarla davranmaması.

En kritik üç problem:

1. Filtreli listelerde ekranda seçili görünen satır ile aksiyonun uygulandığı gerçek satır bazı panellerde ayrışabiliyor.
2. Birçok V2 panel hala sabit `height := 24` veya `const modelWindow = 12` ile render ediliyor; dış layout sonra bunu kırpıyor. Sonuç: içerik ekrana göre yeniden akmak yerine kesiliyor.
3. Kullanım modeli çok kalabalık: F tuşları, Shift+F, Alt, Ctrl, tek harf, eski alias, panel switcher, action menu ve slash command aynı anda yaşıyor. Kullanıcı “ok, enter, escape, space” modeli isterken TUI hala vim-benzeri tek harf yüzeyine dayanıyor.

## Öncelikli Bulgular

### P0 - Ekranda Seçili Olan ile Çalışan Nesne Ayrışabiliyor

Providers panelinde liste filtreleniyor ama birçok aksiyon ham `m.providers.rows[scroll]` üzerinden çalışıyor.

İlgili yerler:

- `ui/tui/provider_panel_key_list.go:133` filtreli liste hesaplanıyor.
- `ui/tui/provider_panel_key_list.go:176`, `184`, `191`, `201`, `216`, `223` aksiyonlar ham `m.providers.rows[scroll]` kullanıyor.
- `ui/tui/provider_panel_menus.go:33` ve `ui/tui/provider_panel_menu.go:106` civarında legacy menü de aynı riskli paterni sürdürüyor.

Etkisi: arama filtresi açıkken ekranda `zai-coding-plan` seçili görünür, ama `primary`, `fallback`, `model cycle`, `save`, `test`, `detail` başka provider'a uygulanabilir.

Öneri: Provider selection `provider name/id` üzerinden tutulmalı. Filtreli listede `selectedProviderName` seçilmeli; aksiyonlar asla filtreli indeksin ham listeye denk geldiğini varsaymamalı.

Files panelinde aynı model var.

- `ui/tui/render_files.go:110` filtreli dosyalar render ediliyor.
- `ui/tui/render_files.go:135` ve `179` seçim filtreden gelen indeks ile karşılaştırılıyor.
- `ui/tui/mention_helpers.go:157` `selectedFile()` ham `m.filesView.entries[m.filesView.index]` kullanıyor.
- `ui/tui/panel_keys.go:27`, `35`, `43`, `45` navigation/action ham liste indeksine bağlı.

Etkisi: dosya filtresi varken seçili görünen dosyaya değil, ham listedeki aynı indekse aksiyon uygulanabilir. `pin`, `insert`, `explain`, `review`, preview yanlış dosyaya gidebilir.

Öneri: Files state içinde `selectedPath` tutulmalı. Render, preview ve action aynı `selectedPath` üzerinden çalışmalı.

### P0 - Tools Panel Scroll Seçimi Takip Etmiyor

Tools panelinde index değişiyor ama render scroll'u takip etmiyor.

- `ui/tui/render_tools.go:110` `start := m.toolView.scroll` hesaplanıyor.
- `ui/tui/render_tools.go:116` hesaplanan `start` yerine tekrar `scroll` ile döngü kuruluyor.
- `ui/tui/panel_keys.go` içinde Tools seçiminde `m.toolView.index` artıp azalıyor ama `m.toolView.scroll` güncellenmiyor.

Etkisi: tool listesi uzunsa seçili tool görünür alanın dışına çıkar. Sağdaki spec değişir ama soldaki listede kullanıcı neyi seçtiğini göremez.

Öneri: Tools panelinde `scrollWindow(m.toolView.index, len(tools), rowBudget)` kullanılmalı; ayrıca ayrı `scroll` state gerekiyorsa index değişince clamp edilerek görünür alana çekilmeli.

### P1 - V2 Paneller Responsive Değil, Sonradan Kırpılıyor

Birçok panel ekrana göre render etmek yerine içeride sabit yükseklikle çiziliyor:

- `ui/tui/render_providers_v2.go:36` `height := 24`
- `ui/tui/render_tools.go:26` `height := 24`
- `ui/tui/render_workflow_v2.go:35` `height := 24`
- `ui/tui/render_patch.go:35` `height := 24`
- `ui/tui/tool_introspect.go:188` `height := 24`
- `ui/tui/provider_panel_render_detail.go:146` `const modelWindow = 12`

Dış layout ise örneğin `ui/tui/render_layout.go:68`, `70`, `72`, `80` ile bunları `fitPanelContentHeight` üzerinden kırpıyor.

Etkisi: küçük terminalde içerik “scroll edilebilir bir panel” gibi davranmıyor, sadece alt kısımlar kayboluyor. Kullanıcı özellikle Providers/Workflow/Tools gibi yoğun ekranlarda neyin dışarıda kaldığını anlayamıyor.

Öneri: Tüm panel renderer imzaları `renderX(width, height)` olmalı. Her pane kendi `rowBudget` değerini gerçek `height` üzerinden almalı. Dış `fitPanelContentHeight` son savunma olmalı, layout stratejisi olmamalı.

### P1 - Workflow TODO Tree Scroll Kırık

Workflow tree tarafında `scrollY` var, key handler bunu güncelliyor, ama render tarafı satırları hep baştan kırpıyor.

- `ui/tui/render_workflow_keys.go:132` `handleWorkflowKey`
- `ui/tui/render_workflow_keys.go:161`, `172`, `181` `scrollY` güncelleniyor.
- `ui/tui/render_workflow_v2.go:252` `rowBudget`
- `ui/tui/render_workflow_v2.go:254` `if len(rows) > rowBudget { rows = rows[:rowBudget] }`

Etkisi: kullanıcı aşağı indikçe seçili TODO state olarak değişebilir ama tree görünümü üstten kırpıldığı için seçili TODO ekranda kalmayabilir.

Öneri: `renderWorkflowTreeRows` çıktısı `scrollWindow(m.workflow.scrollY, len(rows), rowBudget)` ile kesilmeli. Seçili TODO ve expanded TODO ayrımı da ID üzerinden tutulmalı.

### P1 - Provider Panel İki Ayrı Menü Sistemiyle Yaşıyor

Provider panelinde global `actionMenu` var, ama eski `m.providers.menuActive` hala tutuluyor.

- `ui/tui/provider_panel_render.go:78` menu aktif değilse V2 render.
- `ui/tui/provider_panel_render.go:87` menu aktifse legacy render.
- `ui/tui/provider_panel_render_legacy.go:108` legacy liste hala canlı.
- `ui/tui/panel_action_menu.go` zaten ortak arrow-driven action menu sunuyor.

Etkisi: kullanıcı menü açınca ekranın görsel dili değişiyor. V2 kart düzeninden legacy liste düzenine düşmek “neredeyim?” hissini bozuyor.

Öneri: `providers.menuActive` ve legacy provider menüsü kaldırılmalı. Providers yalnızca ortak `panelActionMenu` kullanmalı. Legacy render dosyası ancak test snapshot geçişi için kısa süre tutulabilir, ürün akışında olmamalı.

### P1 - Kısayol Modeli Kullanıcı İsteğiyle Çelişiyor

Kullanıcı modeli net olmalı: ok tuşları hareket, Enter aksiyon/seçim, Esc geri/kapat, Space toggle/follow. Gerekiyorsa panel özel `Alt+...`.

Mevcut durumda:

- Global: `ui/tui/update_keypress_shortcuts.go` içinde F1-F12, Shift+F1-F7, Alt+1..8, Alt harfler, Ctrl harfler birlikte yaşıyor.
- Files: `ui/tui/panel_keys.go:20` sonrası `r`, `j/k`, `p`, `i/e/v`.
- Tools: `ui/tui/panel_keys.go` içinde `j/k`, `e`, `x`, `r`.
- Providers: `ui/tui/provider_panel_key_list.go` içinde `p/f/m/s/T/d/n/r/c`.
- Security: `ui/tui/security_keys.go:206` sonrası `j/k/g/G/v/r/c/i/f`.
- Activity: `ui/tui/render_activity_v2.go:38` hint satırı `j/k`, `r`, `f`, `y`, `1-6`, `/` gösteriyor.

Etkisi: TUI terminal-native gibi değil, gizli komut ezberi gibi hissediyor. Ayrıca Türkçe klavye/AltGr/paste sorunları için sürekli istisna yazılıyor.

Öneri: Tek harf aksiyonları ürün yüzeyinden kaldır. Aksiyonlar Enter/Right ile açılan menüde listelensin. Panel içinde sadece:

- Up/Down/Left/Right: hareket ve pane geçişi
- Enter: seç veya action menu
- Esc: geri/kapat
- Space: takip/toggle/seçim işaretleme
- Ctrl+F veya `/`: arama; tek standart seçilmeli
- Panel özel hızlı yol gerekiyorsa `Alt+P`, `Alt+R` gibi açık, ekrana özel ve help’te görünen kombinasyonlar

## Panel Bazlı Değerlendirme

### Chat

Chat ana ekran ama varsayılan olarak çok ağır başlıyor.

- `ui/tui/tui_lifecycle.go:50` `showStatsPanel: true`
- `ui/tui/tui_lifecycle.go:53` `toolStripExpanded: true`
- `ui/tui/render_layout.go:90` stats panel görünürse chat genişliği azaltılıyor.
- `ui/tui/render_chat_meta.go:134` stats panel görünürlüğü genişliğe bağlı.

Sorun: Chat aynı anda konuşma, composer, slash command, mention picker, stats panel, task overlay ve tool strip taşıyor. Dar terminalde asıl iş olan mesaj alanı eziliyor.

Öneri: Stats panel ve tool strip varsayılan kapalı olmalı. Sadece aktif drive, hata, token bütçesi veya provider problemi olduğunda geçici olarak açılmalı. Kullanıcı kapattıysa o tercih kalıcı olmalı.

### Providers

Bu ekran en fazla ürüne dönüşmesi gereken yer. Mevcut kodda doğru parçalar var ama akış temiz değil.

Sorunlar:

- models.dev sync “reference catalog” gibi davranmak yerine profil alanlarını değiştirebiliyor: `ui/tui/provider_panel_crud.go:59` `RewriteBaseURL: true`.
- Yeni provider yaratma sadece minimal `openai-compatible` profil ekliyor: `ui/tui/provider_panel_crud.go:105`.
- List/detail/model picker/pipeline/profile edit/key edit hepsi tek state objesinde büyümüş.
- API key ve profil edit backspace yer yer byte kesiyor: `ui/tui/provider_panel_key_edit.go:132`, `178`, `220`. Unicode veya paste girdilerinde riskli.

Olması gereken akış:

1. My Providers ekranı: aktif kullanıcı provider’ları.
2. Add Provider:
   - models.dev catalog’dan seç
   - custom provider ekle
3. Provider formu:
   - display name
   - protocol/compatible: OpenAI, OpenAI-compatible, Anthropic, Gemini
   - endpoint/base URL
   - API key, paste exact string olarak
   - enabled
4. Models sekmesi:
   - models.dev ref varsa modelleri ve context/fiyat/yetenek bilgisini göster
   - custom model ID ekle
   - model test/probe
5. Routing sekmesi:
   - tier primary/fallback ataması
   - skill primary/fallback ataması
6. Save davranışı:
   - models.dev cache sadece referans
   - user-owned provider config açık onay olmadan overwrite edilmez

### Files

Görsel olarak iyi niyetli bir 3-pane explorer var, ama selection/filter bug’ı kritik.

Sorunlar:

- Filtreli liste ile ham selection ayrışıyor.
- Preview sadece üstten kırpılıyor; dosya içi bağımsız preview scroll yok.
- Actions kartı `i/e/v/p/r` ezberini tekrar ediyor.

Öneri: `selectedPath`, `listScroll`, `previewScroll`, `query` ayrı tutulmalı. Enter action menu açmalı; Space pin/unpin olabilir. Preview pane PgUp/PgDn veya Right/Left focus ile kaymalı.

### Tools

Tools paneli manuel harness olarak faydalı ama şu anda kullanıcıyı kolayca kaybettiriyor.

Sorunlar:

- Scroll selection takip etmiyor.
- Param editor düz text buffer; JSON/YAML doğrulama net bir alt panel olarak görünmüyor.
- Tool spec ve last result aynı pane içinde üst üste biniyor.

Öneri: Sol liste gerçek scroll window ile çalışmalı. Orta pane sadece spec/args, sağ pane current params + validation + last run result olmalı. Enter action menu, Space “run selected” veya “toggle edit” için kullanılabilir.

### Workflow

Workflow, Drive/autonomy için ana cockpit olmalı. Şu an doğru yönde ama tree scroll ve yoğun chip kullanımı yıpratıyor.

Sorunlar:

- Tree scroll render’da uygulanmıyor.
- Banner her zaman `running/planning/done/failed/stopped` chiplerini gösteriyor; sıfırlar gürültü yaratıyor.
- `renderWorkflowRunRow` dar genişlikte iki satır döndürebiliyor; liste row budget hesapları tek satır varsayarken bu kayma yaratabilir.

Öneri: Workflow Drive’ın ana evi olmalı. Activity’den drive/agent event açılınca Plans overlay’e değil Workflow’a gitmeli. Banner sadece anlamlı chipleri göstermeli. Tree gerçek scroll ile çalışmalı.

### Activity

Activity paneli teknik olarak güçlü: timeline + inspector düzeni mantıklı. Ama kullanım dili fazla kalabalık.

Sorunlar:

- Hint satırı tek harf ve sayı ezberiyle dolu.
- `activityTargetForEntry` drive/agent event’lerini Plans’a yönlendiriyor: `ui/tui/activity_render.go:107`, `ui/tui/activity_actions.go:135`.
- “firehose” yaklaşımı iyi ama filtreler daha sakin bir UI olmalı.

Öneri: Activity sadece olay akışı ve inspector olsun. Enter action menu açsın: Open related, Copy payload, Filter same source, Jump to provider, Jump to file. Drive/agent olayları Workflow’a gitmeli.

### Security

Security paneli işlevsel ama TUI içinde CLI metni ve sınırsız liste render’ı taşıyor.

- `ui/tui/security_render.go:137` CLI önerisi veriyor.
- `ui/tui/security_render.go:234` ve `252` filtreli listenin kalan tamamı render ediliyor; dış layout kırpıyor.
- Key surface yine `v/r/c/i/f` ağırlıklı.

Öneri: Security kendi row budget’ını bilmeli, sadece görünür bulguları çizmelidir. CTA metni CLI yerine TUI aksiyon menüsüne yönelmeli. Ignore/fix/view toggle action menu içinde görünmeli.

### Memory ve Conversations

Bu paneller ana tab olarak tutulmuş ama daily-flow ağırlığı tartışmalı. Providers ve Workflow kadar kritik değiller.

Öneri: Eğer 8 ana tab kalacaksa Memory/Conversations sade kalmalı. Alternatif olarak Memory + Conversations “Context” grubu altında panel switcher/overlay olabilir. Ana tablar şu şekilde daha net olabilir:

- Chat
- Files
- Patch
- Workflow
- Activity
- Providers
- Context
- Settings

## Görsel ve Layout Sorunları

### Unicode / Windows Terminal Riski

Mevcut PowerShell çıktısında box drawing, emoji ve ok karakterleri mojibake olarak görünüyor (`â”€`, `Â·`, `â†’`, vb.). Bu doğrudan TUI runtime bug’ı olmayabilir; komut çıktısı encoding’i de etkiliyor. Ama TUI Windows hedefliyorsa bu gerçek bir risk.

Öneri:

- `DFMC_ASCII_UI=1` veya config `tui.glyph_mode: ascii|unicode` ekle.
- Box drawing/emoji glyphleri tek palette/helper üzerinden üret.
- Width hesaplarında emoji yerine ASCII fallback kullan.

### Kart İçinde Kart ve Fazla Çerçeve

Birçok panel global frame içinde kendi card/pane düzenini kuruyor. Özellikle küçük terminallerde çerçeve, divider, banner, hint, footer toplamı gerçek içerikten çok yer kaplıyor.

Öneri: Ana frame tek olsun. İç panellerde çerçeve yerine sade başlık + divider yeterli. Metadata kartları yalnızca wide layout’ta gösterilmeli.

## Önerilen TUI Mimari Kararı

TUI için tek bir panel kontratı tanımlanmalı:

```go
type Panel interface {
    Title() string
    Render(width, height int) string
    UpdateKey(tea.KeyMsg) (Model, tea.Cmd)
    Actions() []panelAction
}
```

Her panel için ortak kurallar:

- `selectedID` veya `selectedPath` kullan; indeks sadece render window içindir.
- Renderer gerçek `height` alır, sabit 24 yoktur.
- Enter/Right ortak action menu açar.
- Esc bir seviye geri çıkar.
- Space panelin ana toggle’ıdır.
- Search standarttır ve her panelde aynı davranır.
- Tek harf doğrudan aksiyonlar ürün yüzeyinden kaldırılır.

## İlk Uygulama Sırası

1. Providers ve Files selection bug’larını düzelt.
2. Tools ve Workflow scroll/render bug’larını düzelt.
3. `height := 24` kullanan V2 panelleri gerçek height parametresine taşı.
4. Provider legacy menu/render yolunu kaldır, ortak `panelActionMenu` kullan.
5. Tek harf shortcutları action menu içine taşı; help ve hint satırlarını sadeleştir.
6. Chat default stats/tool strip kapalı gelsin ve tercih persist edilsin.
7. Providers ekranını “My Providers + Catalog + Custom + Routing” akışına böl.
8. Unicode glyph fallback ekle.

## Doğrulama Notu

`go test ./ui/tui ./ui/tui/theme -run '^$'` denendi. `ui/tui/theme` geçti, ama `ui/tui` setup aşamasında şu yüzden fail verdi:

```text
internal\engine\engine.go:37:2: no required module provides package github.com/dontfuckmycode/dfmc/internal/applog
```

Bu TUI panel tasarımından bağımsız bir repo/working tree durumu gibi görünüyor. TUI değişikliklerine başlamadan önce bu import/package durumu netleştirilmeli; aksi halde TUI compile doğrulaması kilitli kalır.
