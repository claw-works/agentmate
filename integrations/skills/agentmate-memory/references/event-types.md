# Memory Event Types

`memory_record` accepts an arbitrary JSON object in `payload`. Use the following compact conventions so later consolidation can interpret events consistently.

## Common fields

- `summary`: one self-contained sentence describing what changed.
- `details`: only information needed to understand or reproduce the event.
- `artifacts`: stable file paths, commit IDs, report IDs, test names, or URLs.
- `verification`: command, test, observation, or other confirmation.

Omit empty fields. Never include credentials or an unfiltered environment dump.

## Type selection

| Event type | Use for | Recommended payload fields |
|---|---|---|
| `goal` | A task objective or changed objective | `summary`, `constraints`, `success_criteria` |
| `observation` | A relevant fact discovered during work | `summary`, `source`, `artifacts` |
| `action` | A consequential operation worth auditing | `summary`, `target`, `artifacts` |
| `decision` | A choice that constrains later work | `summary`, `rationale`, `alternatives`, `artifacts` |
| `issue` | A blocker, defect, conflict, or risk | `summary`, `symptoms`, `impact`, `artifacts` |
| `attempt` | A non-trivial approach, especially one that failed | `summary`, `approach`, `result`, `verification` |
| `outcome` | A verified result or task completion state | `summary`, `status`, `verification`, `artifacts` |
| `correction` | A previous fact or decision shown to be wrong | `summary`, `replaces`, `reason`, `verification` |
| `checkpoint` | A resumable snapshot before pausing or handing off | `summary`, `completed`, `open_issues`, `next_steps` |
| `note` | Relevant context that fits no stronger type | `summary`, `details` |

Prefer the most specific type. Use `note` sparingly.

## Example

```json
{
  "event_type": "decision",
  "idempotency_key": "0190-task:12:decision",
  "session_id": "0190-task",
  "sequence_no": 12,
  "scope_type": "repository",
  "scope_key": "github.com/claw-works/agentmate",
  "payload": {
    "summary": "Use PostgreSQL as the memory source of truth and Qdrant as a rebuildable index.",
    "rationale": "Memory remains auditable and searchable when vector indexing is unavailable.",
    "alternatives": ["Treat Qdrant as the primary store"],
    "artifacts": ["docs/memory-design-v0.3.md"]
  }
}
```

The `idempotency_key` is account-wide. Reusing it with different event content returns a conflict. Provide `source_type` and `source_id` together or omit both.
