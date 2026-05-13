# Agentmate

AI-native 工具服务平台（Backend as Toolset）。纯 API 产品，无 UI。任意外部 Agent 可通过 REST API 或 MCP 接入。多用户 SaaS，按高并发标准设计。

## 架构

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

## 技术栈

- Go 1.22+, Gin, pgx v5, sqlc, golang-migrate
- 认证：JWT + API Key 双轨
- MCP：mark3labs/mcp-go

## 快速开始

```bash
# 1. 启动 PostgreSQL
docker run -d --name agentmate-pg -p 5432:5432 \
  -e POSTGRES_USER=agentmate -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=agentmate \
  postgres:16

# 2. 运行迁移
migrate -path migrations -database "postgres://agentmate:secret@localhost:5432/agentmate?sslmode=disable" up

# 3. 启动服务
cp .env.example .env
go run ./cmd/server

# 服务运行在 http://localhost:8080
```

## API 端点

### Auth（公开）
- `POST /auth/register` — 注册
- `POST /auth/login` — 登录，返回 JWT

### Auth（需认证）
- `GET /auth/me` — 当前用户
- `POST /auth/apikeys` — 创建 API Key
- `GET /auth/apikeys` — 列出 API Keys
- `DELETE /auth/apikeys/:id` — 删除 API Key

### Todos（需认证）
- `POST /todos` — 创建
- `GET /todos` — 列表
- `GET /todos/search?q=` — 搜索
- `GET /todos/:id` — 获取
- `PATCH /todos/:id` — 更新
- `DELETE /todos/:id` — 删除

### Notes（需认证）
- `POST /notes` — 创建
- `GET /notes` — 列表
- `GET /notes/search?q=` — 搜索
- `GET /notes/:id` — 获取
- `PATCH /notes/:id` — 更新
- `DELETE /notes/:id` — 删除

## 认证方式

```bash
# JWT
curl -H "Authorization: Bearer <jwt_token>" http://localhost:8080/todos

# API Key (x-api-key)
curl -H "x-api-key: ak_xxxx" http://localhost:8080/todos

# API Key (Bearer)
curl -H "Authorization: Bearer ak_xxxx" http://localhost:8080/todos
```
