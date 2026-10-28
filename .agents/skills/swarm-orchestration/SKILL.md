---
name: swarm-orchestration
description: Use Ruflo persistent swarms when a task needs coordinated parallel work across independent agents.
---

# Ruflo Swarm Orchestration

Use the connected `mcp__ruflo__swarm_*` and `mcp__ruflo__agent_*` tools for persistent coordination.

- Use native in-session work for simple or tightly coupled changes.
- Before continuing an existing Ruflo effort, inspect it with `swarm_status` and `agent_list`.
- Initialize a swarm only when the task needs persistent multi-agent coordination; do not create one for a health check or a single-agent task.
- Track spawned agents with `agent_status` and `agent_logs`; report their actual results separately from code or test verification.
