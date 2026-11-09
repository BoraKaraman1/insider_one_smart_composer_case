# Kararlar

Bu dosyayı sonra kendim yazacağım. Şimdilik kısa notlar var. Tasarımın kendisi `docs/ARCHITECTURE.md` dosyasında.

## Şablon

Her karar için:

- **Bağlam:**
- **Seçenekler:**
- **Seçim:**
- **Neden:**
- **Sonuç:**

## Karar listesi

1. **Backend Go.** Tek süreç dağıtıcıyı, hız sınırlayıcıyı ve SSE'yi yönetebiliyor.
2. **Veri ve kuyruk Postgres'te (River).** Onay ve kuyruğa ekleme aynı transaction içinde.
3. **API anahtarı Claude oturumlarına girmez.** Deny kuralları, guard hook ve pre-commit hook var. OS sandbox zaman yüzünden seçilmedi.
4. **Segment kuralı düz bir JSON DSL.** SQL'i kod kurar, değerler parametre olarak gider. Rıza (consent) kuralda yok, derleyici ekler.
5. **İki şema, tek kaynak.** Tam şema yerelde çalışır. API şeması ondan türetilir, çünkü API `maxLength` gibi kuralları desteklemiyor.
6. **Her alan ve operatör grubu için ayrı şema dalı.** Böylece model `in` ile tek değer veremez.
7. **SMS en fazla 160 karakter.** Bu, değişkenler doldurulmuş hâliyle ölçülür. Kodlama ve segment sayısı sadece uyarıdır.
8. **Modelden link gelmez.** Link sadece `{{campaign_url}}` ile gelir.
9. **Retry, hata türüne göre yapılır.** 408, 429, 5xx, timeout, refused ve reset tekrar denenir. Diğer 4xx denenmez. Nedenler ayrı etiketlerle tutulur.
10. **Auth için tek bir admin token var.** Token yoksa API başlamaz. Compose'da bir dev token var.
11. **Mesaj durumları:** queued, sending, sent, failed, dead, skipped.
12. **Teslim raporları (delivery receipts) yok.** Spec'te istenmiyor.
13. **Hız sınırı kanal başına 100/sn, burst 1.** Retry'lar da token harcar.
14. **Test veritabanı:** her paket şablondan kendi kopyasını alır.
15. **Şehir değerleri ASCII slug.** Böylece İ/i sorunu çıkmaz.
16. **Zaman bir parametre.** Derleyici `asOf` alır. Seed, yüklendiği ana göre kurulur.
17. **DSL sınırı:** olay pencereleri birbirinden bağımsız. "Sepete ekledi, sonra almadı" ifade edilemiyor.

## Bağımlılıklar

| Modül | Sürüm | Lisans | Neden |
|---|---|---|---|
| `github.com/santhosh-tekuri/jsonschema/v6` | v6.0.3 | Apache-2.0 | JSON Schema doğrulama |
| `golang.org/x/text` | v0.14.0 | BSD-3-Clause | jsonschema ile geliyor |

## Ajan günlüğü (agent log)

| Tarih | Ajanın işi | Ne yanlıştı | Nasıl yakalandı |
|---|---|---|---|
| 2026-11-08 | Web araştırma özeti | Türkçe harflerin GSM-7 durumu doğrulanmamıştı | Twilio GSM-7 tablosuyla kontrol edildi |
| 2026-11-08 | Insider SMS dokümanları | İki sayfa birbirini tutmuyor | İki sayfa karşılaştırıldı |
| 2026-11-09 | River araştırması (alt ajan) | Sabit isimleri dokümanda ve pkg.go.dev'de farklı | Alt ajan işaretledi, kaynak koddan bakılacak |
| 2026-11-09 | `guard-secrets.sh` | `docker compose config` engellenmiyordu | Tablo testi |
| 2026-11-09 | Gün 1 planı | Derleyici özelliği zayıftı, testler çakışırdı, seed zamanı kayardı, DSL eksikti | Benim incelemem |
| 2026-11-09 | Adım 1 | Claude commit attı | Ben fark ettim, commit geri alındı |
| 2026-11-09 | `docs/segment-rules.md` taslağı | Doğru bir durumu sınır gibi yazmıştı | Claude'un kendi kontrolü |
