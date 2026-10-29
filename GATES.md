# Gates: External project import and autonomous setup

OWNS: server/internal/projectbundle/**, server/internal/handler/project*.go, server/internal/teamprovision/**, server/internal/daemon/**, packages/core/projects/**, packages/core/api/**, packages/views/projects/**, apps/web/**, docs/architecture/**

Scope: Import an arbitrary local project directory into Multica with Git-backed source access, automatically provision the project manager/team and reviewable plan, then allow approved tasks to execute and recover the existing DeviceCleaner import.

- [x] G1: Import bootstrap enables project manager provisioning and persists a reviewable team/plan state.
  CHECK: cd server && go test ./internal/projectbundle ./internal/teamprovision ./internal/handler -count=1
  EXPECT: ok
  EVIDENCE: DeviceCleaner import persisted an awaiting_configuration team draft with six proposed roles and an autonomous plan in awaiting_approval; no execution was started.

- [x] G2: Selected local directory becomes the project source; preview is read-only and Git initializes only after import confirmation.
  CHECK: pnpm --filter @multica/views exec vitest run projects/components/project-portability-dialogs.test.tsx platform/local-directory.test.ts; pnpm --filter @multica/views typecheck; cd server && go test ./internal/daemon -run 'TestPrepareProjectDirectory|TestValidateLocalPath' -count=1
  EXPECT: 10 frontend tests pass, views typecheck exits 0, daemon tests pass.
  EVIDENCE: 10 frontend tests pass; views typecheck exits 0; daemon package tests pass. Desktop typecheck and local-directory tests also pass. DeviceCleaner source resource persisted with its local daemon ID and path; Git initialized only after import confirmation.

- [ ] G3: Imported project can be approved and tasks materialize into runnable work; existing project can recover without reimport.
  CHECK: make check
  EXPECT: passed
  EVIDENCE: pending

- [x] G4: Current web and backend Docker services run the verified images and respond successfully.
  CHECK: docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml ps
  EXPECT: Up
  EVIDENCE: Backend image sha256:548d90eb46cfe38620d0702cd31d87973122c480077793685d0180e16b9177d6 and web image sha256:0c849c2287b4bbc6b5c3b3404af3feb349952e4dd798ff3c1885bdd533cdb123 are running; /healthz returned status ok with database and migrations ok.

- [ ] G5: DeviceCleaner user accepts that chosen directory is attached, Git is initialized, and project setup reaches approval state.
  EVIDENCE: Project is imported and source/Git are verified; the six-role team draft is waiting for human approval before agent execution.
