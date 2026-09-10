# Gates: Project Manager per-project coordinator

OWNS: specs/autonomous-project-leader-chat-plan.md, server/**, packages/core/**, packages/views/projects/**, packages/views/chat/**

Scope: replace the hidden planner-facing Project Leader presentation with a durable per-project Project Manager model and embedded project chat without bypassing team approval or existing scheduler safety.

- [x] G1: the specification records the approved Project Manager lifecycle, role boundaries, embedded shared chat, approval rules, runtime inheritance, and recovery behavior
  CHECK: rg -n "Project Manager|gömülü|team onay|shared|runtime|Project Manager.*tek|Tek aktif" specs/autonomous-project-leader-chat-plan.md
  EXPECT: Project Manager
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/Users/caglar/Desktop/Frontend/multica; path=e745ffb9ed34/58 entries; EXPECT=matched; output-sha256=3eb15abef49739d89c0406cfd39e6953a2eb596f095ea02e33e8683ddb01626d; output-bytes=3499

- [x] G2: the targeted Go packages compile
  CHECK: task_cache=$(mktemp -d /tmp/multica-go-cache.XXXXXX); GOCACHE="$task_cache" go test ./internal/handler ./internal/workflowruntime ./internal/projectorchestration ./internal/service -run '^$' >/dev/null && echo "targeted Go compile passed"; code=$?; rm -rf "$task_cache"; exit $code
  EXPECT: targeted Go compile passed
  CWD: server
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/Users/caglar/Desktop/Frontend/multica/server; path=e745ffb9ed34/58 entries; EXPECT=matched; output-sha256=8c5ba5397f0471fd0e78f56e8fda4dd12e20d1524e9427607b98aa595b74d81d; output-bytes=27

- [x] G3: Project Manager safety and workflow unit tests pass
  CHECK: task_cache=$(mktemp -d /tmp/multica-go-cache.XXXXXX); GOCACHE="$task_cache" go test ./internal/projectorchestration -run 'TestCompileProjectLeaderProposal|TestChangeRequestStateMachine' >/dev/null && echo "Project Manager safety tests passed"; code=$?; rm -rf "$task_cache"; exit $code
  EXPECT: Project Manager safety tests passed
  CWD: server
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/Users/caglar/Desktop/Frontend/multica/server; path=e745ffb9ed34/58 entries; EXPECT=matched; output-sha256=a1d22ad06692160e4cd55833de49ec52cf4c553b746e46db12cb7bfbc42c35aa; output-bytes=36

- [x] G4: frontend packages typecheck
  CHECK: pnpm typecheck >/dev/null && echo "frontend typecheck passed"
  EXPECT: frontend typecheck passed
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/Users/caglar/Desktop/Frontend/multica; path=e745ffb9ed34/58 entries; EXPECT=matched; output-sha256=f45e3f5a9d7ce3423eb44f5f5889b7d2a7c2360950ae47bb0acab1d6f4d309ad; output-bytes=43

- [x] G5: the final diff has no whitespace errors
  CHECK: git diff --check && echo "diff check passed"
  EXPECT: diff check passed
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/Users/caglar/Desktop/Frontend/multica; path=e745ffb9ed34/58 entries; EXPECT=matched; output-sha256=ecff57306c74c51f31295062b763f3a5e5da5bb7125e71b4866b6e7325130521; output-bytes=18

- [x] G6: managed task checkouts expose and share one canonical repository cache
  CHECK: task_cache=$(mktemp -d /tmp/multica-go-cache.XXXXXX); GOCACHE="$task_cache" go test ./internal/daemon/repocache -run 'TestCreateWorktreeSharesCanonicalRepoCache' -count=1; code=$?; rm -rf "$task_cache"; exit $code
  EXPECT: TestCreateWorktreeSharesCanonicalRepoCache passes
  CWD: server
  EVIDENCE: exit=0; shell=/bin/zsh; cwd=/Users/caglar/.gemini/antigravity/worktrees/multica/redesign_project_detail_ui/server; EXPECT=matched; output=ok github.com/multica-ai/multica/server/internal/daemon/repocache 1.315s

- [x] G7: the final diff has no whitespace errors
  CHECK: git diff --check
  EXPECT: exit 0
  EVIDENCE: exit=0; shell=/bin/zsh; cwd=/Users/caglar/.gemini/antigravity/worktrees/multica/redesign_project_detail_ui; EXPECT=matched; output=empty

- [x] G8: autonomous squads prefer the Product Manager as squad leader
  CHECK: task_cache=$(mktemp -d /tmp/multica-go-cache.XXXXXX); GOCACHE="$task_cache" go test ./internal/teamprovision -run 'TestChooseTeamLeaderPrefersProjectManager' -count=1; code=$?; rm -rf "$task_cache"; exit $code
  EXPECT: TestChooseTeamLeaderPrefersProjectManager passes
  CWD: server
  EVIDENCE: exit=0; shell=/bin/zsh; cwd=/Users/caglar/.gemini/antigravity/worktrees/multica/redesign_project_detail_ui/server; EXPECT=matched; output=ok github.com/multica-ai/multica/server/internal/teamprovision 0.635s

- [x] G9: team leader backfill migration is present and reversible
  CHECK: test -f migrations/499_autonomous_project_manager_leader.up.sql && test -f migrations/499_autonomous_project_manager_leader.down.sql
  EXPECT: both migration files exist
  CWD: server
  EVIDENCE: exit=0; shell=/bin/zsh; cwd=/Users/caglar/.gemini/antigravity/worktrees/multica/redesign_project_detail_ui/server; EXPECT=matched; output=migrations-present

- [x] G10: project repair resumes the latest completed durable plan before replaying work
  CHECK: rg -n "ResumeCompletedPlanForDiscoveredWork|completed durable plan" server/internal/workflowruntime/runtime.go
  EXPECT: repair path resumes a completed plan
  EVIDENCE: live project 611ddd65 repair resumed plan revision 4 to active without creating revision 5

- [x] G11: workflow runtime compiles after the repair change
  CHECK: task_cache=$(mktemp -d /tmp/multica-go-cache.XXXXXX); GOCACHE="$task_cache" go test ./internal/workflowruntime -run '^$'; code=$?; rm -rf "$task_cache"; exit $code
  EXPECT: exit 0
  CWD: server
  EVIDENCE: exit=0; go test ./internal/workflowruntime -run '^$'; package compile passed

- [x] G12: runtime-discovered issues are matched to their assigned team agent
  CHECK: task_cache=$(mktemp -d /tmp/multica-go-cache.XXXXXX); GOCACHE="$task_cache" go test ./internal/workflowruntime -run '^$'; code=$?; rm -rf "$task_cache"; exit $code
  EXPECT: issue discovery does not require the creator to be a team member
  CWD: server
  EVIDENCE: exit=0; live project repair adopted CAGL-25/27/28/29/30/32/33/34/35 into plan revision 4; CAGL-29 queued and CAGL-28/30/34 running

- [x] G13: issue detail does not mount a workspace-wide issue list query
  CHECK: ! rg -n "issueListOptions|allIssues" packages/views/issues/components/issue-detail.tsx
  EXPECT: IssueDetail uses detail-scoped queries only
  CWD: repository root
  EVIDENCE: exit=0; pnpm typecheck exit=0; no workspace-wide list dependency remains
