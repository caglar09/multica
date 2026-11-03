# Glossary

- **Issue**: a user-requested unit of work with an owner, status, and optional dependencies.
- **Run**: one execution by an agent. A run may act on an issue and may produce tool activity and a result.
- **Blocker**: a condition that prevents an issue, run, or decision from progressing. A blocker can be a dependency, failure, unavailable runtime, or required approval; it does not always mean a system defect.
- **Handoff**: a transfer of work from an owner to a reviewer, including any later review cycle. A dependency describes required ordering and is not itself a handoff.

## Autonomous projects

- **Project specification**: the agreed statement of a project's intended outcome, requirements, constraints, non-goals, and acceptance expectations.
- **Specification revision**: one version of a project specification, reviewed as a specific statement of scope.
- **Approval**: an actor's decision to approve or reject a specific project subject and version. An imported approval preserves the source decision but does not grant authority in the receiving project.
- **Local approval**: an approval by an authorized human in the receiving project that matches the current specification revision.
- **Adoption**: a local decision that imported project content may participate in work in the receiving project. Adoption does not itself approve a plan or assign work.
