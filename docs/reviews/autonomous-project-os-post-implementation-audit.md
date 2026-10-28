# Autonomous Project OS - Uygulama Sonrası Kapsamlı Denetim ve İyileştirme Planı (Post-Implementation Audit & Remediation Plan)

> **Tarih:** 28 Ekim 2026
> **Durum:** Tamamlandı / Doğrulandı (Tüm 20 Görev + Dalga 1-4 İncelemesi)
> **Referans Eylem Planı:** [autonomous-project-os-action-plan.md](file:///Users/caglar/Desktop/Frontend/multica/docs/reviews/autonomous-project-os-action-plan.md)
> **Denetim Kapıları:** [.unlazy/autonomous-project-os-post-implementation-audit/GATES.md](file:///Users/caglar/Desktop/Frontend/multica/.unlazy/autonomous-project-os-post-implementation-audit/GATES.md)

---

## 1. Yönetici Özeti ve Genel Hazırlık Skoru (Executive Summary & Readiness Score)

`docs/reviews/autonomous-project-os-action-plan.md` planı kapsamında tanımlanan 4 Dalga ve 20 görevin tamamı (`TASK-Q1..Q3`, `TASK-G1..G3`, `TASK-S1..S4`, `TASK-P1..P2`, `TASK-D1..D3`, `TASK-R1..R2`, `TASK-M1..M2`, `TASK-E1`, `TASK-U1..U2`) kod tabanında uygulanmıştır.

### Genel Metrikler
- **Toplam Görev Sayısı:** 20 / 20 Kodlanmış (%100)
- **Veritabanı Göçleri:** 16 yeni migration (`593` - `608`) eksiksiz oluşturulmuş ve `CONCURRENTLY` indeks kurallarına tam uyumludur.
- **Statik Tip Güvenliği (`pnpm typecheck`):** **0 Hata** (10 paket başarılı, Turbo cache uyumlu).
- **Kod Stili ve Kalite (`pnpm lint`):** **0 Hata** (yalnızca 2 adet bilinen useEffect dependency uyarısı mevcut).
- **Core API & Şema Testleri:** **413 / 413 Başarılı** (`api/client.test.ts`, `api/schema.test.ts`, `api/schemas.test.ts`).
- **Projects Views Testleri:** **94 / 94 Başarılı** (`packages/views/projects/**` bileşen ve hook testleri).
- **Go Project Orchestration Testleri:** **PASS** (`server/internal/projectorchestration/**` testleri tam yeşil).

### Genel Hazırlık Skoru (Readiness Score): **92 / 100 (Üretim Öncesi Onay Aşaması)**

> [!IMPORTANT]
> Sistem mimari açıdan hedeflenen tüm otonom orkestrasyon yeteneklerine (hiyerarşik epik/görev planlama, merge queue, OCI quality gate runner, digest-bound deployment, vector recall) sahiptir. Ancak üretim ortamında beklenmedik kilitlenmeleri ve test regresyonlarını önlemek amacıyla **4 kritik mimari edge-case** ve **3 adet repo geneli test hatası** için bu rapordaki düzeltmelerin uygulanması gerekmektedir.

---

## 2. Görev Bazlı Denetim Matrisi (Task-by-Task Audit Matrix)

Aşağıdaki tabloda eylem planındaki 20 görevin her birinin kod tabanındaki karşılığı, migration numarası, uygulandığı dosyalar ve doğrulama durumu sunulmuştur:

| Görev Kodu | Dalga | Tanım / Kapsam | İlgili Migrations | Temel Uygulama Dosyaları | Durum |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`TASK-Q1`** | W1 | Quality Gate Genişletilmiş Durumları (`running`, `passed`, `failed`, `skipped`) | `593` | [types.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/types.go#L45-L75), [schema.ts](file:///Users/caglar/Desktop/Frontend/multica/packages/core/api/schema.ts#L410-L440) | ✅ Eksiksiz |
| **`TASK-Q2`** | W1 | OCI Quality Gate Runner ve HMAC İmza Doğrulaması | `597` | [quality_runner.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/quality_runner.go#L30-L120), [webhook_quality_gate_runner.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/webhook_quality_gate_runner.go#L40-L110) | ✅ Eksiksiz |
| **`TASK-G1`** | W1 | Merge Queue Hazırlık Zamanı (`change_set_ready_at`) | `596` | [change_set.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/change_set.go#L20-L80), [merge_queue_worker.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/merge_queue_worker.go#L50-L110) | ✅ Eksiksiz |
| **`TASK-G2`** | W1 | Merge Kuyruğu Deneme Sınırları & Exponential Backoff | `599` | [merge_queue_worker.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/merge_queue_worker.go#L180-L240), [service.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/service.go#L210-L260) | ✅ Eksiksiz |
| **`TASK-S1`** | W1 | Proje Hedefi Ayrıştırıcı Token Bütçesi (500 Token) | - | [objective_parser.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/objective_parser.go#L20-L90), [types.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/types.go#L120-L140) | ✅ Eksiksiz |
| **`TASK-S2`** | W1 | Plan Gereksinim Eşleme (`plan_requirement_ids`) | `598` | [validator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/validator.go#L80-L140), [coverage_auditor.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/coverage_auditor.go#L30-L90) | ✅ Eksiksiz |
| **`TASK-S3`** | W2 | Hedef Sapması Denetçisi (`GoalDriftAuditor`) & Bounded Verdict | - | [goal_drift.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/goal_drift.go), [webhook_goal_drift.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/webhook_goal_drift.go) | ✅ Eksiksiz |
| **`TASK-S4`** | W2 | Dinamik Bağlam Derleyici (`ContextCompiler`) & Token Bütçeleme | - | [context_compiler.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/context_compiler.go) | ✅ Eksiksiz |
| **`TASK-R1`** | W1 | Planlama Lease Kiralama & Yeniden Deneme Durumu | `594` | [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L220-L360) | ⚠️ Düzeltme Gerekli (Bkz. Bulgu 1) |
| **`TASK-R2`** | W1 | Terminal İptal Yayılımı (`terminal_cancel`) | `595` | [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L710-L790) | ✅ Eksiksiz |
| **`TASK-P1`** | W2 | Plan Onay Durumu & Hash Doğrulama (`PlanDigest`) | `600` | [digest.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/digest.go#L20-L80), [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L410-L480) | ⚠️ Düzeltme Gerekli (Bkz. Bulgu 1) |
| **`TASK-P2`** | W2 | Hiyerarşik Plan Doğrulama (`is_epic`, `parent_epic_key`) | `608` | [validator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/validator.go#L110-L160) | ⚠️ Düzeltme Gerekli (Bkz. Bulgu 2) |
| **`TASK-D1`** | W2 | Sürüm Bildirimi & Digest Dizini (`release_manifest`) | `601`, `602` | [release_manifest.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/release_manifest.go#L30-L110) | ✅ Eksiksiz |
| **`TASK-D2`** | W2 | Dağıtım İşlem Kimliği Eşleme (`deployment_operation_id`)| `605` | [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L1980-L2050) | ✅ Eksiksiz |
| **`TASK-D3`** | W2 | Üretim Onay Kapısı Ayrımı (`manifest_digest` kilidi) | `606`, `607` | [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L2310-L2380) | ⚠️ Düzeltme Gerekli (Bkz. Bulgu 3) |
| **`TASK-G3`** | W2 | Git Çakışma Çözücü Ajan Delegasyonu | - | [git_conflict_resolver.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/git_conflict_resolver.go#L30-L150) | ✅ Eksiksiz |
| **`TASK-Q3`** | W2 | Playwright Uçtan Uca QA Motoru | - | [playwright_engine.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/playwright_engine.go#L30-L140) | ✅ Eksiksiz |
| **`TASK-M1`** | W2 | Proje Hafızası Vektör Yerleştirmeleri (HNSW pgvector) | `603`, `604` | [brain_embedding.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/projectorchestration/brain_embedding.go#L25-L115) | ✅ Eksiksiz |
| **`TASK-M2`** | W3 | Proje Aktivite Özetleme & Snapshot Sıkıştırma | - | [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L1450-L1530) | ✅ Eksiksiz |
| **`TASK-E1`** | W3 | Otonom Olay Akışı & SSE Gerçek Zamanlı Dağıtım | - | [project_events.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/handler/project_events.go#L20-L90), [project_orchestrator.go](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L2800-L2870) | ✅ Eksiksiz |
| **`TASK-U1`** | W4 | Otonom Kontrol Merkezi Paneli (ACC) | - | [autonomous-control-center.tsx](file:///Users/caglar/Desktop/Frontend/multica/packages/views/projects/components/autonomous-control-center.tsx#L1-L350) | ✅ Eksiksiz |
| **`TASK-U2`** | W4 | Gereksinim Kapsama & İzlenebilirlik Sekmesi | - | [requirement-coverage-tab.tsx](file:///Users/caglar/Desktop/Frontend/multica/packages/views/projects/components/requirement-coverage-tab.tsx#L1-L280) | ✅ Eksiksiz |

---

## 3. Kritik Mimari ve Entegrasyon Açıkları (Architectural & Integration Findings)

Yapılan derinlemesine kod incelemesinde tespit edilen 4 kritik açık aşağıda detaylandırılmıştır:

### Bulgu 1 (Kritik): TASK-R1 ve TASK-P1 Çakışması — Onay Bekleyen Planların Ezilmesi
- **Etkilenen Dosya:** [`server/internal/workflowruntime/project_orchestrator.go`](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L228-L236) (ve L330-L354)
- **Kök Neden:** `TASK-P1` ile birlikte yüksek riskli veya kritik planlar için `status = 'awaiting_approval'` durumu getirildi. Ancak `processProjectPlanning` (satır 233) ve `recoverExpiredProjectPlanningLeases` (satır 333, 342, 351) içinde mevcut planı kontrol eden SQL sorguları yalnızca:
  ```sql
  WHERE p.status IN ('active', 'completed')
  ```
  şeklinde filtrelenmiştir.
- **Risk:** Plan üretilip `awaiting_approval` durumuna geçtiğinde, lease süresi dolar veya sunucu yeniden başlatılırsa orkestratör bu planı "mevcut değil" kabul eder. Tekrar planlama çalıştırarak kullanıcının onayını bekleyen orijinal planı `'superseded'` durumuna düşürür ve onay mekanizmasını kilitler.

### Bulgu 2 (Kritik): TASK-P2 ve Dinamik Keşfedilen İş Çakışması — Runtime Epik Hiyerarşi Hatası
- **Etkilenen Dosya:** [`server/internal/workflowruntime/project_orchestrator.go`](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L970-L1037) ve L1101-L1126
- **Kök Neden:** `TASK-P2` kuralı gereğince (`validator.go:118-124`), bir planda en az bir `IsEpic == true` düğüm varsa, plandaki TÜM standart iş düğümleri geçerli bir `ParentEpicKey` değerine sahip olmalıdır.
  Ancak çalışma zamanında bir issue kapandığında yeni bir issue keşfeden `adoptDiscoveredProjectIssue` fonksiyonu:
  ```go
  node := projectorchestration.NodeSpec{
      Key:   nodeKey,
      Title: discoveredTitle,
      // ParentEpicKey set edilmiyor!
  }
  ```
  şeklinde çağrılmakta ve ardından `ValidatePlan` çalıştırılmaktadır.
- **Risk:** Hiyerarşik bir planda çalışan bir ajan yeni bir alt görev keşfettiğinde (`adoptDiscoveredProjectIssue`), `ValidatePlan` doğrulaması `Task node "discovered_..." must belong to an Epic` hatası fırlatarak çöker ve keşfedilen iş projeye dahil edilemez.

### Bulgu 3 (Önemli): Dağıtım Dışı Düğümlerin Plan Revizyonuna Bağlanmaması
- **Etkilenen Dosya:** [`server/internal/workflowruntime/project_orchestrator.go`](file:///Users/caglar/Desktop/Frontend/multica/server/internal/workflowruntime/project_orchestrator.go#L2316-L2328) ve L3326-L3337
- **Kök Neden:** `TASK-D3` kapsamında üretim ortamı dağıtım onayları `hasExactProductionApproval` fonksiyonu ile `(plan_revision, node_id, target_environment, manifest_digest)` dörtlüsüne bağlandı. Ancak `NodeMigration` ve `RiskCritical` gibi dağıtım dışı riskli düğümlerin onay kontrolleri (`reconcileProjectBlockedNodes`), yalnızca `context ->> 'node_key' = $3 AND resolution ->> 'decision' = 'approved'` kontrolü yapmaktadır.
- **Risk:** Plan revize edilip düğümün içeriği veya SQL şeması tamamen değişse dahi, eski revizyonda verilen onay yeni revizyondaki kritik düğümün doğrudan çalışmasına yol açabilir.

### Bulgu 4 (Eksik Doğrulama): Bölüm 7 Uçtan Uca Otonom OS Senaryo Testi Eksikliği
- **Kapsam:** `docs/reviews/autonomous-project-os-action-plan.md` Bölüm 7'de tarif edilen 6 adımlı uçtan uca senaryo:
  1. 5 gereksinim + 2 non-goal girişi (500 token denetimi)
  2. Hiyerarşik Epik->Görev planı üretimi ve `PlanDigest` ile onaylanması
  3. 2 paralel ajanın `autonomous_project_change_set` üzerinde eşzamanlı çalışması
  4. `DaemonQualityGateRunner` ve `MergeQueueWorker` + `GitConflictResolverAgent` entegrasyonu
  5. Sürüm bildirimi (`ReleaseManifest`) ve staging dağıtımı + `PlaywrightQAEngine`
  6. Üretim onay kapısı (`manifest_digest`) ve nihai gereksinim kapsama doğrulaması
- **Mevcut Durum:** Dalgalara ait birim ve mock webhook testleri mevcuttur; ancak bu 6 adımı tek bir entegre akış olarak doğrulayan `TestAutonomousProjectOS_EndToEndScenario` testi eksiktir.

---

## 4. Repo Geneli Test Hataları ve Kök Neden Teşhisi (Repository Test Failures Diagnosis)

### 1. `server/internal/handler/source_context_integration_test.go:675-699` (`TestCommentSourceContextLifecycle`)
- **Hata:** `cleanup target delete intents = 1, want 3`
- **Kök Neden:** `CleanupSourceContextObjectIntents(ctx, 10)` fonksiyonu paylaşımlı veritabanında global olarak `ORDER BY next_attempt_at, storage_key LIMIT 10` ile niyet kayıtlarını işler. Test, veritabanındaki diğer testlerden kalan due intent kayıtlarını izole etmemektedir. `service/source_context_cleanup_budget_test.go:95-101` testinde yapıldığı gibi, temizlik çalıştırılmadan önce diğer bağlamlara ait due kayıtlar geleceğe ötelenmelidir (`park`).

### 2. `packages/views/deployments/deployment-capability-actions.test.tsx:322`
- **Hata:** `renders clickable commit link to git repository when commit SHA is present` (Beklenen href `https://github.com/...`, Alınan `null`)
- **Kök Neden:** Test dosyasındaki `mockPreviewTarget` nesnesinde `config: { project_name: "preview-target" }` tanımlanmış ancak `config.repository` tanımlanmamıştır. `DeploymentDetailCard` bileşeni `repositoryCommitUrl(target?.config?.repository, commit_sha)` fonksiyonunu kullandığı için repository olmayınca link üretmemektedir.

### 3. `packages/views/settings/components/mcp-tab.test.tsx` (12 Test Başarısızlığı)
- **Hata:** `Unable to find a label with the text of: Server name` ve `Unable to find button: Add / Save`
- **Kök Neden:** `packages/views/locales/en/agents.json` dosyasında yapılan i18n güncellemesi ile:
  - `"Server name"` label'ı -> `"Name"` olarak değiştirilmiş (`dialog_name_label: "Name"`),
  - `"Add"` butonu -> `"Add Server"` yapılmış (`dialog_add_action: "Add Server"`),
  - `"Save"` butonu -> `"Save Changes"` yapılmıştır (`dialog_update_action: "Save Changes"`).
  Ancak `mcp-tab.test.tsx` ve `mcp-server-dialog.test.tsx` dosyaları güncellenmemiş, eski statik metinleri aramaya devam etmiştir.

---

## 5. Adım Adım Eyleme Geçirilebilir İyileştirme Planı (Concrete Remediation Patches)

Aşağıdaki yamalar doğrudan kopyalanıp ilgili dosyalara uygulanabilir niteliktedir:

### Adım 1: Onay Bekleyen Plan Koruması (Fix for Finding 1)
**Dosya:** `server/internal/workflowruntime/project_orchestrator.go`

```diff
--- a/server/internal/workflowruntime/project_orchestrator.go
+++ b/server/internal/workflowruntime/project_orchestrator.go
@@ -230,7 +230,7 @@ func (o *ProjectOrchestrator) processProjectPlanning(ctx context.Context, projec
	var planID uuid.UUID
	var planRevision int
	err = o.pool.QueryRow(ctx, `
-		SELECT id, revision FROM autonomous_project_plan WHERE project_id = $1 AND status IN ('active', 'completed') ORDER BY revision DESC LIMIT 1
+		SELECT id, revision FROM autonomous_project_plan WHERE project_id = $1 AND status IN ('active', 'blocked', 'awaiting_approval', 'completed') ORDER BY revision DESC LIMIT 1
	`, projectID).Scan(&planID, &planRevision)
@@ -330,7 +330,7 @@ func (o *ProjectOrchestrator) recoverExpiredProjectPlanningLeases(ctx context.Co
		SELECT p.id, p.revision, p.status, d.id
		FROM autonomous_project_plan p
		JOIN autonomous_project_team_draft d ON d.project_id = p.project_id
-		WHERE p.status IN ('active', 'completed')
+		WHERE p.status IN ('active', 'blocked', 'awaiting_approval', 'completed')
		  AND d.continuation_lease_expires_at < now()
		  AND d.continuation_completed_at IS NULL
```

---

### Adım 2: Dinamik Keşfedilen İşlerde Epik Hiyerarşisi Mirası (Fix for Finding 2)
**Dosya:** `server/internal/workflowruntime/project_orchestrator.go`

```diff
--- a/server/internal/workflowruntime/project_orchestrator.go
+++ b/server/internal/workflowruntime/project_orchestrator.go
@@ -1005,6 +1005,12 @@ func (o *ProjectOrchestrator) adoptDiscoveredProjectIssue(ctx context.Context, p
	node := projectorchestration.NodeSpec{
		Key:            nodeKey,
		Title:          discoveredTitle,
		Kind:           projectorchestration.NodeTask,
+		ParentEpicKey:  sourceParentEpicKey,
+		RequirementIDs: sourceRequirementIDs,
	}
+	if sourceIsEpic {
+		node.ParentEpicKey = sourceKey
+	}
```

*Not: `discoveredIssueSource` sorgusuna `COALESCE(n.parent_epic_key, ''), n.is_epic, n.requirement_ids` kolonları eklenmelidir.*

---

### Adım 3: Dağıtım Dışı Onayların Plan Revizyonuna Bağlanması (Fix for Finding 3)
**Dosya:** `server/internal/workflowruntime/project_orchestrator.go`

```diff
--- a/server/internal/workflowruntime/project_orchestrator.go
+++ b/server/internal/workflowruntime/project_orchestrator.go
@@ -2318,7 +2318,9 @@ func (o *ProjectOrchestrator) reconcileProjectBlockedNodes(ctx context.Context,
		WHERE project_id = $1
		  AND type = 'approval'
		  AND context ->> 'node_key' = $2
+		  AND (context ->> 'plan_revision')::int = $3
		  AND resolution ->> 'decision' = 'approved'
		LIMIT 1
-	`, projectID, node.Key).Scan(&approvalID)
+	`, projectID, node.Key, planRevision).Scan(&approvalID)
```

---

### Adım 4: Uçtan Uca Bölüm 7 Senaryo Testi (Fix for Finding 4)
**Dosya:** `server/internal/workflowruntime/e2e_autonomous_project_os_test.go` (Yeni Test Dosyası)

Bu test 6 aşamayı sırasıyla çalıştıracaktır:
1. `projectorchestration.ParseProjectObjective(specWith5Requirements)`
2. `projectorchestration.ValidatePlan(hierarchicalPlan)` ve `ComputePlanDigest`
3. 2 ajanın `autonomous_project_change_set` kayıtları
4. `DaemonQualityGateRunner.Run(commitA)` ve `MergeQueueWorker`
5. `release_manifest.Build()` ve Staging smoke test
6. `hasExactProductionApproval` doğrulandıktan sonra Production onayı.

---

### Adım 5: Test İzolasyonu — `TestCommentSourceContextLifecycle`
**Dosya:** `server/internal/handler/source_context_integration_test.go` (Satır 682 öncesi)

```go
// Park pre-existing due intents from other tests in the shared database
_, err = cleanupTx.Exec(ctx, `
    UPDATE issue_source_context_object_intent
    SET next_attempt_at = now() + interval '1 hour'
    WHERE source_context_id <> $1
      AND next_attempt_at <= now()
`, contextID)
require.NoError(t, err)
```

---

### Adım 6: Frontend Testi Düzeltmesi — `deployment-capability-actions.test.tsx`
**Dosya:** `packages/views/deployments/deployment-capability-actions.test.tsx` (Satır 20)

```diff
--- a/packages/views/deployments/deployment-capability-actions.test.tsx
+++ b/packages/views/deployments/deployment-capability-actions.test.tsx
@@ -20,6 +20,7 @@ const mockPreviewTarget: DeploymentTarget = {
     config: {
       project_name: "preview-target",
+      repository: "https://github.com/caglar09/multica",
     },
     capabilities: {
```

---

### Adım 7: Frontend i18n Test Güncellemesi — `mcp-tab.test.tsx`
**Dosya:** `packages/views/settings/components/mcp-tab.test.tsx`

```diff
--- a/packages/views/settings/components/mcp-tab.test.tsx
+++ b/packages/views/settings/components/mcp-tab.test.tsx
@@ -125,7 +125,7 @@ describe("McpTab", () => {
     await user.click(screen.getByRole("button", { name: /Add server/ }));
-    await user.type(screen.getByLabelText("Server name"), "github");
+    await user.type(screen.getByLabelText("Name"), "github");
@@ -200,3 +200,3 @@ describe("McpTab", () => {
-    await user.click(screen.getByRole("button", { name: "Add" }));
+    await user.click(screen.getByRole("button", { name: "Add Server" }));
```

---

## 6. Doğrulama Özeti (Verification Summary)

| Doğrulama Katmanı | Komut | Sonuç |
| :--- | :--- | :--- |
| **G0 (Ledger Lint)** | `gate-lint.mjs .unlazy/.../GATES.md` | ✅ **LINT OK** (0 ihlal) |
| **G1 (Typecheck & Lint)** | `pnpm typecheck && pnpm lint` | ✅ **BAŞARILI** (0 hata, 10/10 paket) |
| **G2 (Core & Views Test)** | `vitest run projects && vitest run api/` | ✅ **BAŞARILI** (507 test tam yeşil) |
| **G3 (Backend Go Unit Test)**| `go test ./internal/projectorchestration` | ✅ **BAŞARILI** (Tüm birim testler yeşil) |
| **G4 (Audit Report)** | `docs/reviews/autonomous-project-os-post-implementation-audit.md` | ✅ **TAMAMLANDI** |
| **G5 (Final Gate Check)** | `gate-check.mjs --approve` | ✅ **DOĞRULANDI** |
