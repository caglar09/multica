# Multica Autonomous Project OS: tersine mühendislik denetimi

**İncelenen checkout:** `feat/autonomous-with-deployments`, HEAD `5b97f23397c786b130a0f7bb96170ce421182a71`

**Yöntem:** Mevcut kaynak, mimari belgeler ve test tanımları incelendi. Test, build, lint, veritabanı veya canlı runtime çalıştırılmadı. Bulgular koddan doğrulanabilen durum ile koşullu etki/çalışma zamanı bilinmeyenlerini ayırır.

## Yönetici özeti

Multica'da bunun için gerçek bir temel var: proje fikri ve planı, rol bazlı takım taslağı, DAG biçiminde iş planı, ajan görevleri, proje hafızası, kalite kapıları, bütçe, insan onayları, duraklatma ve kontrol paneli kodda mevcut. Takım onayı admin ile sınırlandırılmış; çalışma alanı dışındaki ajan ve beceriler reddediliyor; varsayılan politikada üretim dağıtımı, veritabanı geçişi ve kritik risk için onay isteniyor.

Ancak mevcut kaynak, “fikri ver → orijinal niyeti koru → kanıtla test et → tam olarak test edilen kodu dağıt → sonucu izle ve gerekirse geri al” döngüsünü uçtan uca garanti etmiyor. En ciddi dört açık şunlar:

1. Deterministik test kapısı, güvenilir test çalıştırıcısı yokken işi yapan ajanın kendi test raporuyla `passed` olabiliyor.
2. Onaylanmış fikir ile uygulanan plan arasında izlenebilirlik eksik: bazı şartlar bağlamdan düşebiliyor; plan, kullanıcıya gösterilmiş belirli bir sürüme bağlı olmadan aktifleşebiliyor.
3. Dağıtıma gönderilen `release_ref`, kod commit'i değil; ilk planlamada kullanıcı onay zamanından türetiliyor. Bu nedenle test edilen kaynak ile dağıtılan kaynak eşleşmesi kanıtlanmıyor.
4. Plan onayları belirli plan revizyonu ve içerik özetiyle bağlanmıyor; aynı `node_key` yeniden kullanılırsa eski bir onay sonraki eylemi de açabilir.

Bu nedenle sistemi **iyi bir otonom iş akışı iskeleti; fakat henüz kanıta dayalı, kaynağı sabitlenmiş ve geri alınabilir otonom ürün teslim sistemi değil** olarak değerlendiriyorum. Bu, canlı ortamda mutlaka yanlış dağıtım yapıldığı anlamına gelmez; kaynak kodun bunu engellediğini ve güvenli sonucu kanıtlayabildiğini gösteremediği anlamına gelir.

## Mevcut akışın kaynakta görünen hali

1. **Takım taslağı ve insan kontrolü:** Proje yöneticisi takım/rol taslağı oluşturur. Admin rol atamalarını onaylar; handler çalışma alanı, çevrimiçi runtime, seçili beceri ve rol kapsamını doğrular. Başarılı onaydan sonra planlama başlatma işareti yazılır. (`server/internal/handler/project_autonomous.go:1980-2177`; takım planlayıcı sözleşmesi `server/internal/teamprovision/README.md:60-93,145-171`)
2. **Planlama:** Reconciler planlama devam işaretini görür, proje ve takım verisini toplar, planner görevi açar, planı doğrular ve doğrudan `active` durumunda kaydeder. Aynı reconciler daha sonra scheduling'e geçebilir. Oluşturulan spesifikasyon, DAG, kabul kriterleri ve risk özeti için ayrı bir “kullanıcının gördüğü plan sürümünü onayla” durumu yoktur. (`server/internal/workflowruntime/project_orchestrator.go:176-257,426-501`; `server/internal/projectorchestration/store_tx.go:43-106`; `server/internal/workflowruntime/runtime.go:356-386`)
3. **Görev yürütme:** Plan düğümleri issue'lara yansıtılır ve hazır düğümler takım üyelerine atanır. Görev girdisi; proje hedefi/spesifikasyonu, politika, seçili beyin kayıtları, önceki artefaktlar ve repo bağlamından derlenir. Çalışma çıktısı yapılandırılmış handoff/review artefaktları ve kalite kanıtlarına çevrilir. (`server/internal/workflowruntime/project_orchestrator.go:1177-1266,1497-1583`; `server/internal/projectorchestration/context_compiler.go:103-165`)
4. **Kalite ve engeller:** Politika kalite kapıları tanımlar; başarısız/engelli düğümler bloke edilip escalation oluşturabilir. Fakat `unit_test` kapısının gerçekte bağımsız test çalıştırdığı her konfigürasyonda garanti değil. (`server/internal/projectorchestration/quality.go:14-78`; `server/internal/workflowruntime/phase2_quality.go:248-358`)
5. **Dağıtım:** Dağıtım düğümü ayrı webhook adaptörünü senkron çağırır ve sonucu otonom projeye özel tabloya yazar; `environment` sabit olarak `production`, `ReleaseRef` ise planın `source_revision` değeridir. Generic Deployment Service'in operasyon kimliği ve durum uzlaştırması bu yola bağlanmış görünmüyor. (`server/internal/workflowruntime/project_orchestrator.go:1977-2077`; `server/internal/workflowruntime/runtime.go:57-64,137-140,185-188`)
6. **İşletim:** Admin pause/resume, restart/replan ve escalation çözümü yapabilir. Pause yeni dispatch'i durdurur; başlatılmış ajan/provider işlerinin hemen iptal edildiği anlamına gelmez. Kontrol paneli durum ve geçmişi sunar; bazı timeline sorgu hataları sonucu sessizce kısmi döndürebilir. (`server/internal/handler/project_autonomous.go:1225-1334,1861-1943`; `server/internal/workflowruntime/project_orchestrator.go:1177-1193`)

## Kritik bulgular

### P1 — `unit_test` kapısı ajan iddiasıyla yeşile dönebiliyor

**Kanıt:** Varsayılan politika `development`; bu seviyede implementation düğümleri `unit_test` kapısı alıyor. Kalite runner webhook'u opsiyonel; URL yoksa runner `nil`. Bu durumda `applyTaskQualityEvidence`, görevi üreten ajanın artefaktındaki `tests[]` kayıtlarından `passed` yazabiliyor. Yardımcı fonksiyon, “non-blocking” işaretli başarısızlığı ve “değişiklik dışında” denilen başarısız/atlanmış kontrolü de başarılı sayabiliyor. İlgili kod: `server/internal/projectorchestration/validator.go:35-51`; `server/internal/projectorchestration/quality.go:14-78`; `server/internal/projectorchestration/webhook_adapters.go:44-49`; `server/internal/workflowruntime/phase2_quality.go:248-358`. `phase2_quality_test.go:5-52` bu kabul davranışını test tanımı olarak sabitliyor.

**Etki:** Kontrol paneli deterministik test kapısını gerçek test çalıştırmadan geçmiş gösterebilir. Webhook'un üretimde daima tanımlı olduğu bu incelemede doğrulanmadı.

**Düzeltme ve kabul ölçütü:** Dağıtım/kapalı döngü kipinde testleri güvenilir bir runner çalıştırmalı. Runner yoksa sonuç `blocked` veya `inconclusive` olmalı; ajan beyanı `passed` yapmamalı. Sonuçlar tam repo kimliği, commit/tree SHA, komut ve çıkış koduna bağlanmalı. Runner yokluğu, sahte `passed`, skipped test ve runner hatası yeşil kapı üretememeli.

### P1 — Onaylanmış kullanıcı niyeti plan ve görev doğrulamasına taşınmıyor

Burada birkaç ayrı veri kaybı var:

- **Kapsam değişikliği spesifikasyonu güncellemiyor:** Project Leader teklif tipi yalnızca plan operasyonlarını taşıyor; `base_specification_revision_id` veya yeni spesifikasyon yok. Onay uygulaması change proposal'ı `nil` spec revision ile kaydediyor. Tasarım belgesi ise kapsam değişikliği için spesifikasyon alanları bekliyor. (`server/internal/projectorchestration/project_leader.go:32-49,243-284`; `server/internal/projectorchestration/change_store.go:480-492,577-592`; `specs/autonomous-project-leader-chat-plan.md:254-299`)
- **Typed spesifikasyon sadeleştirilirken alanlar düşüyor:** Requirement ID, kaynak/gerekçe, non-goal ve acceptance expectation içeren model, `LegacySpecification()` ile düz string listesine indirgeniyor. Workflow context de aktif typed revision yerine plan goal/specification kullanıyor. (`server/internal/projectorchestration/change_model.go:48-107`; `server/internal/projectorchestration/change_store.go:480-492`; `server/internal/projectorchestration/context_compiler.go:112-120,216-250`)
- **Uzun fikir metni sessizce kesilebiliyor:** Goal + spec tek bir authoritative context öğesi. Spec bölümü 450 token bütçeli; `NewBoundedContext` öğeyi kesip `Omitted` sayısını artırabiliyor. Context derleyici bu sayıya bakıp çalışmayı durdurmuyor. (`server/internal/projectorchestration/context.go:5-18,95-120`; `server/internal/projectorchestration/context_compiler.go:112-120,159-165`)
- **Plan doğrulaması şart kapsamını ölçmüyor:** Plan düğümünde kabul kriteri olabilir ama requirement ID yok; validator her kabul edilmiş gereksinimin en az bir görev ve doğrulamaya bağlandığını kanıtlayamıyor. (`server/internal/projectorchestration/model.go:95-105`; `server/internal/projectorchestration/validator.go:53-172`)

**Etki:** Sistem biçimsel olarak geçerli bir planı, ana fikrin zorunlu maddelerini atlamış veya yasakladığı bir kapsamı içermiş olsa bile aktif edebilir. Uzun fikirlerde gerekli bir şart ajan prompt'una hiç ulaşmayabilir. Gerçek projelerde ne sıklıkta olduğu ölçülmedi.

**Düzeltme ve kabul ölçütü:** Tek canonical, sürümlü `ProjectSpecification` kullanın. Her requirement'a sabit ID, kaynak, öncelik, rationale, non-goal ve ölçülebilir kabul kanıtı verin. Her plan düğümü hangi requirement'ı karşılayıp hangi test/kanıtla doğrulayacağını taşısın. Zorunlu bağlam bütçeye sığmıyorsa yürütme engellensin. Son şartı kritik bir non-goal olan uzun spesifikasyon ya eksiksiz ulaşmalı ya da açık bir engel üretmeli; hiçbir şart sessizce düşmemeli.

### P1 — Üretilen plan kullanıcıya gösterilmiş/onaylanmış sürüme bağlanmadan çalışabiliyor

Planlama başlatma endpoint'i devam işaretini yazar. Planner planı doğrudan `active` kaydeder ve reconciler scheduling adımına ilerler. `awaiting_plan_approval` benzeri bir durum veya plan hash'iyle verilmiş onay kaynağı yok. (`server/internal/handler/project_autonomous.go:2136-2177`; `server/internal/workflowruntime/project_orchestrator.go:176-257,475-493`; `server/internal/projectorchestration/store_tx.go:43-106`)

İlk konuşmada “kapsam onaylandı” ifadesi kullanıcıya sorulmuş olabilir; ancak kaynak, kullanıcının tam olarak hangi DAG, kabul kriteri, bütçe ve risk özetini gördüğünü kaydetmiyor. O nedenle sorun “mutlaka insan plan onayı zorunlu olmalı” değil; **otonom kipte bile hangi planın onaylanmış niyete göre yürütüldüğünün kanıtı yok**.

**Düzeltme ve kabul ölçütü:** Üretilen planı sürümlü taslak olarak saklayın. Onay politikasına göre (otomatik/düşük risk veya admin/yüksek risk) exact spec+plan+policy hash'i kabul edilmeden scheduler hiçbir düğümü başlatmamalı. Replan, kapsam veya risk değişimi önceki onayı geçersiz kılmalı. Kullanıcı onayı gerektirmeyen modda bile otomatik plan doğrulama sonucu ve hangi sürümün yürütüldüğü kayda geçmeli.

### P1 — Eski deploy onayı aynı `node_key` için sonraki plan revizyonunu açabilir

Üretim deploy onayı gerçekten kontrol ediliyor; bu olumlu bir koruma. Ancak kontrol, onay kaydını plan revizyonuna, node içeriğine, politika snapshot'ına veya deploy digest'ine bağlamıyor. Sorgu workspace/project, `context.node_key`, `approval_required`, `resolved`, `decision=approved` ile eşleşiyor. Escalation context'te plan revizyonu ve action digest yok; resolver da bu bağı kurmuyor. (`server/internal/workflowruntime/project_orchestrator.go:2387-2438,2462-2473`; `server/internal/handler/project_autonomous.go:2344-2400`)

**Etki:** Daha sonra aynı `node_key` kullanılırsa önceki bir deploy için verilen onay yeni planın farklı release target'ını da açabilir. Kaynakta düğüm anahtarının sonraki deploy planlarında mutlaka aynı kaldığını doğrulamadım; bulgu onay sorgusundaki bağ eksikliği ve yeniden kullanım koşuludur.

**Düzeltme ve kabul ölçütü:** Onayı `plan_revision + node_id + policy_digest + environment + immutable_release_digest` tuple'ına bağlayın ve dispatch öncesi aynı tuple'ı yeniden doğrulayın. Revision N onayı, aynı key'li ama içeriği değişmiş N+1 düğümünü açmamalı.

### P1 — Deployment `release_ref` agentların ürettiği kaynak revision'ı değil

İlk planlamada `sourceRevision`, takım onay zamanından (`confirmed_at`) biçimleniyor ve plan metadata'sına yazılıyor. Deployment düğümü bu değeri `ReleaseRef` olarak webhook'a aktarıyor ve kayda koyuyor. Kaynak akışında ajan çıktısının repo kimliği + commit SHA/tree digest'i ile ilişkilendirildiği görülmüyor. (`server/internal/workflowruntime/project_orchestrator.go:222-225,426-430,490-493,1995-2002,2021-2028,2060-2067`; `server/internal/projectorchestration/store_tx.go:43-106`)

**Etki:** Provider'ın gerçekte hangi kaynağı dağıttığı webhook implementasyonuna bağlı ve burada bilinmiyor. Test edilmiş kodla dağıtılan kodun aynı olduğu kanıtlanamıyor.

**Düzeltme ve kabul ölçütü:** Review ve kalite kapılarından sonra repository kimliği, tam commit SHA/tree digest'i, build artifact digest'i ve provenance içeren değişmez bir release manifest üretin. Deployment yalnızca bu manifest'i dağıtmalı ve sonuç aynı digest'i atteste etmeli. Branch ucu değiştiğinde dağıtılacak kaynak değişmemeli.

### P1 — Autonomous deployment ortak Deployment Service yerine ayrı webhook/table kullanıyor

Runtime `MULTICA_AUTONOMOUS_DEPLOYMENT_WEBHOOK_URL` ile özel webhook adaptörü kuruyor. Deploy düğümü doğrudan bu adaptörü çağırıp `autonomous_project_deployment` tablosunu yönetiyor. Generic `server/internal/deployment` servisine operation ID, restart sonrası durum uzlaştırma veya ortak operation state üzerinden bağlandığı görülmüyor. Mimari plan, OS düğümünün generic service'e gitmesini, operation ID ile takip edilmesini ve otonom tablonun korelasyon kaydı olmasını bekliyor. (`server/internal/workflowruntime/runtime.go:57-64,137-140,185-188`; `server/internal/workflowruntime/project_orchestrator.go:1508-1518,1977-2077`; `specs/deployment-providers-plan.md:900-929,1347-1357`)

Webhook kontratı senkron biçimde yalnızca `succeeded` veya `failed` kabul ediyor; uzun süren operasyonun devamlılık durumu yok. (`server/internal/projectorchestration/webhook_adapters.go:141-166`)

**Etki:** İki dağıtım lifecycle'ı oluşuyor; ortak sağlayıcı durumları, tekrar başlatma kurtarması ve Deployments UI görünürlüğü garanti değil. Bu branch için belgede entegrasyonun sonraki ayrı iş olduğu yazıyor; o nedenle bunu runtime arızası değil, mevcut branch kapsamındaki tamamlanmamış köprü olarak sınıflandırıyorum.

**Düzeltme ve kabul ölçütü:** Generic service'e dispatch edin, `operation_id` ve onay/release digest'ini kalıcı korelasyon olarak saklayın; restart sonrası aynı operasyonu uzlaştırın. UI'daki dağıtım ve OS düğümü aynı operation state'inden türemeli.

### P1 — Planner hatası her 10 saniyede yeni LLM işi açabilir; bitmeyen görev reconciler'ı kilitleyebilir

Planlama adayı; continuation tamamlanmamış ve aktif/tamamlanmış plan yoksa her 10 saniyelik reconcile'da tekrar seçilebiliyor. Hata halinde yalnızca `last_error` yazılıyor; attempt sayısı, next-retry, lease veya terminal hata durumu yok. Yeni denemede yeni session/task oluşturuluyor. Candidate claim yok; birden fazla server instance aynı işi açabilir. (`server/internal/workflowruntime/runtime.go:356-386`; `server/internal/workflowruntime/project_orchestrator.go:181-247`; `server/internal/workflowruntime/project_planner_executor.go:35-75`)

Planlama task'ı için deadline yok ve executor terminal duruma gelene dek polling yapıyor. Bu çağrı reconciler içinde senkron çalıştığından, hiç tamamlanmayan planner o process'in scheduling/recovery döngüsünü durdurabilir. (`server/internal/workflowruntime/project_orchestrator.go:222-225`; `server/internal/workflowruntime/project_planner_executor.go:148-186`; `server/internal/workflowruntime/runtime.go:356-386`)

**Düzeltme ve kabul ölçütü:** Proje+continuation revision'ı durable lease ile claim edin; task ID ve attempt state'i kaydedin. Idempotent retry, jitter'lı backoff, max deneme ve operatör retry durumu ekleyin. Planner deadline aşınca tam task'ı iptal edip hata kaydetsin ve reconciler devam etsin. İki instance yarışı bir task üretmeli; hiç bitmeyen planner diğer projenin döngüsünü durdurmamalı.

### P1 — Onay bekleyen issue değişiklikleri sürüm kontrolsüz uygulanabiliyor

Proje Leader tasarım belgesi issue revision'ını doğrulamayı ve güncelleme çakışmasında rollback'i bekliyor. Ancak change operation expected issue revision taşımıyor; apply yolu başlık/açıklama/öncelik/acceptance alanlarını id + workspace + statü ile yazıyor. Sıfır satır update durumunda, eksik issue veya sorgu hatası bazı koşullarda hata olarak döndürülmüyor. (`specs/autonomous-project-leader-chat-plan.md:341-353,418-464`; `server/internal/projectorchestration/change_model.go:125-133`; `server/internal/projectorchestration/change_store.go:604-622`)

**Etki:** Onay beklerken yapılan kullanıcı düzenlemesi overwrite olabilir; eksik board issue'su olmasına rağmen plan değişikliği uygulanmış sayılabilir.

**Düzeltme ve kabul ölçütü:** Tek transaction içinde expected revision + status eşleşmesine göre yazın, revizyonu artırın ve sıfır satır etkisini çakışma/eksik kayıt hatası yapın. Kullanıcı onay beklerken issue description değişirse tüm proposal apply rollback olmalı.

## Orta öncelikli açıklar

### P2 — Pause yalnızca yeni dispatch'i durduruyor

Scheduler pause bayrağını dispatch öncesi kontrol ediyor; zaten başlamış agent/provider işi için endpoint iptal veya containment yapmıyor. UI metni bunu “dispatch paused” diye doğru adlandırıyor. (`server/internal/workflowruntime/project_orchestrator.go:1177-1193,1977-2028`; `server/internal/handler/project_autonomous.go:1861-1924`; `packages/views/projects/components/autonomous-control-center.tsx:1688-1691`)

**Öneri:** “Yeni iş başlatma” ile “devam eden işi durdurma/iptal” ayrı yetenekler olsun. Her runtime/provider için iptal garantisi ayrı belirtilsin; pause yarışlarının sonucu activity kaydında görülsün.

### P2 — Rollback adaptörü var, fakat orchestrator çağırmıyor

Autonomous deployment arayüzü ve webhook adaptörü `Rollback` tanımlıyor; workflowruntime/projectorchestration içinde caller bulunmadı. Deploy sonrası başarısız health/observation için OS tarafından yönetilen geri alma ve doğrulama yolu kaynakta görünmüyor. (`server/internal/projectorchestration/adapters.go:68-96`; `server/internal/projectorchestration/webhook_adapters.go:169-192`; `server/internal/workflowruntime/project_orchestrator.go:1977-2077`)

**Öneri:** Rollback/compensation'ı kalıcı operation olarak modelleyin; tetikleme, idempotency, gerekiyorsa onay ve health doğrulaması belirleyin. Provider'ın rollback garantisi bilinmiyor.

### P2 — Kontrol paneli activity listesi sessizce kısmi dönebilir

Snapshot endpoint'i bazı activity sorguları başarısız olduğunda uyarı/error vermeden bu kayıtları atlıyor; yine de Activity yanıtını dönüyor. (`server/internal/handler/project_autonomous.go:1225-1334`)

**Öneri:** Snapshot'ı fail et veya bölüm bazında freshness/error metadata'sı ve UI uyarısı döndür. DB hatasında geçmiş sessizce tam görünmemeli.

### P2 — Brain araması düğüm başlığını kullanıyor, iş tanımını değil

Compiler node description'ı yüklüyor ancak memory search terimi olarak yalnızca task title (yoksa issue title) seçiyor. Genel başlıklı ama özel şartlı işte alakasız brain kayıtları seçilebilir; bu etki olasılık çıkarımıdır, gerçek retrieval kalitesi ölçülmedi. (`server/internal/projectorchestration/context_compiler.go:122-126,216-250`)

**Öneri:** Başlık + description + acceptance criteria/requirement ID ile arayın; hangi hafıza maddelerinin seçildiğini ve nedenini task evidence'ına ekleyin.

### P2 — Planner kalite kapısının nerede çalıştığı bu incelemede kanıtlanmadı

Üretim runtime config'i, webhook erişimi, replika sayısı, workspace/global spend cap ve dış provider'ın retry/rollback davranışı kontrol edilmedi. Kodda proje düzeyi token/runtime/cost muhasebesi, dört paralel düğüm ve 100 deneme sınırı var; fakat buradan workspace/account global bütçesi olduğunu veya üretimde webhook'un yapılandırıldığını çıkaramayız. (`server/internal/projectorchestration/validator.go:31-53`; `server/internal/projectorchestration/model.go:64-85`; `server/internal/projectorchestration/usage.go:82-175`)

**Öneri:** Per-project limitlere ek olarak workspace/global günlük ve aylık hard cap, uyarı eşiği, maliyet attribution ve concurrency başına bütçe rezervasyonu ekleyin; gerçek deployment config'ini ayrıca denetleyin.

## Halihazırda iyi kurulmuş kontroller

- Project Leader teklifleri typed allowlist kullanıyor; bilinmeyen patch alanlarını reddediyor, stale plan revision'ını kontrol ediyor ve materialized/executing düğüm topolojisini koruyor. (`server/internal/projectorchestration/project_leader.go:56-117,133-160,189-223,239-283`)
- Project brain kayıtlarında authority/revision/freshness var; retrieval superseded, current olmayan ve süresi geçmiş girdileri filtreliyor. Workflow action context'i retry için kaydediliyor. (`server/internal/projectorchestration/brain.go:43-63,278-325`; `server/internal/projectorchestration/context_compiler.go:58-79`)
- Admin/owner sınırı, workspace kapsamı, online runtime kontrolü ve rol/skill doğrulaması takım onay akışında mevcut. (`server/internal/handler/project_autonomous.go:1980-2128`)
- Varsayılan plan policy'si veritabanı migration, production deploy, major dependency ve critical risk için approval ister; max paralellik 4, max toplam deneme 100'dür. (`server/internal/projectorchestration/validator.go:31-53`)
- Per-project usage/budget verileri tutuluyor; pause, escalation, quality gate, diagnostics, plan ve proje brain ayarları kontrol yüzeyinde temsil ediliyor. (`server/internal/projectorchestration/usage.go:82-175`; `server/internal/handler/project_autonomous.go:317-365,657-850,1325-1334`)

## Profesyonelleştirme sırası

### 1. Fikir ve plan için tek, izlenebilir doğruluk kaynağı kur

Sürümlü canonical spec, requirement ID'leri, non-goal'lar, kabul kanıtları ve plan bağlantısı aynı onaylanabilir snapshot'ta bulunsun. Change proposal bu snapshot'ı değiştirsin; plan DAG'ındaki her düğüm requirement ID'lerine bağlansın. Prompt context'i mandatory şartları kesmesin. Her koşul için: **kaynak fikir → canonical requirement → plan node → test/kanıt → release manifest** zinciri sorgulanabilir olsun.

### 2. Kalite sonucunu ajandan ayır

Unit/integration/e2e/security/migration/acceptance gate sonuçlarını güvenilir runner üretsin. Gate state yalnızca `passed/failed` değil, `blocked`, `unavailable`, `inconclusive`, `waived` ayrımı da taşısın. Waiver admin, gerekçe ve kapsam içersin. Her test sonucu test komutu, runner image/version, repo SHA ve artifact digest'e bağlı olsun. Kod değişikliği sonrası eski kanıt geçersiz olsun.

### 3. Dağıtımın tam kaynağını ve operasyonunu sabitle

Test/review'den geçen kod için immutable commit/tree ve build artifact oluştur. Deployment generic Deployment Service üzerinden operation ID alsın; aynı operation restart'tan sonra uzlaştırılsın. Approval bu release digest ve environment için geçerli olsun. Staging/production ayrımı, smoke/health check, rollout/canary, rollback/compensation ve rollback sonrası doğrulama tanımlansın.

### 4. Reconciler'ı hata ve çoklu instance'a dayanıklı yap

Planning ve task dispatch için durable claim/lease, idempotency key, bounded retry/backoff, max age, deadline ve per-project worker izolasyonu kur. Her run'ın `run_id`, `plan_revision`, `source_sha`, task ID'si ve terminal nedeni olsun. Sıkışan iş dashboard'da actionable olarak görünsün; sonsuz retry veya sessiz kaybolma olmasın.

### 5. Otonomi zarfı ve gerçek durdurma davranışını açık tanımla

Her proje için hangi eylemlerin otomatik olduğu, hangi risklerin onay istediği, hangi maliyet üstünde duracağı, hangi repo/branch/environment'a yazabileceği başlangıçta kaydedilsin. **Kullanıcının belirlediği sınır:** kod ve testler otomatik; üretime geçiş insan onaylı. Üretim onayı exact test edilmiş commit/build digest'i ve environment'a bağlanmalı; onay değişen kaynakla tekrar kullanılamamalı. Kullanıcı planlama veya staging sınırını ayrıca değiştirmediği için, planın otonom üretilmesi ve doğrulanması öneriliyor; kaynakta varsayılan olan migration/critical-risk onayları korunmalı. Pause, cancel ve rollback ayrı anlam taşısın.

### 6. Kontrol panelini kanıt ve eksik veri etrafında düzenle

Kullanıcı tek ekranda aktif spec revizyonunu, plan revizyonunu, requirement coverage'ı, hangi kanıtların çalıştığını, hangi SHA'nın test edilip dağıtıldığını, onay snapshot'ını, maliyeti, etkin agent/task'ları, blocker'ları ve rollback durumunu görebilmeli. Kısmi activity veya stale telemetry belirgin olmalı. Her “tamamlandı” durumu hangi acceptance criteria'yı hangi kanıtın karşıladığını göstermeli.

## Kabul için minimum uçtan uca senaryo

Bir proje fikrine 5 gereksinim ve 2 non-goal verin; son gereksinimi context bütçesini aşacak uzunlukta ama kritik seçin. Sistem spec revision üretmeli; plan DAG'ındaki her requirement'ı implementasyon ve bağımsız verification'a bağlamalı. Kullanıcı/otomatik policy'nin kabul ettiği exact plan hash'i kaydedilmeden görev başlamamalı. Bir gereksinim için kod üretildikten sonra trusted runner aynı commit SHA üzerinde test çalıştırmalı. Deploy onayı, plan revizyonu + production environment + commit/build digest'ine bağlı olmalı. Provider operation ID'si restart sonrası uzlaştırılmalı; post-deploy health başarısızsa tanımlı rollback yapılmalı. Timeline her adımı tam veya açıkça partial göstermeli. Herhangi bir şart eksik, atlanmış veya test kanıtı runner'sız ise proje “done” olamamalı.

Bu senaryo için özellikle regresyon testleri eklenmeli: uzun spesifikasyon clipping'i, requirement coverage boşluğu, eski plan onayı, source SHA değişimi, runner yokluğu/fabricated test sonucu, planner timeout/backoff, iki replika claim yarışı, deployment restart recovery ve rollback.

## İncelemenin sınırları

Bu rapor kaynak kod ve test tanımlarıyla sınırlıdır. Testlerin geçip geçmediği, production webhook config'i, gerçek agent runtime davranışı, çoğaltılmış server topolojisi, gerçek provider dağıtımının hangi kaynağı kullandığı, maliyet limitleri ve rollback garantisi canlı ortamda doğrulanmadı. Depodaki önceden var olan değişikliklere ve ürün koduna dokunulmadı.
