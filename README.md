# Agentmate

AI-native tool service platform (Backend as Toolset). Pure API product, no UI. Any external Agent can integrate via REST API or MCP. Multi-tenant SaaS, designed for high concurrency.

## Architecture

```
┌──────────────────────────────────────┐
│         External Agents              │
│  (Claude, GPT, Custom, etc.)        │
└────┬─────────────────────┬───────────┘
     │ REST (JWT/API Key)  │ MCP (stdio)
┌────▼────┐          ┌─────▼─────┐
│ Gin API │          │ MCP Server│
└────┬────┘          └─────┬─────┘
     │                     │
┌────▼─────────────────────▼────┐
│     Service Layer             │
│  auth / todo / notes          │
└────┬──────────────────────────┘
     │
┌────▼──────────────────────────┐
│     Repository Layer          │
│  pgx v5 + PostgreSQL          │
└───────────────────────────────┘
```

## Tech Stack

- Go 1.22+, Gin, pgx v5, sqlc, golang-migrate
- Auth: JWT + API Key (dual-track) with scopes
- MCP: mark3labs/mcp-go

## Quick Start

Local dev/deployment (git pull + build + run, backed by the shared `base`
Postgres/Qdrant infrastructure) is managed from `infra/agentmate/deploy` — see
that directory's `README.md` for the full setup. Summary:

```bash
cd infra/agentmate/deploy
cp agentmate.env.example agentmate.env   # fill in AGENTMATE_SRC_DIR, secrets, etc.
nohup bash run.sh >> nohup.log 2>&1 &
# Server runs at http://0.0.0.0:26001
```

### Manual (without infra scripts)

```bash
# 1. Run migrations against your PostgreSQL instance
migrate -path migrations -database "$DATABASE_URL" up

# 2. Start server
cp .env.example .env
go run ./cmd/server
```

## Agent Skills

The architecture and implementation roadmap for the Git-backed registry are documented in
[Skill Registry Design v0.1](docs/skill-registry-design-v0.1.md). Skill Registry Phase 4 is
implemented as an offline deterministic quality layer: package lint, platform contract checks,
same-skill release comparison (including package identity and resource behavior metadata), and
strictly version-bound telemetry suggestions. It does not call
LLMs, providers, Qdrant, publish/activate/index versions, produce a composite score, or claim
semantic evaluation/reinforcement learning. DAG composition remains a later increment.

Skills and knowledge are separate domains. Skill packages carry behavior and execution
assets; domain knowledge corpora belong to a standalone Knowledge Registry that
skills discover at runtime through a Knowledge Discovery Contract instead of fixed bindings.
The target model is specified in
[Skill + Knowledge Architecture v0.3](docs/skill-knowledge-architecture-v0.1.md).
The K1 milestone (knowledge sources, immutable revisions, document snapshots) and the
K2 milestone (K0 catalog cards, deterministic Markdown chunking, document link graph,
account-scoped hybrid retrieval) are implemented; the knowledge compiler and runtime
KnowledgeResolutionRun remain unimplemented.

The official [AgentMate Memory skill](integrations/skills/agentmate-memory/SKILL.md)
teaches compatible agents to recall scoped context, journal meaningful events,
and preserve evidence-backed durable memory through the Memory MCP server.

Install the complete `integrations/skills/agentmate-memory` directory in the
agent host's skills directory, then configure its MCP client to use
`http://localhost:26001/mcp/memory` or the deployment's corresponding URL. Keep
the API key in the host's environment or secret manager.

## API Endpoints

All REST endpoints are mounted under `/api` (kept separate from the frontend's
page routes when served from the same origin, see `infra/agentmate`).

### Auth (public)
- `POST /api/auth/register` — Register a new user
- `POST /api/auth/login` — Login, returns JWT

### Auth (authenticated)
- `GET /api/auth/me` — Current user info
- `POST /api/auth/apikeys` — Create API Key (accepts optional `scopes` field)
- `GET /api/auth/apikeys` — List API Keys
- `DELETE /api/auth/apikeys/:id` — Delete API Key

#### Create API Key with Scopes

```bash
curl -X POST http://localhost:26001/api/auth/apikeys \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-agent", "scopes": ["todos:rw", "notes:r"]}'
```

Available scopes:
| Scope | Description |
|-------|-------------|
| `todos:r` | Read todos |
| `todos:rw` | Read & write todos (implies `todos:r`) |
| `notes:r` | Read notes |
| `notes:rw` | Read & write notes (implies `notes:r`) |
| `reports:r` | Read reports |
| `reports:rw` | Read & write reports (implies `reports:r`) |
| `bookmarks:r` | Read bookmarks |
| `bookmarks:rw` | Read & write bookmarks (implies `bookmarks:r`) |
| `expenses:r` | Read expenses |
| `expenses:rw` | Read & write expenses (implies `expenses:r`) |
| `memory:r` | Read and search durable memories |
| `memory:rw` | Record events and store durable memories (implies `memory:r`) |
| `skills:r` | Read skill logs and versions |
| `skills:rw` | Read & write skill logs and versions (implies `skills:r`) |
| `knowledge:r` | Read knowledge sources, revisions, and documents |
| `knowledge:rw` | Register/sync knowledge sources and push snapshots (implies `knowledge:r`) |
| `manage_keys` | Create/delete API keys |

Empty scopes array `[]` means **full access**.

### Todos (authenticated)
- `POST /api/todos` — Create (scope: `todos:rw`)
- `GET /api/todos` — List (scope: `todos:r`)
- `GET /api/todos/search?q=` — Search (scope: `todos:r`)
- `GET /api/todos/:id` — Get by ID (scope: `todos:r`)
- `PATCH /api/todos/:id` — Update (scope: `todos:rw`)
- `DELETE /api/todos/:id` — Delete (scope: `todos:rw`)

### Notes (authenticated)
- `POST /api/notes` — Create (scope: `notes:rw`)
- `GET /api/notes` — List (scope: `notes:r`)
- `GET /api/notes/search?q=` — Search (scope: `notes:r`)
- `GET /api/notes/:id` — Get by ID (scope: `notes:r`)
- `PATCH /api/notes/:id` — Update (scope: `notes:rw`)
- `DELETE /api/notes/:id` — Delete (scope: `notes:rw`)

### Memory (authenticated)
- `POST /api/memory/events` — Append an immutable, idempotent memory event (scope: `memory:rw`)
- `POST /api/memory/entries` — Store an evidence-backed durable memory (scope: `memory:rw`)
- `GET /api/memory/entries` — List memories by scope, type, or status (scope: `memory:r`)
- `GET /api/memory/entries/:id` — Get a memory with its evidence (scope: `memory:r`)
- `POST /api/memory/search` — Hybrid PostgreSQL FTS and Qdrant search (scope: `memory:r`)
- `GET /api/memory/timeline?session_id=&skill_version_id=&limit=` — Time-ordered merge of skill executions and memory events. Requires `session_id` or `skill_version_id`; an unfiltered account-wide timeline is a data dump, not attribution. Reports `skill_log_count`, `memory_event_count`, `unattributed_count` and `truncated` so the coverage of an attribution conclusion is explicit (scope: `memory:r`)
- `POST /api/memory/entries/:id/supersede` — Record that this entry replaces another (body `superseded_id`). The replaced entry moves to `superseded`, its validity window closes at the supersede time, and its retrieval projection is deleted (scope: `memory:rw`)
- `POST /api/memory/entries/:id/feedback` — Report whether a memory helped (body `signal` = `useful`|`harmful`, plus `reason`, `session_id`, `skill_version_id`, `metadata`) (scope: `memory:rw`)
- `GET /api/memory/entries/:id/feedback?limit=` — The usefulness signal log, newest first (scope: `memory:r`)
- `POST /api/memory/checkpoints` — Save a resumable snapshot of session intent (body `session_id`, `goal` required, plus `done`, `next`, `open`, `notes`, `label`, `scope_type`, `scope_key`, `skill_version_id`) (scope: `memory:rw`)
- `GET /api/memory/resume?session_id=` — Latest checkpoint plus everything recorded after it; `resolution` is `checkpoint`, `journal_only` or `empty` (scope: `memory:r`)
- `GET /api/memory/entries/:id/attribution` — Resolve which skill execution produced a durable memory. Walks entry → source event → skill version and reports how far the chain got via `resolution`: `skill_version`, `session_only`, `event_only`, or `none`. Includes the surrounding session timeline when a session is known (scope: `memory:r`)

Memory events carry an optional `skill_version_id` attributing them to the skill
execution that produced them. `session_id` alone is not enough: a session commonly
runs several skills, so session-level correlation cannot tell which execution
produced a given event. The value is verified against the caller's account, and it
participates in the idempotency hash — a replay that adds or changes attribution
returns `409 Conflict` rather than silently returning the original unattributed
row. Leave it unset for events with no skill origin, such as a note the user wrote
directly.

Superseding builds a chain, not a graph: C replacing B which replaced A is fine,
but a cycle is rejected with `409` because it makes "which entry is current"
unanswerable. Replaying the same supersede is idempotent; pointing an
already-replaced entry at a different replacement is a `409` conflict. Deleting the
retrieval projection matters as much as the status change — search draws candidates
from the projection and filters by status afterwards, so a replaced entry left
indexed would keep consuming top-k slots and crowd out its replacement.

Feedback signals are the durable record; `useful_count` and `harmful_count` on the
entry are a projection of them, kept because ranking cannot afford an aggregate
query per search. One signal of each kind per memory per session, so a retrying
agent cannot inflate a memory's standing. The search score is nudged by a bounded
adjustment (at most ±0.15 on a 0..1 scale) weighted by how much evidence exists:
feedback is a weak, biased signal, so it breaks ties and demotes repeatedly harmful
memories rather than overriding semantic relevance. `retrieval_score` and
`feedback_adjustment` are reported separately so a surprising order can be
explained.

Checkpoints are stored as `checkpoint` events on the journal rather than in their
own table, inheriting immutability, ordering, idempotency and skill attribution.
The default idempotency key is derived from the content, so saving unchanged state
is a no-op instead of appending a near-duplicate. `resume` returns the snapshot
**plus the activity recorded after it**: a session is interrupted after its last
checkpoint, so that tail is exactly the state the snapshot is missing.

Event retries must reuse the same `idempotency_key`. Reusing a key with different
event content returns `409 Conflict`. Durable memories require either
`source_event_id` or at least one `evidence` item. PostgreSQL is the source of truth:
if embedding or Qdrant indexing fails, creation still succeeds with
`indexing.status=failed`, and search continues through PostgreSQL FTS.

### Context Pack (authenticated)

- `POST /api/context/pack` — Assemble the minimal execution context for a task in one call. Body: `task` (required), `query`, `skill_name`, `session_id`, `knowledge_domain`, `knowledge_source_ids`, `memory_scope_type`, `memory_scope_key`, `max_chars`, `top_k`, `layers`, `render` (route scope: `memory:r`, plus per-layer scopes below)

Five layers, assembled and rendered in this order: `[SKILL]` instructions,
`[KNOWLEDGE]` evidence with citations, `[MEMORY]` relevant experience, `[FACTS]`
live todos and notes, `[TASK]` the goal plus recent session activity. Every item
carries a source label and a traceable `ref` so a model can tell authority apart
and a claim can be traced back to its origin.

The value is the budget, not the concatenation. `max_chars` (default 12000) is
split across the requested layers by fixed shares; omitted layers hand their
share to the rest. Within a pack a layer's budget is never lent to another, so
the result does not depend on assembly order. Oversized content is truncated at a
paragraph or sentence boundary and flagged per item, and each layer reports
`char_budget`, `chars_used`, `dropped` and `truncated`. Budgets are in characters
rather than tokens deliberately: token cost is model specific, so embedding a
tokenizer would tie the server to one vendor — characters are exact and the
caller can apply its own ratio.

Authorisation is per layer, not per endpoint: one endpoint spanning five domains
must not let a `skills:r` key read memory. `SKILL` needs `skills:r`, `KNOWLEDGE`
needs `knowledge:r`, `MEMORY` needs `memory:r`, `FACTS` needs `todos:r` and/or
`notes:r` (each half authorised independently), and `TASK` needs no scope for the
goal statement but `memory:r` for the session slice. A layer the caller may not
read comes back empty with an explanatory note, and the call still succeeds —
partial context beats no context, but never silently. The same applies to a
failing or unconfigured layer.

`TASK` prefers a saved checkpoint over reconstruction, and includes the activity
recorded after it. Sessions with no checkpoint fall back to a journal replay, and
the layer note says which of the two was used. One current limitation remains
reported rather than hidden: skill selection is a pinned `skill_name` or the top
retrieval hit; dynamic discovery driven by a Skill's knowledge contract is K4.

`FACTS` is queried live and never embedded: task state changes constantly, so an
indexed copy would serve stale facts with the confidence of retrieved evidence.

### Skills (authenticated)
- `POST /api/skills/sources` — Register or update a skill source (`git` or `local`) (scope: `skills:rw`)
- `GET /api/skills/sources` — List skill sources (scope: `skills:r`)
- `GET /api/skills/sources/:id/revisions` — List source revisions (scope: `skills:r`)
- `POST /api/skills/sources/:id/snapshots` — Push a local skill package snapshot (scope: `skills:rw`)
- `POST /api/skills/sources/:id/sync` — Pull and sync a public GitHub/GitLab skill package (scope: `skills:rw`)
- `POST /api/skills/compile` — Compile/recompile one version, or backfill all active versions (scope: `skills:rw`)
- `GET /api/skills/catalog?query=&limit=&offset=` — List active L0 cards with stable pagination (scope: `skills:r`)
- `GET /api/skills/versions/:id/instructions` — Load L1 `SKILL.md` instructions (scope: `skills:r`)
- `GET /api/skills/versions/:id/resources?limit=&offset=` — Load the paginated L2 resource manifest without content (scope: `skills:r`)
- `GET /api/skills/versions/:id/resources/:file_id` — Load one selected text resource (scope: `skills:r`)
- `POST /api/skills/index` — Index compiled active skill cards into retrieval (scope: `skills:rw`)
- `POST /api/skills/versions/:id/quality-runs` — Run offline deterministic quality checks with an optional `baseline_version_id` (scope: `skills:rw`)
- `GET /api/skills/versions/:id/quality-runs?limit=&offset=` — List report-free quality run summaries with stable pagination (scope: `skills:r`)
- `GET /api/skills/quality-runs/:run_id` — Get one account-scoped full quality report (scope: `skills:r`)
- `POST /api/skills/search` — Semantic search across indexed L0 cards; `include_content` remains supported (scope: `skills:r`)
- `GET /api/skills/versions/active?skill_name=` — Get active skill version (scope: `skills:r`)
- `GET /api/skills/versions/:id/files` — List internal package file records (compatibility endpoint, scope: `skills:r`)
- `POST /api/skills/versions/:id/activate` — Activate a skill version (scope: `skills:rw`)

Successful local/Git ingest, direct publish, and activation attempt to refresh the compiled artifact after the package transaction commits. A compiler failure never rolls back package identity. Catalog reads return a basic card when an artifact is missing; call `/api/skills/compile` to backfill it. Instruction and resource-content responses use `Cache-Control: private, no-store`.

Migration `000018` replaces any pre-compiler Skill retrieval document that may contain full
instructions with a bounded basic L0 card, marks its stale vector as non-hydratable, and keeps
a safe PostgreSQL lexical fallback. Run `POST /api/skills/compile` and then
`POST /api/skills/index` after upgrading to rebuild current artifacts and embeddings.
`include_content=true` remains compatible by loading the selected L1 instructions from
PostgreSQL after search; instructions are never stored in the retrieval index.

Lexical retrieval matches a bigram projection (`retrieval_documents.lexical_text`, migration
`000023`) instead of raw text, because PostgreSQL's `simple` configuration does not segment CJK
script and would otherwise collapse a whole Chinese sentence into one unmatchable token. CJK runs
become overlapping character bigrams while ASCII runs stay whole lowercased words, so identifiers
keep exact matching. Index side and query side share one Go implementation
(`retrieval.LexicalProjection` / `retrieval.LexicalTSQuery`); that shared rule is the correctness
condition of the scheme. Rows written outside the Go write path (for example by an earlier
migration's direct UPDATE) have an empty projection and are invisible to the lexical leg — repair
them with `POST /api/admin/retrieval/lexical/rebuild`, which recomputes the projection from stored
title and content without re-embedding anything or calling Qdrant.

Phase 4 quality runs are synchronous, offline, and side-effect free except for their own audit row.
Each run reads its version, optional same-skill baseline, compiled artifacts, files, cutoff, and latest
version-bound logs from one read-only repeatable-read snapshot. `skill_log_add` accepts an optional immutable `skill_version_id`; when present it must belong to the
same account and `skill_name`, and the server canonicalizes the legacy version label. Logs without
that ID remain unassigned and are excluded from quality telemetry. Reports use at most the latest
200 logs before a fixed cutoff, require 20 triggered samples for suggestions, and contain counts,
fingerprints, and log IDs rather than instruction, resource, or log bodies. Direct body-only versions
validate `sha256(content) == content_hash == package_hash`. Each check carries a deterministic
`blocker`, `error`, or `warning` severity. Release comparison reports both `package_hash_changed`
and `resource_manifest_changed`; the latter compares static resource behavior metadata such as
kind, MIME type, indexability, and text availability without changing the Phase 1 package hash.
Quality-run list responses contain summaries only; the detail endpoint loads the full report.

Migration `000019` keeps audit targets append-only: deleting a target version referenced by a
version-bound log or quality run is rejected by the default `NO ACTION` foreign keys. Account
deletion still cascades quality runs, while deleting a baseline version only clears the
`baseline_version_id` column.

Skill sources keep registry metadata and deterministic file snapshots. Git sources are registered as server-pull sources; local sources are client-push sources where the client sends a package snapshot. `SKILL.md` remains the compatibility content for `skill_versions`, while additional files are tracked as revision file metadata and indexable text snapshots.

```bash
# Register a local source.
curl -X POST http://localhost:26001/api/skills/sources \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "personal-domain-web",
    "type": "local",
    "repository_url": "file:///Users/me/.agents/skills",
    "package_path": "domain-web"
  }'

# Push a local snapshot. Omit sha256/package_hash to let the server derive them
# from supplied text content.
curl -X POST http://localhost:26001/api/skills/sources/<source_id>/snapshots \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{
    "activate": true,
    "index": true,
    "files": [
      {
        "path": "SKILL.md",
        "mime_type": "text/markdown",
        "content": "---\nname: domain-web\ndescription: Build web services\n---\n\n# Instructions\n..."
      }
    ]
  }'
```

### Knowledge (authenticated)

Knowledge Registry K1: knowledge sources with immutable revisions and document
snapshots. K1 covers source registration, Git/local ingest, canonical package
identity, and account-scoped document reads. K2 adds the K0 catalog,
deterministic Markdown chunking, a document link graph, and account-scoped
hybrid retrieval. K3.1–K3.2 add the wiki compiler: profile versioning, immutable
wiki builds, deterministic checks as the only activation gate, build diff and
rollback. Still planned: asynchronous compilation, incremental builds, wiki-page
retrieval, lint, LLM review and validation signals (see
`docs/knowledge-wiki-compiler-k3-v0.1.md` §13 for exactly what is and is not
implemented).

Skill and knowledge packages can be organised by domain inside a repository
(`platform/retrieval`, `product/faq`). The owning domain is derived from the
first `package_path` segment and stored on the source; a single-segment path has
no domain, since a flat package is not organised by domain. The source name is
derived from every path segment (`platform/retrieval` → `platform-retrieval`)
because knowledge sources are unique per `(account_id, name)`, so a
basename-derived name would let same-named packages under different domains
overwrite each other. Domain is never accepted from the client.

Registration upserts by name, so a second registration whose `package_path`
derives the same name but points at a different package is rejected rather than
silently repointing the existing source. Register such a package under an
explicit distinct `name`.

- `POST /api/knowledge/sources` — Register or upsert a knowledge source by `name` (`git` or `local`) (scope: `knowledge:rw`)
- `GET /api/knowledge/sources` — List knowledge sources (scope: `knowledge:r`)
- `GET /api/knowledge/sources/:id/revisions` — List immutable source revisions (scope: `knowledge:r`)
- `POST /api/knowledge/sources/:id/sync` — Pull and ingest a public GitHub/GitLab knowledge package (scope: `knowledge:rw`)
- `POST /api/knowledge/sources/:id/snapshots` — Push a local knowledge package snapshot (scope: `knowledge:rw`)
- `GET /api/knowledge/revisions/:id/documents?limit=&offset=` — Paginated document metadata without content bodies (scope: `knowledge:r`)
- `GET /api/knowledge/revisions/:id/documents/:doc_id` — One document including its text content snapshot, served `Cache-Control: private, no-store` (scope: `knowledge:r`)
- `GET /api/knowledge/catalog?query=&domain=&limit=&offset=` — K0 collection cards for sources with an active revision: manifest metadata (name/description/profile/language/citation_policy), owning domain, document count, package hash, and chunk index status; stable pagination with ILIKE-style name/description filtering plus exact `domain` filtering. The response also carries `domains`, the account's domain roster with collection counts, so a domain can be chosen before reading individual cards (scope: `knowledge:r`)
- `POST /api/knowledge/index` — Chunk-index active revisions (body `source_id` optional; empty indexes every active source) into the account-scoped `knowledge` retrieval namespace and rebuild the document link graph (scope: `knowledge:rw`)
- `POST /api/knowledge/search` — Hybrid lexical + semantic search over indexed chunks (body `query`/`top_k`/`domain`/`source_ids`/`include_content`; `domain` resolves to that domain's sources and intersects with `source_ids`, so it can only narrow the search); hits carry document/source/revision provenance, heading path, score, snippet, and 1-hop link neighbors (metadata only, capped at 16); the snippet is the first 240 runes of the chunk body (a chunk shorter than that is fully visible in its snippet), and the full chunk body is returned only with `include_content=true`; served `Cache-Control: private, no-store` (scope: `knowledge:r`)
- `GET /api/knowledge/documents/:doc_id/links?limit=&offset=` — Both directions of one document's package-internal links: outgoing links keep the target path (with a NULL document ID when dangling), incoming links carry the linking document's path (scope: `knowledge:r`)

#### Wiki compiler (K3)

The wiki is compiled on the platform, not written back to Git by an agent: the
agent runs on the client and is not controllable, so making it the author would
make wiki quality depend on whichever client happened to run. Git holds only the
raw sources; the wiki is a platform artifact.

A build is **not reproducible** — the same sources compiled twice yield different
text. Everything else follows from that: builds are immutable and retained,
provenance is complete (raw revision and package hash, profile version, compiler
version, prompt version, model, reviewer independence), reuse keys on *input*
identity rather than a content hash, and content hashes are used only for diffing.

Activation is **automatic**. The knowledge base belongs to the user while the
quality standard belongs to the platform, so asking a user to approve compiler
output is an identity mismatch that yields either rubber stamping or a wiki that
never updates. What makes that safe is `check`: deterministic, machine-decidable
invariants (citations resolve to real documents, links close inside the build,
page kinds and link types are allowed by the profile, the index covers every page,
paths are unique, page count has not drifted implausibly from the parent). check
is the **only** gate — an LLM reviewer is deliberately kept off the blocking path,
because an unreproducible verdict there means a retried build passes or fails at
random. A build failing check writes no pages at all: half a wiki is worse than
none, since an agent cannot tell pages are missing.

`wiki/index.md` and `wiki/log.md` are generated by the platform, not the model.
If the model wrote the index, check's coverage rule would be testing the model's
diligence instead of the build's completeness. Those two paths are reserved; a
model page landing there is dropped and recorded as a `page_rejected` event.

- `POST /api/knowledge/compile` — Compile a source's active raw revision into a wiki build; runs check and activates on pass. Body `source_id` (required), `mode` (`full`; `incremental` is rejected rather than silently downgraded), `force` (recompile despite a matching input identity), `activate` (default true). Returns the build, whether it was reused, whether it activated, and warnings — including a standing warning when reviewer independence is not `cross_provider`. Returns 501 when no compiler model is configured, which is an operator gap rather than a failed build (scope: `knowledge:rw`)
- `GET /api/knowledge/builds?source_id=&limit=&offset=` — Build history, newest first, with `is_active` derived from the source pointer (scope: `knowledge:r`)
- `GET /api/knowledge/builds/:build_id` — One build with full provenance, check verdict and failures, and token spend (scope: `knowledge:r`)
- `GET /api/knowledge/builds/:build_id/pages` — Page metadata without bodies (scope: `knowledge:r`)
- `GET /api/knowledge/builds/:build_id/pages/*path` — One page with its body, citations and both inbound and outbound typed links; served `Cache-Control: private, no-store` (scope: `knowledge:r`)
- `GET /api/knowledge/builds/:build_id/diff?from=` — Compare two builds by page path and content hash; `from` defaults to the previous succeeded build of the same source (scope: `knowledge:r`)
- `GET /api/knowledge/builds/:build_id/events` — The ordered build log, rendered as `wiki/log.md` on succeeded builds (scope: `knowledge:r`)
- `POST /api/knowledge/builds/:build_id/activate` — Point the source's wiki at this build. This is also the rollback operation; the response carries `previous_build_id` so a rollback can be undone. Returns 409 for a build that did not succeed or did not pass check (scope: `knowledge:rw`)

Compilation is currently **synchronous and slow** — 200–400 seconds per package
against a reasoning model — which is beyond any sane HTTP client default timeout.
If the client hangs up, the build is recorded as `cancelled` rather than left
stuck in `running`. Asynchronous compilation with leases is the next milestone.

Model configuration (both roles fall back to `EMBEDDING_BASE_URL` /
`EMBEDDING_API_KEY`, which is how a single-credential deployment starts):

| Variable | Default | Notes |
|---|---|---|
| `COMPILER_BASE_URL` / `COMPILER_API_KEY` | embedding endpoint and key | OpenAI-compatible `/chat/completions` |
| `COMPILER_MODEL` | `qwen3.7-plus` | |
| `COMPILER_TEMPERATURE` | `0.2` | As reproducible as an LLM allows |
| `COMPILER_MAX_TOKENS` | `32768` | A reasoning model spends this budget on its own thinking before writing, so it must be a large multiple of the expected wiki size; a truncated reply fails the build rather than yielding a partial wiki |
| `COMPILER_TIMEOUT_SECONDS` | `900` | |
| `REVIEWER_BASE_URL` / `REVIEWER_API_KEY` | embedding endpoint and key | Point at another vendor for real independence |
| `REVIEWER_MODEL` | `qwen-max` | Different from the compiler by default, so review is at least not literal self-review |
| `REVIEWER_TEMPERATURE` | `0` | The same claim and source should get the same verdict |

Reviewer independence is classified per build as `cross_provider`,
`same_provider`, `same_model` or `unavailable`, and stored on the build. A
heterogeneous reviewer reduces correlated blind spots but does not create
impartiality — mainstream models share training data. The genuinely independent
anchors are `check` (mechanical) and human validation, not another model.

Every knowledge package must carry a root `KNOWLEDGE.yaml` manifest
(`name` required; optional `description`, `profile`, `language`,
`include`/`exclude` glob lists, and `citation_policy: required|optional`).
The manifest's include/exclude rules select which files become documents;
`KNOWLEDGE.yaml` itself always participates in the package identity hash but
is never returned as a document. Text files keep a stored content snapshot;
binary files contribute only hash and size to identity. Ingest is
transactional and idempotent: replaying the same package hash returns the
existing revision and re-targets the source's `active_revision_id`; a failed
sync marks the source `error` without leaving a partial revision.

```bash
# Register a Git knowledge source.
curl -X POST http://localhost:26001/api/knowledge/sources \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "product-support",
    "type": "git",
    "repository_url": "https://github.com/acme/knowledge",
    "package_path": "product-support"
  }'

# Sync a ref to an immutable commit and ingest the package.
curl -X POST http://localhost:26001/api/knowledge/sources/<source_id>/sync \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"ref": "main"}'
```

## Authentication

### JWT

```bash
# Login to get token
curl -X POST http://localhost:26001/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "user@example.com", "password": "password123"}'

# Use token
curl -H "Authorization: Bearer <jwt_token>" http://localhost:26001/api/todos
```

### API Key

```bash
# Via x-api-key header
curl -H "x-api-key: ak_xxxx" http://localhost:26001/api/todos

# Via Authorization header
curl -H "Authorization: Bearer ak_xxxx" http://localhost:26001/api/todos
```

### Scopes Example

```bash
# Create a read-only key for todos
curl -X POST http://localhost:26001/api/auth/apikeys \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"name": "readonly-agent", "scopes": ["todos:r", "notes:r"]}'

# This key can list/get but cannot create/update/delete
curl -H "x-api-key: ak_xxxx" http://localhost:26001/api/todos        # ✓ 200
curl -X POST -H "x-api-key: ak_xxxx" http://localhost:26001/api/todos # ✗ 403 insufficient scope
```

## MCP Integration

Each business module mounts its own Streamable HTTP MCP server, rather than a
single aggregated endpoint. This keeps tool lists focused and lets an agent
integration opt into only the modules it needs.

| Endpoint | Tools |
|----------|-------|
| `POST /mcp/todos` | `todo_create`, `todo_get`, `todo_list`, `todo_update`, `todo_delete`, `todo_search` |
| `POST /mcp/notes` | `note_create`, `note_get`, `note_list`, `note_update`, `note_append`, `note_delete`, `note_search` |
| `POST /mcp/reports` | `report_create`, `report_get`, `report_list`, `report_list_sources`, `report_update`, `report_delete` |
| `POST /mcp/bookmarks` | `bookmark_create`, `bookmark_get`, `bookmark_list`, `bookmark_update`, `bookmark_delete` |
| `POST /mcp/expenses` | `expense_create`, `expense_get`, `expense_list`, `expense_summary`, `expense_update`, `expense_delete` |
| `POST /mcp/memory` | `memory_record`, `memory_store`, `memory_search`, `memory_get`, `memory_timeline`, `memory_attribution`, `memory_supersede`, `memory_feedback`, `memory_feedback_list`, `memory_checkpoint_save`, `memory_resume` |
| `POST /mcp/skills` | `skill_log_add`, `skill_logs_list`, `skill_version_publish`, `skill_version_get_active`, `skill_source_sync`, `skill_stats`, `skill_signals`, `skill_search`, `skill_index_active`, `skill_catalog_list`, `skill_compile`, `skill_version_instructions`, `skill_version_resources`, `skill_resource_get`, `skill_quality_run`, `skill_quality_get` |
| `POST /mcp/context` | `context_pack` |
| `POST /mcp/knowledge` | `knowledge_sources_list`, `knowledge_source_sync`, `knowledge_documents_list`, `knowledge_document_get`, `knowledge_catalog_list`, `knowledge_search`, `knowledge_index_active`, `knowledge_document_links`, `knowledge_compile`, `knowledge_builds_list`, `knowledge_build_get`, `knowledge_build_pages`, `knowledge_page_get`, `knowledge_build_diff`, `knowledge_build_events`, `knowledge_build_activate` |

Authenticate with a valid API key via `X-Api-Key` header, `Authorization: Bearer ak_xxx`,
or `?api_key=ak_xxx` query param. MCP tool calls enforce the same API key scopes as REST
(e.g. `todo_create` requires `todos:rw`, `todo_list` requires `todos:r`).

### Connecting an MCP client

Point the client at the module endpoint it needs, e.g. for todos only:

```json
{
  "mcpServers": {
    "agentmate-todos": {
      "url": "http://localhost:26001/mcp/todos",
      "headers": { "X-Api-Key": "ak_xxxx" }
    }
  }
}
```

To use multiple modules, add one entry per endpoint (each is an independent
MCP server with its own tool list):

```json
{
  "mcpServers": {
    "agentmate-todos": {
      "url": "http://localhost:26001/mcp/todos",
      "headers": { "X-Api-Key": "ak_xxxx" }
    },
    "agentmate-notes": {
      "url": "http://localhost:26001/mcp/notes",
      "headers": { "X-Api-Key": "ak_xxxx" }
    },
    "agentmate-skills": {
      "url": "http://localhost:26001/mcp/skills",
      "headers": { "X-Api-Key": "ak_xxxx" }
    }
  }
}
```

### Quick test (JSON-RPC over HTTP)

```bash
curl -X POST http://localhost:26001/mcp/todos \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "X-Api-Key: ak_xxxx" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

## Roadmap

API Key → OAuth Device Flow → Agent DID

1. **API Key** (current) — Simple key-based auth with scopes for agent integration
2. **OAuth Device Flow** — Enable headless agents to authenticate via user-approved device codes
3. **Agent DID** — Decentralized identity for agents, enabling cross-platform trust and verifiable agent credentials
