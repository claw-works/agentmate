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

### Docker Compose (recommended)

```bash
docker compose up --build
# Server runs at http://localhost:26001
```

### Manual

```bash
# 1. Start PostgreSQL
docker run -d --name agentmate-pg -p 5432:5432 \
  -e POSTGRES_USER=agentmate -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=agentmate \
  postgres:16

# 2. Run migrations
migrate -path migrations -database "postgres://agentmate:secret@localhost:5432/agentmate?sslmode=disable" up

# 3. Start server
cp .env.example .env
go run ./cmd/server

# Server runs at http://localhost:26001
```

## API Endpoints

### Auth (public)
- `POST /auth/register` — Register a new user
- `POST /auth/login` — Login, returns JWT

### Auth (authenticated)
- `GET /auth/me` — Current user info
- `POST /auth/apikeys` — Create API Key (accepts optional `scopes` field)
- `GET /auth/apikeys` — List API Keys
- `DELETE /auth/apikeys/:id` — Delete API Key

#### Create API Key with Scopes

```bash
curl -X POST http://localhost:26001/auth/apikeys \
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
| `skills:r` | Read skill logs and versions |
| `skills:rw` | Read & write skill logs and versions (implies `skills:r`) |
| `manage_keys` | Create/delete API keys |

Empty scopes array `[]` means **full access**.

### Todos (authenticated)
- `POST /todos` — Create (scope: `todos:rw`)
- `GET /todos` — List (scope: `todos:r`)
- `GET /todos/search?q=` — Search (scope: `todos:r`)
- `GET /todos/:id` — Get by ID (scope: `todos:r`)
- `PATCH /todos/:id` — Update (scope: `todos:rw`)
- `DELETE /todos/:id` — Delete (scope: `todos:rw`)

### Notes (authenticated)
- `POST /notes` — Create (scope: `notes:rw`)
- `GET /notes` — List (scope: `notes:r`)
- `GET /notes/search?q=` — Search (scope: `notes:r`)
- `GET /notes/:id` — Get by ID (scope: `notes:r`)
- `PATCH /notes/:id` — Update (scope: `notes:rw`)
- `DELETE /notes/:id` — Delete (scope: `notes:rw`)

## Authentication

### JWT

```bash
# Login to get token
curl -X POST http://localhost:26001/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "user@example.com", "password": "password123"}'

# Use token
curl -H "Authorization: Bearer <jwt_token>" http://localhost:26001/todos
```

### API Key

```bash
# Via x-api-key header
curl -H "x-api-key: ak_xxxx" http://localhost:26001/todos

# Via Authorization header
curl -H "Authorization: Bearer ak_xxxx" http://localhost:26001/todos
```

### Scopes Example

```bash
# Create a read-only key for todos
curl -X POST http://localhost:26001/auth/apikeys \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"name": "readonly-agent", "scopes": ["todos:r", "notes:r"]}'

# This key can list/get but cannot create/update/delete
curl -H "x-api-key: ak_xxxx" http://localhost:26001/todos        # ✓ 200
curl -X POST -H "x-api-key: ak_xxxx" http://localhost:26001/todos # ✗ 403 insufficient scope
```

## MCP Integration

The MCP servers run over Streamable HTTP on the main API port:

- `POST /mcp` — todos, notes, reports, bookmarks, expenses
- `POST /mcp/skills` — skill logs and skill versions

Authenticate with a valid API key via `X-Api-Key`, `Authorization: Bearer ak_xxx`, or `?api_key=ak_xxx`. MCP tool calls enforce the same API key scopes as REST.

Available `/mcp` tools:
- `todo_create`, `todo_list`, `todo_get`, `todo_update`, `todo_delete`, `todo_search`
- `note_create`, `note_list`, `note_get`, `note_update`, `note_delete`, `note_search`, `note_append`
- `report_create`, `report_list`, `report_get`, `report_update`, `report_delete`, `report_search`
- `bookmark_save`, `bookmark_list`, `bookmark_get`, `bookmark_update`, `bookmark_mark_read`, `bookmark_delete`, `bookmark_search`
- `expense_add`, `expense_list`, `expense_summary`, `expense_search`, `expense_update`, `expense_delete`

Available `/mcp/skills` tools:
- `skill_log_add`, `skill_logs_list`, `skill_version_publish`, `skill_version_get_active`, `skill_stats`, `skill_signals`

## Roadmap

API Key → OAuth Device Flow → Agent DID

1. **API Key** (current) — Simple key-based auth with scopes for agent integration
2. **OAuth Device Flow** — Enable headless agents to authenticate via user-approved device codes
3. **Agent DID** — Decentralized identity for agents, enabling cross-platform trust and verifiable agent credentials
