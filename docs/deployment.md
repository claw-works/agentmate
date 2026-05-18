# AgentMate Deployment

## Service Info
- **Base URL**: http://localhost:26001
- **Deploy Method**: docker compose
- **Database**: PostgreSQL (container: agentmate-postgres)
- **Port**: 26001

## Quick Start
```bash
cd /Users/wellxie/projects/claw-works/agentmate

# Start all services
docker compose up -d

# Stop all services
docker compose down

# Rebuild and restart (after code changes)
git pull && docker compose up --build -d

# View logs
docker compose logs -f server
```

## Accounts
API Keys are stored in `~/.agentmate/keys.json` (not committed to git).

| Username | Role |
|----------|------|
| wellxie | Main account (human) |
| amazon-quick | Amazon Quick Agent |
| claude-code | Claude Code Agent |
| kiro | Kiro Agent |

## Update History
| Date | Commit | Changes |
|------|--------|---------|
| 2026-05-18 | initial | Initial deployment, 7 todos migrated from SQLite |
