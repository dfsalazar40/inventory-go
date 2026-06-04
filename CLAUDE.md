<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan at
`specs/001-stock-reservation/plan.md` (and its sibling `research.md`,
`data-model.md`, `contracts/openapi.yaml`, `quickstart.md`).

## Active Technologies

- Backend: Go 1.24+ with chi (router), pgx/v5 (Postgres), gorilla/websocket, golang-migrate
- Database: PostgreSQL 16+ — all concurrency correctness enforced here via atomic conditional writes
- Frontend: React 19 + Vite 6 + TypeScript 5 on Node 22; Vitest + React Testing Library
- Packaging: Docker Compose brings up db + backend + frontend, seeded, in one command

## Project Structure

- `backend/` — cmd/server, internal/{domain,store,api,realtime,ttl}, migrations, seed
- `frontend/` — src/{api,hooks,components,lib}
- `specs/001-stock-reservation/` — spec, plan, research, data-model, contracts, tasks

## Key Constraints

- Zero over-sell: `available = total_stock - reserved` never negative; one atomic conditional UPDATE
- Two-phase reservations: PENDING hold -> CONFIRMED (no TTL) | RELEASED | EXPIRED (stock returns once)
- Idempotency-Key required on reserve (frontend-generated; 400 if missing); release is a safe no-op
- Strict TDD on concurrency + idempotency critical paths
<!-- SPECKIT END -->
