# Multica Sanal Ofis: Gerçekçi Oda Görünümü, Canlı Araç/Skill Akışı & Takım Skill Yönetimi

Bu plan, Multica'nın mevcut 2.5D izometrik sanal ofisini soyut podyumlardan çıkarıp **gerçek bir dijital çalışma ofisine (Living Digital Workspace)** dönüştürmeyi; ajanların "gerçek çalışanlar" gibi kendi tematik odalarında ve masalarında çalışmasını; üzerinde çalıştıkları task detaylarının, **anlık çağırdıkları araçların (CLI/Tools)** ve **kullandıkları skill'lerin** canlı olarak görünmesini; ayrıca proje/takım oluşturulurken **yalnızca role uygun filtrelenmiş skill'lerin atanmasını** kapsar.

---

## 🏛️ Mimari Vizyon & Temel Prensipler

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                          MULTICA VIRTUAL WORKSPACE HQ                         │
├───────────────────────────────────────┬───────────────────────────────────────┤
│ 📐 PLANLAMA & MİMARİ ODASI (CEO SUITE)│ 🔒 GÜVENLİK & TEST LABORATUVARI       │
│ • Whiteboard & Proje Yol Haritası     │ • Test Cihazları & Sunucu Kabinleri   │
│ • Product Manager & Solution Arch     │ • QA Engineer & Security Auditor      │
│ • Skill Set: [architecture, ultrathink│ • Skill Set: [testing, debugging]     │
├───────────────────────────────────────┼───────────────────────────────────────┤
│ 💻 MÜHENDİSLİK & YAZILIM STÜDYOSU     │ ⚖️ YÖNETİM & ONAY KAPISI (LOUNGE)     │
│ • Çift Monitörlü Masalar, Kahve Köşesi│ • Şık Koltuklar & Review Gate         │
│ • Mobile, Fullstack, Frontend Ajanları│ • Karar Verici & İnsan Onay Masası    │
│ • Skill Set: [react-doctor, backend]  │ • Bekleyen PR/Task İnceleme Kartı     │
└───────────────────────────────────────┴───────────────────────────────────────┘
                                ▲
                                │ Canlı Durum & Olay Akışı
┌───────────────────────────────┴───────────────────────────────────────────────┐
│ ⚡ ÇALIŞAN MASASI HUD & DİYALOG                                                │
│ • Çalışan Avatarları + Koltuk + Monitör (Gündüz / Çalışma Işığı)               │
│ • Durum: [Çalışıyor] / [Boşta - 0 Token Standby (Zzz)]                        │
│ • Aktif Task: #108 - Sanal Ofis Zoom & Pan İyileştirmesi (İlerleme %65)       │
│ • Canlı Araç (Tool Stream): ⚙️ Read: packages/views/office/spatial-mapper.ts │
│ • Aktif Skill: ⚡ react-native-best-practices                                 │
│ • Konuşma Balonu: 💬 "Testler %100 geçti, PR'ı onay kapısına ilettim."         │
└───────────────────────────────────────────────────────────────────────────────┘
                                ▲
                                │ Kullanıcı Direktifi / Soru
┌───────────────────────────────┴───────────────────────────────────────────────┐
│ 🎙️ OFİS KOMUTA MERKEZİ (ASK CEO & AGENTS DOCK)                                 │
│ [👑 CEO / Proje Yöneticisi] "Ofis genel durumu nedir, sprintte ne kaldı?"      │
│ [🎯 Seçili Ajan: Mobile Eng] "Navigation stack'teki memory leak çözüldü mü?"  │
└───────────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Faz: Takım Başlatma & İlgili Skill Filtreleme (Scoped Skills Architecture)

### Problem
Şu an sistem genelinde taranan onlarca skill (SEO, Mobile, Docker, Marketing, Backend vb.) hiçbir ayrım olmaksızın tüm ajanlara yüklenebilmekte veya bağlamı şişirip gereksiz token tüketebilmektedir.

### Çözüm & Mimari
* **Rol & Takım Odaklı Skill Politikası (Team Skill Profile):**
  Takım initialize edilirken her ajanın rolüne uygun bir `SkillPolicy` belirlenir:
  - **Mimarlık / PM Takımı:** `architecture`, `senior-architect`, `thinking-partner`, `documentation`
  - **Mühendislik / Frontend / Mobile Takımı:** `frontend-ui-engineering`, `design-system`, `shadcn-ui`, `react-doctor`, `react-native-best-practices`
  - **Mühendislik / Backend Takımı:** `backend`, `drizzle-orm`, `prisma-cli`, `debugging`, `optimization`
  - **QA & Güvenlik Takımı:** `testing`, `debugging`, `codebase-design`, `unlazy`
* **Backend Entegrasyonu:**
  - `server/internal/handler/agent_runtime_skills.go` ve daemon'daki `runtime_skill_policy.go` kullanılarak, ajan oluşturulurken yalnızca izinli skill alt kümesi (`allowed_skills`) enjekte edilir.
  - Alakasız skill'ler `disabledRuntimeSkills` listesine eklenerek prompt kirliliği engellenir ve **0-Token disiplini** korunur.

---

## 2. Faz: İzometrik Gerçekçi Oda Görünümü (Themed Office Rooms)

Mevcut düz renkli soyut podyumlar yerine, gerçek bir ofisin departman odaları inşa edilir:

### A. Oda Mimarisi (Room Layout & Decor)
1. **Mühendislik & Yazılım Stüdyosu (Engineering Room):**
   - Ahşap parke zemin dokusu, açık mavi neon hatlar.
   - Her geliştirici için çift monitörlü masa (biri dikey pivot monitör).
   - Ergonomik ofis koltuğu, masa üstü kahve kupası, mini sunucu kabini (LED ışıkları yanıp sönen rack).
2. **Mimari & Planlama Odası (Architecture & PM Suite):**
   - Krem/açık gri modern zemin, cam bölme duvarlar.
   - Büyük toplantı masası, izometrik çizim tahtası (Whiteboard üzerinde çizimler), mimari diyagram panosu.
3. **Test & Güvenlik Laboratuvarı (QA & Security Lab):**
   - Koyu metalik zemin, mor/eflatun siber güvenlik aydınlatması.
   - Çoklu test cihazı standları (telefon/tablet simülatörleri), log tarama konsolu.
4. **Yönetim & Onay Kapısı (Executive & Review Lounge):**
   - Yeşil/zümrüt lounge zemini, yönetici masası, rahat berjerler.
   - "Onay Bekleyenler" tepsisi ve bekleyen iş olduğunda parlayan karar kapısı lambası (Decision Light).

### B. Otomatik Oda Genişleme & Yerleşim Algoritması
- `ajan-ofisi`ndeki akıllı yerleşim mantığı uygulanır:
  - 1-3 ajan varsa tek sıra ferah masalar.
  - 4 ve üzeri ajan olduğunda oda otomatik olarak derinleşir ve masalar **2 sıra halinde çapraz (staggered)** konumlandırılır.

---

## 3. Faz: Çalışan Ajan Metaforu (Living Digital Employees)

Ajanlar sıradan bir arayüz kartı değil, ofiste yaşayan birer çalışan olarak resmedilir:

* **Çalışan Durumları & Görsel Karşılıkları:**
  1. **Çalışıyor (Working / Focus Mode):**
     - Çalışan sandalyede dik oturur, monitör aydınlıktır ve monitör ekranından hafif yeşil/mavi kod ışığı yayılır.
     - Klavye yazma / işlem parıltısı animasyonu.
  2. **Boşta (0 Token Standby / Zzz):**
     - Ajan sandalyede hafif geriye yaslanmış veya kahve molasında.
     - Monitörü uyku moduna geçmiştir (karanlık ekran).
     - Üzerinde **"0 Token Idle / Standby"** rozeti ve hafif nefes alma (breathing glow) nabzı.
  3. **Test Ediyor (Testing):**
     - QA masasında test cihazı ekranları yanıp söner.
  4. **Onay Bekliyor (Review Gate):**
     - Karar kapısı podyumuna doğru yönelmiş, sarı dikkat halkası ile vurgulanmış.

---

## 4. Faz: Canlı Araç/Skill Akışı & Görev HUD'ı (Live Tools & Skills Stream)

Ajan masasının üstündeki HUD'a üç kritik canlı veri katmanı eklenir:

```html
┌─────────────────────────────────────────────────────────────┐
│ 🧑‍💻 Senior Mobile Engineer             [🟢 Working]         │
│ 📋 #142 - Trackpad Pinch-to-Zoom Desteği (İlerleme: %75)    │
├─────────────────────────────────────────────────────────────┤
│ ⚙️ ARAÇ (TOOL): Read: packages/views/office/spatial.ts     │
│ ⚡ SKILL     : react-native-best-practices                 │
│ 📦 İŞ YÜKÜ   : 1 Aktif • 3 Sırada (Queued)                  │
└─────────────────────────────────────────────────────────────┘
```

1. **Canlı Araç Göstergesi (Live Tool Stream):**
   - Ajanın arka planda o saniye çalıştırdığı tool'un adı ve hedefi kısa ve güvenli şekilde gösterilir:
     - `⚙️ Read: filepath=...`
     - `⚙️ Bash: command=pnpm test`
     - `⚙️ Edit: filepath=...`
     - `⚙️ Grep: pattern=...`
2. **Kullanılan Skill Rozeti (Active Skill Badge):**
   - Ajanın o iş için devreye soktuğu uzmanlık yeteneği (örn: `⚡ react-doctor`, `⚡ testing`, `⚡ backend`).
3. **Kuyruk Dağılımı & Tooltip:**
   - Masanın üzerinde `1 active • 3 queued` göstergesi ve tıklandığında/hover'da sıradaki 3 görevin başlıkları.

---

## 5. Faz: Detaylı Çalışan & Masa İnceleme Paneli (Employee Desk Drawer)

Kullanıcı bir masaya veya ajana tıkladığında sağ taraftan açılan genişletilmiş tezgâh paneli:
* **Çalışan Kimliği:** Ad, Rol, Takım, Departman Odası, Avatar.
* **Yetkili & İzinli Skill'ler:** Takım başlatılırken ajana tahsis edilmiş özel skill kütüphanesi (diğer alakasız skill'lerin filtrelendiği net liste).
* **Canlı Monitör / Konsol Akışı:** Ajanın son çalıştırdığı 10 araç çağrısı ve log özetleri (stream-json benzeri temiz log görünümü).
* **Görev Kuyruğu Yönetimi:** Ajanın sıradaki işlerini yeniden sıralama veya yeni iş ekleme.
* **Onay Kartı (Review Gate Card):** Tamamlanan iş onay bekliyorsa tek tıkla "Onayla / Revize İste".

---

## 6. Faz: İnteraktif İletişim: CEO Komuta Çubuğu & Ajanla Doğrudan Soru-Cevap (Ask CEO & Direct Agent Chat)

Kullanıcı yalnızca izleyici değil, ofisin yöneticisi / ürün sahibi (Product Owner) olarak her an diyalog kurabilmelidir:

### A. Ofis Alt Komuta Çubuğu (Commander Dock - "Ask CEO / PM")
* Ekranın alt ortasında modern cam dock:
  - Hedef Seçici: `[👑 Proje Yöneticisi / CEO]` veya açılır menüden `[🎯 Belirli bir ajan]`.
  - Hızlı Soru / İstek Kutusu: *"Ofis genel durumu nedir?", "Kritik issue'lar hangileri?", "Mobil tarafta ne kaldı?"*.
  - Soru gönderildiğinde:
    1. CEO orkestratör ajanı genel proje durumunu analiz eder ve anında yanıt döner.
    2. Eğer soru bir iş dağıtımı içeriyorsa ("Blog taslağı hazırla", "Login ekranındaki hatayı incele"), CEO ilgili ajana görevi atar ve ofiste **iş emri uçuş animasyonu (packet flight)** tetiklenir!

### B. Ajan Masası Üzerinde Doğrudan Konuşma (Direct Agent Q&A)
* Kullanıcı belirli bir ajanın masasına tıkladığında veya çift tıkladığında:
  - Ajanın sağdaki tezgâhında (Inspector Drawer) **"Ajanla Konuş (Chat with Agent)"** sekmesi açılır.
  - Kullanıcı ajana spesifik sorular yöneltebilir:
    - *"Bu component'i neden Base UI ile yeniden yazdın?"*
    - *"Bu task'ta hangi test senaryolarını koştun?"*
    - *"Bana bu migration için rollback planını açıklar mısın?"*
  - Ajan, üzerinde çalıştığı task'ın kod bağlamı ve kendi yetkili skill'leri çerçevesinde cevap verir.

### C. Ofis İçi Konuşma Balonları (In-Office Speech Bubbles)
* Ajanlar ve CEO cevap verdiğinde ya da kritik bir aşamaya ulaştığında (örn: PR onaya düştüğünde veya test bittiğinde):
  - Masanın üzerinde izometrik perspektife uygun **mini konuşma balonu (Speech Bubble)** belirir (10 saniye süreyle görünür ve sonra kaybolur).
  - Örneğin: `💬 "Tüm unit testler yeşil! PR'ı onay kapısına ilettim."` veya `💬 "Token limitini korumak için 0-Token Standby moduna geçiyorum."`.
  - Bu sayede ofis sadece statik kartlardan ibaret kalmaz; gerçekten çalışan insanların ses verdiği, yaşayan bir stüdyo havasına kavuşur.

---

## 7. Faz: Kademeli Detay (Level of Detail - LOD) ile Zoom & Pan

Trackpad ve mouse zoom'u ile ofis üç farklı ölçekte deneyimlenir:
1. **Makro Bakış (0.4x - 0.7x):** Tüm ofis tek ekranda. Odaların yerleşimi, oda isimleri, departmanlar ve ana iş akış lazer çizgileri.
2. **Standart Bakış (0.8x - 1.2x):** Masalar, çalışan avatarları, oda içi mobilyalar, aktif görev başlığı ve `1 active • 3 queued` HUD'ı.
3. **Mikro / Masaüstü Bakış (1.3x - 2.2x):** Monitörlerde kayan yeşil araç logları, masa üstü kahve kupası, detaylı skill etiketleri, çalışan çalışma parıltıları ve konuşma balonları.

---

## 📋 Uygulama Adımları & Dosya Planı

| Adım | Yapılacak İşlem | İlgili Dosyalar |
| :--- | :--- | :--- |
| **Adım 1** | Takım & Ajan Skill profillerini ve filtreleme şemasını tanımlama | `packages/core/skills/team-skill-presets.ts`, `server/internal/handler/agent_runtime_skills.go` |
| **Adım 2** | `types.ts` içinde Oda (Room), Masaüstü Mobilya (DeskFurniture), Canlı Araç (ActiveTool), Konuşma Balonu (SpeechBubble) ve Soru-Cevap veri tiplerini genişletme | `packages/views/office/types.ts` |
| **Adım 3** | Gerçekçi izometrik oda zeminleri, cam bölmeler, oda tabelaları ve tematik mobilya çizicileri oluşturma | `packages/views/office/spatial-mapper.ts`, `packages/views/office/components/room-renderer.tsx` |
| **Adım 4** | Çalışan masası, koltuğu, aydınlatmalı monitörü, Standby (0 Token Zzz) ve Focus çalışma animasyonlarını ekleme | `packages/views/office/components/employee-desk-node.tsx` |
| **Adım 5** | Masalar üzerine canlı Araç (Tool Stream), Skill rozetleri ve dinamik Konuşma Balonlarını entegre etme | `packages/views/office/components/agent-desk-hud.tsx` |
| **Adım 6** | Ofis alt komuta barı (Commander Dock - "Ask CEO / Agents") bileşenini ekleme ve API orkestrasyonunu bağlama | `packages/views/office/components/office-commander-dock.tsx` |
| **Adım 7** | Sağ inceleme panelinde (Inspector Drawer) Çalışan Masa Tezgâhı, İzinli Skill Listesi ve Ajanla Doğrudan Sohbet sekmesini oluşturma | `packages/views/office/components/office-inspector-drawer.tsx` |
| **Adım 8** | Vitest birim testleri ve TypeScript tip doğrulaması | `packages/views/office/spatial-mapper.test.ts`, `pnpm typecheck` |

---

## ❓ Kullanıcı Kararı ve Onayı

Bu plan, `ajan-ofisi`'ndeki canlı tool stream, 0-token idle ve dinamik oda mantığı ile Multica'nın kurumsal görev yönetim gücünü birleştirerek tam bir **"Yaşayan Dijital Ofis"** ortaya koyacaktır.

Bu mimari planı onaylıyorsanız hemen 1. Adımdan (Tip genişletmeleri ve Takım Skill filtreleme kuralları) başlayarak geliştirmeyi icra edebiliriz.
