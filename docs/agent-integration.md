# AgentMate Agent Integration Guide

You are an agent. This document tells you how to connect yourself to AgentMate and
what to do with it once connected. Follow it top to bottom; every step is
verifiable, and each one states what a failure means so you do not have to guess.

AgentMate is a backend, not an assistant. It stores four things for you and gives
none of them opinions of its own:

| Plane | What it holds | Why you would call it |
|---|---|---|
| **Memory** | An append-only event journal plus evidence-backed durable memories | Recall what happened before; record what you learned |
| **Skills** | Versioned skill packages (instructions + resources) with compiled L0 cards | Find a procedure you were taught; load only the part you need |
| **Knowledge** | Domain corpora with immutable revisions, plus platform-compiled wiki pages | Answer from sources you can cite |
| **App facts** | Todos, notes, reports, bookmarks, expenses | Read and write the user's actual task state |

Everything is account-scoped. One API key sees exactly one account's data.

---

## 1. Verify the deployment before anything else

```bash
curl -s $AGENTMATE_BASE_URL/api/health
```

Expect `{"status":"ok","service":"agentmate","mcp_endpoints":[...]}`.

- **HTML instead of JSON** → your base URL points at something else, or at a stale
  build. Stop and fix the URL; do not proceed.
- **Connection refused** → the deployment is not running.

`mcp_endpoints` in the reply is authoritative. Prefer it over the list in §4 of
this document: the server knows what it actually mounted.

Local development normally uses `http://localhost:26001`. Everything below assumes
`$AGENTMATE_BASE_URL` is set.

---

## 2. Get a credential

You need an **API key** (`ak_...`). Getting one requires a JWT, which requires an
account. If the operator already handed you a key, skip to §3.

```bash
# Register (first time only)
curl -s -X POST $AGENTMATE_BASE_URL/api/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"<password>"}'

# Login → JWT
TOKEN=$(curl -s -X POST $AGENTMATE_BASE_URL/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"<password>"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

# Create the key
curl -s -X POST $AGENTMATE_BASE_URL/api/auth/apikeys \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-agent","scopes":["memory:rw","skills:r","knowledge:r","todos:rw","notes:rw"]}'
```

The response is `{"api_key":{...},"key":"ak_..."}`. **The plaintext key appears in
this reply only** — it is stored hashed and cannot be retrieved again. Hand it to
the host's secret manager or environment. Never write it into a file you commit,
a log line, or a message you send to the user.

### Choosing scopes

Ask for the narrowest set that lets you do your job. A key with everything is a
key whose blast radius you cannot reason about.

| Scope | Grants |
|---|---|
| `memory:r` / `memory:rw` | Search and read memories / also record events and store memories |
| `skills:r` / `skills:rw` | Read skill catalog, instructions, resources / also publish, compile, index |
| `knowledge:r` / `knowledge:rw` | Search and read knowledge / also register sources, sync, compile wikis |
| `todos:r` / `todos:rw`, `notes:r` / `notes:rw` | The user's task state |
| `reports:r` / `reports:rw`, `bookmarks:*`, `expenses:*` | Other app domains |
| `manage_keys` | Create and delete API keys |

`:rw` implies `:r`. An **empty** `scopes` array means **full access** — that is
not a safe default, so pass an explicit list.

Two scope facts that will otherwise surprise you:

- `POST /api/knowledge/discover` needs **both** `knowledge:r` and `skills:r`. It
  reads a skill's compiled contract to decide what knowledge to look for.
- `POST /api/knowledge/resolutions` needs **`knowledge:rw` plus `skills:r`**.
- `POST /api/context/pack` authorises **per layer**. Missing a scope does not fail
  the call; that layer comes back empty with a note saying why. Partial context
  beats no context, but you will be told.

Verify the key works:

```bash
curl -s -H "X-Api-Key: $AGENTMATE_API_KEY" $AGENTMATE_BASE_URL/api/auth/me
```

- `401` → the key is wrong or was deleted.
- `403 insufficient scope` on a later call → the key is valid, the scope is not.
  These two mean different things; do not retry a 403 as if it were a 401.

---

## 3. One call that gets you oriented

Before wiring up nine MCP servers, know that a single REST call assembles
everything relevant to a task:

```bash
curl -s -X POST $AGENTMATE_BASE_URL/api/context/pack \
  -H "X-Api-Key: $AGENTMATE_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "task": "add rate limiting to the upload endpoint",
    "session_id": "<uuid you generated for this task>",
    "max_chars": 12000
  }'
```

It returns five labelled layers, in this order:

```
[SKILL]      the procedure you were taught, if one matches
[KNOWLEDGE]  evidence with citations
[MEMORY]     relevant past experience
[FACTS]      live todos and notes
[TASK]       the goal, plus what already happened in this session
```

Read it the way it is labelled. **The labels are the point**: `[KNOWLEDGE]` items
carry citations and can be traced; `[MEMORY]` items are your own past conclusions
and may be wrong; `[FACTS]` is queried live and is the only layer that reflects
current task state. Every item has a `ref` you can follow back to its origin.

`max_chars` (default 12000) is split across layers by fixed shares. Oversized
content is truncated at a paragraph boundary and flagged, and each layer reports
`char_budget`, `chars_used`, `dropped`, `truncated`. Budgets are in **characters,
not tokens** — token cost is model-specific, so apply your own ratio.

An empty layer always carries a `note` explaining which of these happened:
nothing matched, you lack the scope, or the layer is not configured. Read the note
before concluding there is nothing there.

Start here for most tasks. Reach for the individual MCP servers when you need to
**write**, or when you need a domain operation the pack does not perform.

---

## 4. Wire up MCP

Each domain mounts its **own** Streamable HTTP MCP server. There is no aggregated
endpoint, deliberately: a single server would hand you 80+ tools and most tasks
need one domain. Configure only what you need.

| Endpoint | Tools | Needs |
|---|---|---|
| `/mcp/context` | `context_pack` | per-layer scopes |
| `/mcp/memory` | `memory_record`, `memory_store`, `memory_search`, `memory_get`, `memory_timeline`, `memory_attribution`, `memory_supersede`, `memory_feedback`, `memory_feedback_list`, `memory_checkpoint_save`, `memory_resume` | `memory:r` / `memory:rw` |
| `/mcp/skills` | `skill_search`, `skill_catalog_list`, `skill_version_instructions`, `skill_version_resources`, `skill_resource_get`, `skill_log_add`, `skill_version_publish`, `skill_compile`, `skill_quality_run`, … | `skills:r` / `skills:rw` |
| `/mcp/knowledge` | `knowledge_search`, `knowledge_wiki_search`, `knowledge_catalog_list`, `knowledge_discover`, `knowledge_resolution_record`, `knowledge_compile`, … | `knowledge:r` / `knowledge:rw` |
| `/mcp/todos`, `/mcp/notes`, `/mcp/reports`, `/mcp/bookmarks`, `/mcp/expenses` | CRUD + search per domain | matching domain scope |

Client configuration is one entry per endpoint:

```json
{
  "mcpServers": {
    "agentmate-context": {
      "url": "http://localhost:26001/mcp/context",
      "headers": { "X-Api-Key": "${AGENTMATE_API_KEY}" }
    },
    "agentmate-memory": {
      "url": "http://localhost:26001/mcp/memory",
      "headers": { "X-Api-Key": "${AGENTMATE_API_KEY}" }
    }
  }
}
```

Authentication also accepts `Authorization: Bearer ak_...` or `?api_key=ak_...`.
Whether `${AGENTMATE_API_KEY}` is interpolated depends on your host — if it is
not, use the host's own secret mechanism rather than pasting the key.

MCP tool calls enforce **the same scopes as REST**. A `memory:r` key can call
`memory_search` and will be refused on `memory_store`.

### If you are testing by hand

These are Streamable HTTP servers: `initialize` first, then send the returned
`Mcp-Session-Id` header on every later request. A cold `tools/list` returns
`Invalid session ID`, which means the protocol is working, not that the endpoint
is broken. A real MCP client does this for you. See README → *Quick test* for the
two-step curl form.

---

## 5. How to use each plane well

### Memory: recall before acting, record what mattered

The official [AgentMate Memory skill](../integrations/skills/agentmate-memory/SKILL.md)
is the full protocol. The short version:

1. Generate one `session_id` per task and keep it across retries.
2. Pick the narrowest stable scope: `repository` for codebase knowledge,
   `project` for knowledge shared across repositories, `global` sparingly.
3. `memory_search` before you start. Search the repository scope first.
4. `memory_record` at goals, decisions, issues, non-trivial attempts,
   corrections, checkpoints, verified outcomes. **Not** routine commands or
   narration — a journal of everything is a journal nobody can read.
5. `memory_store` only for knowledge likely to change a future decision, and only
   with `source_event_id` or explicit `evidence`.

Rules the server enforces, so you may as well know them up front:

- Retries **must reuse the same `idempotency_key`**. Same key with different
  content is `409 Conflict`, not a silent overwrite. A stable scheme is
  `<session_id>:<sequence_no>:<event_type>`.
- Durable memories require `source_event_id` **or** at least one `evidence` item.
  A memory with neither is a claim with no provenance, and is rejected.
- Treat current source code, tests, and the user's instructions as **more
  authoritative than recalled memory**. Ignore a memory contradicted by present
  evidence, and consider `memory_feedback` with `harmful` when one misleads you.

### Skills: progressive disclosure, in that order

Do not load a whole skill package. The levels exist so you can stop early:

1. `skill_search` or `skill_catalog_list` → L0 cards (name, description,
   triggers, capabilities). Cheap. Usually enough to choose.
2. `skill_version_instructions` → L1 `SKILL.md` body. Load for the one you chose.
3. `skill_version_resources` → L2 manifest, metadata only, no content.
4. `skill_resource_get` → one selected resource body.

When you report an execution with `skill_log_add`, pass `skill_version_id` if you
know it. Logs without it stay unattributed and are excluded from that version's
quality telemetry — a session commonly runs several skills, so `session_id` alone
cannot say which one produced an outcome.

### Knowledge: two levels, and cite what you use

`knowledge_wiki_search` first (compiled pages — the synthesis), then follow a
page's citations down to the raw documents with `knowledge_search` when you need
the evidence itself. The two namespaces are separate on purpose: if a synthesis
and its own sources competed in one ranking, the synthesis would win and the
evidence would disappear.

Wiki pages are **model-generated**. A claim is only checkable by following its
citations. Do not present a page's assertion as a source fact.

If your skill declares a `knowledge:` contract, `knowledge_discover` resolves it
against what this account actually has, and tells you *which* problem you hit
rather than just returning nothing:

| Status | Means |
|---|---|
| `matched` | Candidates found within the contract's budget |
| `ambiguous` | More matched than budgeted; the contract's `on_ambiguous` says what to do |
| `no_metadata_match` | Knowledge exists but none declares what you asked for |
| `no_authorized_knowledge` | This account has no knowledge bases at all |
| `pinned_resolved` / `pinned_missing` | Pinned references resolved / one no longer exists |

These are four different fixes. Do not collapse them into "no answer".

After an execution, record what you actually used with
`knowledge_resolution_record`: the discovery fingerprint you followed, the bases
you selected, what you retrieved and cited. An empty `selected` set is worth
recording too — "discovery found nothing and I proceeded per the fallback" is
exactly the run someone will need to see later.

### App facts: read live, never cache

Todos and notes change outside your session. Query them when you need them.

---

## 6. Failure modes, and what each one actually means

| Symptom | Cause | Do this |
|---|---|---|
| `200` with HTML body | Base URL wrong, or path not an API route | Fix the URL; check §1 |
| `404 {"error":"no such endpoint: ..."}` | Path does not exist on this build | Check spelling; the deployment may predate the endpoint |
| `401` | Key missing, wrong, or deleted | Re-read the credential; do not retry unchanged |
| `403 insufficient scope: X` | Key is valid, scope is not | Request a key with scope X; retrying will not help |
| `409 Conflict` on a memory event | Same `idempotency_key`, different content | Do not mutate a retry's content. New content needs a new key |
| `Invalid session ID` from `/mcp/*` | No `initialize`, or missing `Mcp-Session-Id` | Complete the handshake; §4 |
| `501` from `/api/knowledge/compile` | No compiler model configured | Operator gap, not a failed build. Report it |
| Empty context pack layer | Nothing matched, no scope, or unconfigured | **Read the layer's `note`** — it says which |
| `indexing.status=failed` on a stored memory | Embedding or vector store unavailable | The memory **was** saved. PostgreSQL is the source of truth; search still works through full-text |

Two habits that matter more than any of the above:

- **Never claim persistence when a call failed.** If `memory_store` errored, say
  the memory was not saved. A false claim of durability is worse than no memory.
- **Report degraded state.** If AgentMate is unreachable, continue without it and
  say so. Silently working without recall looks identical to working with it,
  right up until someone relies on a memory that was never written.

---

## 7. What is not there yet

So you do not spend time looking for it:

- **No aggregated `/mcp` endpoint.** One entry per domain, by design.
- **No OAuth or device flow.** API keys only; OAuth and agent DID are on the
  roadmap.
- **`scoped_discover` contracts return `501`.** Workspaces, tags and approved
  state do not exist in the knowledge domain yet, so the mode is refused rather
  than silently widened to something broader than declared.
- **Wiki faithfulness review never blocks.** `check` is the only gate. A build
  that passed check has passed mechanical invariants, not a judgement of truth.
- **Private Git repositories are not supported.** Public GitHub/GitLab HTTPS only.
- **No push notifications.** Poll if you need to observe a queued wiki compile
  (`GET /api/knowledge/queue`).

---

## 8. Reference

- Full REST surface, every endpoint and scope: [README](../README.md)
- Machine-oriented API summary: [llms.txt](llms.txt)
- Memory protocol as an installable skill:
  [agentmate-memory](../integrations/skills/agentmate-memory/SKILL.md)
- Design rationale: [Skill + Knowledge Architecture](skill-knowledge-architecture-v0.1.md),
  [Memory Design](memory-design-v0.3.md),
  [Wiki Compiler](knowledge-wiki-compiler-k3-v0.1.md)

A human-facing UI is served from the same origin (`/skills`, `/knowledge`,
`/memory`, `/reports`) if the operator deployed the frontend. You do not need it;
it is where a person watches what you did.
