# Memory Policy

## Choose a scope

| Scope | Use for | Stable key convention |
|---|---|---|
| `global` | A broadly applicable preference or invariant | Leave `scope_key` empty |
| `project` | Knowledge shared by related repositories or systems | Stable project slug or ID |
| `repository` | Code, architecture, deployment, and test knowledge for one repository | Canonical remote such as `github.com/org/repo` |
| `agent` | Agent-specific behavior that should not affect other agents | Stable agent ID or name |
| `session` | Task-local context with no expected cross-session value | Globally unique session ID |

Do not alternate between local paths and remote URLs for the same repository. If no remote exists, use one normalized absolute path consistently.

Search exact scopes separately. `memory_search` does not perform scope inheritance. Avoid an unscoped search unless cross-project recall is intentional.

## Select a memory type

- `semantic`: a stable fact, invariant, convention, ownership rule, or current system property.
- `episodic`: an experience tied to a concrete task, incident, release, or investigation whose context matters.
- `procedural`: a repeatable sequence, diagnostic method, deployment procedure, or recovery playbook.

## Apply the durable-memory gate

Call `memory_store` only when every answer is yes:

1. Will this probably influence work in a later session?
2. Is the content self-contained, specific, and scoped?
3. Does evidence support it?
4. Are uncertainty, conditions, and limitations explicit?
5. Is it free of secrets and unnecessary sensitive data?
6. Did a search show that it is not an equivalent duplicate?

Keep temporary execution state in journal events or checkpoints. Do not store raw chats, routine tool output, obvious source code, or facts that are cheaper to read from their authoritative source.

## Attach evidence

Use the event returned by `memory_record` as `source_event_id` when the durable memory is derived from the current task. AgentMate automatically adds that event as evidence.

For external evidence, provide:

```json
{
  "source_type": "git_commit",
  "source_id": "4aa8d46",
  "excerpt": "Optional minimal excerpt supporting the memory",
  "metadata": {
    "repository": "github.com/claw-works/agentmate"
  }
}
```

Useful source types include `memory_event`, `git_commit`, `file`, `test_run`, `report`, `issue`, and `url`. Use stable identifiers. An excerpt supports inspection but does not replace the identifier.

## Write useful content

State:

- what is true or what procedure works;
- where and when it applies;
- why it matters;
- how it was verified;
- known exceptions or invalidation conditions.

Use `confidence` for evidential certainty and `importance` for expected future impact. Both range from `0` to `1` and default to `0.5`; avoid false precision.

## Handle conflict and staleness

Search defaults to `active` memories. Other lifecycle states are `pending`, `superseded`, `invalidated`, `archived`, and `expired`.

Phase 1 MCP tools cannot update or supersede an existing memory. When current evidence conflicts with a recalled memory:

1. Follow the current evidence.
2. Record a `correction` event referencing the old memory ID.
3. Do not create an ambiguous replacement that leaves two apparently current facts.
4. Report that lifecycle management is required if the old active memory could mislead later agents.
