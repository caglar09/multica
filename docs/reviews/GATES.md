# Gates: Autonomous Project OS Verification Ledger

OWNS: server/internal/workflowruntime/**, server/internal/projectorchestration/**, server/internal/deployment/**, server/migrations/**, packages/views/projects/**, packages/core/src/api/**

Scope: Implementation of the 9 Epics and 20 PR-sized tasks defined in autonomous-project-os-action-plan.md, closing all review findings across quality gates, git merge queue, context truncation, plan approvals, deployment safety, reconciler resilience, and hybrid memory.

- [ ] G1: False-positive quality gate vulnerability closed and daemon runner verifies real tests
  CHECK: go test -v ./server/internal/workflowruntime -run "TestQualityGate"
  EXPECT: --- PASS: TestQualityGate
  EVIDENCE: pending

- [ ] G2: QA handoff requires formal verified artifact and agent free-text comment bypass blocked
  CHECK: go test -v ./server/internal/workflowruntime -run "TestStructuredQA"
  EXPECT: --- PASS: TestStructuredQA
  EVIDENCE: pending

- [ ] G3: Change set table populated on task completion and real git commit SHA exposed
  CHECK: go test -v ./server/internal/projectorchestration -run "TestChangeSet"
  EXPECT: --- PASS: TestChangeSet
  EVIDENCE: pending

- [ ] G4: Git merge queue reconciles parallel branches sequentially with conflict resolution
  CHECK: go test -v ./server/internal/workflowruntime -run "TestMergeQueue"
  EXPECT: --- PASS: TestMergeQueue
  EVIDENCE: pending

- [ ] G5: Fail-closed context prevents silent specification omission and enforces requirement coverage
  CHECK: go test -v ./server/internal/projectorchestration -run "TestContext"
  EXPECT: --- PASS: TestContext
  EVIDENCE: pending

- [ ] G6: Project Leader scope proposals update specification revision and auditor measures goal alignment
  CHECK: go test -v ./server/internal/projectorchestration -run "TestChangeStore"
  EXPECT: --- PASS: TestChangeStore
  EVIDENCE: pending

- [ ] G7: Plan lifecycle enforces cryptographic digest approval and hierarchical sub-DAG decomposition
  CHECK: go test -v ./server/internal/projectorchestration -run "TestPlanLifecycle"
  EXPECT: --- PASS: TestPlanLifecycle
  EVIDENCE: pending

- [ ] G8: Release manifest binds exact commit SHA to generic deployment service with verified rollback
  CHECK: go test -v ./server/internal/workflowruntime -run "TestDeploymentSafety"
  EXPECT: --- PASS: TestDeploymentSafety
  EVIDENCE: pending

- [ ] G9: Planning reconciler operates with distributed durable leases, bounded retries, and in-flight task cancellation
  CHECK: go test -v ./server/internal/workflowruntime -run "TestPlanningReconciler"
  EXPECT: --- PASS: TestPlanningReconciler
  EVIDENCE: pending

- [ ] G10: Hybrid pgvector memory retrieval operates with RRF and synchronous critical architectural decision persistence
  CHECK: go test -v ./server/internal/projectorchestration -run "TestBrainRecall"
  EXPECT: --- PASS: TestBrainRecall
  EVIDENCE: pending

- [ ] G11: Playwright browser QA automated journey captures DOM traces and screenshots on user scenarios
  CHECK: go test -v ./server/internal/projectorchestration -run "TestBrowserQA"
  EXPECT: --- PASS: TestBrowserQA
  EVIDENCE: pending

- [ ] G12: Frontend cockpit renders verified test badges, commit links, and requirement coverage matrix
  CHECK: pnpm test packages/views/projects
  EXPECT: Tests:       3 passed, 3 total
  EVIDENCE: pending
