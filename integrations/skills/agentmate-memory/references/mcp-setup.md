# MCP Setup and API Reference

## Configure the MCP server

AgentMate exposes Memory as a Streamable HTTP MCP server:

```text
<AGENTMATE_BASE_URL>/mcp/memory
```

Local development normally uses `http://localhost:26001/mcp/memory`. Configure a remote deployment URL in the agent host rather than editing this skill.

Create a dedicated API key with `memory:rw`, which implies `memory:r`. Use `memory:r` for recall-only agents. Store the key in the host's environment or secret manager; never commit it.

A generic MCP client entry is:

```json
{
  "mcpServers": {
    "agentmate-memory": {
      "url": "http://localhost:26001/mcp/memory",
      "headers": {
        "X-Api-Key": "${AGENTMATE_API_KEY}"
      }
    }
  }
}
```

Environment interpolation depends on the client. Use its supported secret mechanism if `${AGENTMATE_API_KEY}` is not expanded. AgentMate also accepts `Authorization: Bearer ak_...`.

Use HTTPS when credentials cross an untrusted network. Plain HTTP is appropriate only for localhost or a separately protected trusted network.

## MCP tools

### `memory_search`

Required:

- `query`: task, fact, error, or experience to recall.

Optional:

- `top_k`: `1` to `20`, default `8`.
- `scope_type`: `global`, `project`, `repository`, `agent`, or `session`.
- `scope_key`: requires `scope_type`.
- `memory_type`: `semantic`, `episodic`, or `procedural`.
- `status`: defaults to `active`.

### `memory_record`

Required:

- `event_type`: see [event-types.md](event-types.md).
- `idempotency_key`: stable account-wide retry key, at most 512 characters.

Optional:

- `payload`: structured JSON object.
- `scope_type` and `scope_key`; non-global scopes require a key.
- `session_id` and monotonic `sequence_no`; a sequence requires a session.
- `source_type` and `source_id`; provide both or neither.

The result contains `event` and `created`. Preserve `event.id` as evidence for a later `memory_store`.

### `memory_store`

Required:

- `memory_type`: `semantic`, `episodic`, or `procedural`.
- `content`: self-contained durable knowledge.
- At least one of `source_event_id` or `evidence`.

Optional:

- `title`, `summary`, `scope_type`, `scope_key`, and `metadata`.
- `confidence` and `importance`, each from `0` to `1`.
- `evidence`: objects with required `source_type` and `source_id`, plus optional `excerpt` and `metadata`.

### `memory_get`

Required:

- `id`: durable memory entry ID.

The result includes the memory, all evidence, and indexing state when available.

## REST fallback

Use the same API key in `X-Api-Key`:

```text
POST /api/memory/events
POST /api/memory/entries
GET  /api/memory/entries
GET  /api/memory/entries/:id
POST /api/memory/search
```

REST request bodies use the same field names as the MCP tools. A durable memory remains committed in PostgreSQL if vector indexing fails; inspect `indexing.status` and do not assume semantic retrieval is available until indexing succeeds.
