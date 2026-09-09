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
