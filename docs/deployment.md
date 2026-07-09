# AgentMate Deployment Guide

> **Note**: This file is listed in `.gitignore` when it contains real credentials.
> Copy this template and fill in your own values locally.

## Service Info

| Item | Value |
|------|-------|
| API Server | `http://localhost:26001` |
| Admin Dashboard | `http://localhost:26001/admin` |
| PostgreSQL (external) | `localhost:15432` |
| Deploy Method | `docker compose` |

## Quick Start

```bash
cd /path/to/agentmate

# 1. Copy and fill in environment variables
cp .env.example .env
# Edit .env: set DATABASE_URL, JWT_SECRET

# 2. Start all services (postgres + migrate + server)
docker compose up -d

# 3. Verify
curl http://localhost:26001/auth/me
# → 401 unauthorized (server is up)
```

## Operations

```bash
# Start
docker compose up -d

# Stop
docker compose down

# Rebuild after code changes
git pull && docker compose up --build -d

# View server logs
docker compose logs -f server

# Run migrations manually (if needed)
docker compose run --rm migrate

# Restart server only (no rebuild)
docker compose restart server
```

## Account Setup

After first deploy, register an admin account:

```bash
# Register
curl -X POST http://localhost:26001/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"your-password"}'

# Promote to admin (run in postgres container)
docker exec agentmate-postgres psql -U agentmate -d agentmate \
  -c "UPDATE users SET role='admin' WHERE email='admin@example.com';"

# Login and get JWT
curl -X POST http://localhost:26001/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"your-password"}'

# Create API Keys for each Agent
curl -X POST http://localhost:26001/auth/apikeys \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-agent","scopes":[]}'
```

## API Keys

Store your API keys locally in `~/.agentmate/keys.json` (never commit):

```json
{
  "service": "AgentMate",
  "base_url": "http://localhost:26001",
  "accounts": {
    "my-agent": {
      "key_id": "<uuid>",
      "key": "ak_<your-key>",
      "scopes": []
    }
  }
}
```

```bash
chmod 700 ~/.agentmate
chmod 600 ~/.agentmate/keys.json
```

## Architecture

```
External Agents (REST / MCP)
         ↓
  Gin API Server :26001
         ↓
  PostgreSQL :15432 (internal: 5432)
```

## Modules

| Module | Endpoints | Notes |
|--------|-----------|-------|
| Auth | `/auth/*` | JWT + API Key, Scopes, Roles |
| Todos | `/todos` | CRUD + tag filter + search |
| Notes | `/notes` | CRUD + tag filter + search |
| Reports | `/reports` | CRUD + source/tag filter + full-text search |
| Bookmarks | `/bookmarks` | CRUD + read status + tag/search filters |
| Expenses | `/expenses` | CRUD + summary + tag/date filters |
| Skills | `/skills` | Skill logs, versions, stats, signals, semantic search |
| Tags | `/tags` | Aggregated tag counts across modules |
| Admin | `/admin/*` | Stats, user management, API key management, usage |
| MCP | `/mcp`, `/mcp/skills` | Streamable HTTP tool endpoints using API key auth and scopes |

## Database Migrations

Migrations run automatically on `docker compose up` via the `migrate` service.
Migration files are in `./migrations/`, numbered sequentially.

Current schema version: see `schema_migrations` table.

## Update History

| Date | Commit | Changes |
|------|--------|---------|
| 2026-05-18 | initial | Initial deployment |
| 2026-05-18 | — | Add Admin Dashboard, role-based auth |
| 2026-05-18 | — | Add CORS support |
| 2026-05-19 | — | Add Reports module (migration 000007) |
| 2026-05-19 | — | Add GIN indexes, tag filters, /tags endpoint (migration 000008) |
| 2026-05-20 | — | Add API call logging (migration 000009) |
