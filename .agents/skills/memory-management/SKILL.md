---
name: memory-management
description: Use Ruflo project memory to search for durable Multica decisions and store reusable findings.
---

# Ruflo Memory Management

Use the connected `mcp__ruflo__memory_*` tools for project memory.

- Before work that may depend on prior Multica decisions, call `memory_search` with `namespace: "multica"` and a focused query.
- Store only durable project decisions or reusable findings with `memory_store`, using `namespace: "multica"` and the correct `provenance_type`.
- Do not store credentials, personal data, full task transcripts, or temporary findings.
- Use `memory_stats` to inspect storage; do not insert a test entry just to check connectivity.
