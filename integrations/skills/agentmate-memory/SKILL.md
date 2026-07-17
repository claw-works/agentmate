---
name: agentmate-memory
description: Recall and preserve cross-session context with AgentMate Memory through the memory_search, memory_record, memory_store, and memory_get MCP tools. Use when an agent begins or resumes project or repository work, needs prior decisions, failures, or procedures, reaches a meaningful decision, correction, checkpoint, or outcome, or should save evidence-backed knowledge for future tasks.
---

# AgentMate Memory

Use the event journal as an audit trail and durable memory as a small, reusable knowledge layer. Treat current source code, tests, and user instructions as more authoritative than recalled memory.

## Run the workflow

1. Establish context.
   - Create one globally unique `session_id` for the task and retain it across retries.
   - Choose the narrowest stable scope. Prefer `repository` for codebase knowledge and `project` for knowledge shared by several repositories.
   - Use a canonical `scope_key` consistently. See [memory-policy.md](references/memory-policy.md).

2. Recall before acting.
   - Call `memory_search` with the task, relevant component names, symptoms, and error text.
   - Search the repository scope first. Search project or global scope separately only when relevant.
   - Start with `top_k: 5` to `8`; refine the query instead of loading many weak matches.
   - Call `memory_get` before relying on a high-impact result so its evidence can be inspected.
   - Ignore memories contradicted by current evidence or outside their stated conditions.

3. Record meaningful progress.
   - Call `memory_record` for goals, decisions, issues, non-trivial attempts, corrections, checkpoints, and verified outcomes.
   - Increase `sequence_no` monotonically within the session.
   - Use a stable retry key such as `<session_id>:<sequence_no>:<event_type>`. Retry the exact same event with the same key; changed content requires a new key.
   - Do not journal routine commands, transient narration, or full conversation dumps.
   - Read [event-types.md](references/event-types.md) when selecting event types or payload fields.

4. Promote only durable knowledge.
   - Search for an existing equivalent memory before calling `memory_store`.
   - Store only information likely to change a future decision or action.
   - Select `semantic` for facts, `episodic` for a contextual experience, and `procedural` for a repeatable method.
   - Supply `source_event_id` or at least one evidence object. State conditions, limitations, and verification in the content.
   - Read [memory-policy.md](references/memory-policy.md) before persisting durable memory.

5. Close the task.
   - Record a verified `outcome`, or a `checkpoint` with completed work, open issues, and next steps.
   - Promote a durable lesson only if it passes the quality gate in [memory-policy.md](references/memory-policy.md).

## Enforce safety

- Never send passwords, API keys, tokens, private keys, session cookies, or raw secret-bearing configuration.
- Minimize personal and confidential data. Store a stable reference and a necessary excerpt instead of a whole source.
- Do not turn guesses into facts. Record uncertainty and use conservative confidence values.
- Do not claim persistence when a tool call fails.
- If the MCP server is unavailable, continue without memory or use the authenticated REST fallback in [mcp-setup.md](references/mcp-setup.md), and report the degraded state.

## Configure the integration

Read [mcp-setup.md](references/mcp-setup.md) when installing the skill, configuring credentials or scopes, using REST, or troubleshooting tool availability.
