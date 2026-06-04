# Implementation Plan: Stock Reservation System

**Branch**: `main` (single-branch workflow) | **Date**: 2026-06-03 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-stock-reservation/spec.md`

## Summary

A high-concurrency flash-sale stock reservation system. Shoppers create temporary **PENDING**
holds ("Reserve Item"), then **Confirm** them into final reservations or let them expire/release.
The core promise is **zero over-sell under arbitrary concurrent load**, enforced by a single
atomic conditional `UPDATE` at the PostgreSQL layer (no read-then-write, no app-level locks).
Reserve/confirm/release are idempotent via a frontend-generated `Idempotency-Key`. A 60s TTL sweeper
(plus lazy expiration on read) frees abandoned holds, with a configurable `RESET_TTL_ON_ADD`
behavior. All clients stay in sync over WebSockets with reconnection + state reconciliation. The
whole stack (Postgres + Go backend + React frontend) runs with a single `docker compose up`.

## Technical Context

**Language/Version**: Go 1.24+ (backend), TypeScript 5.x / Node 22 (frontend)

**Primary Dependencies**: Go — `chi` (router), `pgx/v5` (Postgres driver + pool), `gorilla/websocket`
(realtime), `golang-migrate` (schema migrations). Frontend — React 19, Vite 6, native `WebSocket` +
`fetch`, `vitest` + React Testing Library.

**Storage**: PostgreSQL 16+. All correctness guarantees (anti-oversell, exactly-once stock return,
idempotency) live in the database via atomic conditional writes and unique constraints.

**Testing**: Go `testing` + goroutine/`sync.WaitGroup` concurrency harness against a real Postgres
(Docker Compose service or `testcontainers-go`); Vitest + RTL for the frontend timer, reserve
happy-path, and error-state component tests.

**Target Platform**: Linux containers (Docker Compose) — Postgres, Go API, static React build.

**Project Type**: Web application (separate `backend/` and `frontend/`).

**Performance Goals**: Correct and stable under the mandated adversarial load — ≥50 concurrent
requests for the last unit and 100 concurrent requests for 10 units. Stock-change events propagate
to connected clients within ~1s; TTL expiration frees stock within ~1s of elapse.

**Constraints**: `available = total − reserved` MUST never go negative under any interleaving.
Every reserved-stock mutation is a single atomic statement. Idempotency-Key required on reserve
(400 if missing). Single-command dockerized startup with seed data. Complete OpenAPI contract.

**Scale/Scope**: A realistic single-instance flash sale (one Postgres). The backend is
horizontally scalable because all correctness lives in the DB; multi-region is out of scope.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Status | How this plan satisfies it |
|-----------|--------|----------------------------|
| **I. Architecture-First** | ✅ PASS | Commit order is constitution → spec → clarify → plan → tasks → code. This plan precedes any application code. |
| **II. Correctness Under Concurrency** | ✅ PASS | Single atomic conditional `UPDATE items SET reserved = reserved + $q WHERE total_stock - reserved >= $q` gates every hold; stock return uses a conditional state transition so units return exactly once. Adversarial load tests (50+ last-unit, 100→10) are mandated in tasks. |
| **III. Idempotency by Design** | ✅ PASS | `POST /reservations` honors a required `Idempotency-Key` (same key+payload → same reservation, one decrement; key+different payload → 409). `DELETE /reservations/:id` is a safe repeatable no-op returning stock at most once. **Confirm is additive and also idempotent** (re-confirm = no-op), consistent with — not a deviation from — Principle III. |
| **IV. Test-First on Critical Paths** | ✅ PASS | Concurrency + idempotency test suites are first-class tasks committed with/before the code they cover (Strict TDD). Frontend timer unit + reserve happy-path + error-state tests required. |
| **V. Synced, Observable State** | ✅ PASS | WebSocket hub broadcasts stock/reservation/expiration events; client reconnects and reconciles from a REST snapshot. All async actions expose loading/success/error + conflict messaging. |
| **VI. Traceability & Clean History** | ✅ PASS | Tasks map 1:1 to implementation; conventional commits in workflow order; assumptions/pivots logged in `spec-kit-notes.md`. |
| **VII. Simplicity & Latest Stable Tooling** | ✅ PASS | Latest stable Go/React/Postgres; no abstraction beyond a thin store/handler split; one `docker compose up` brings up the full seeded stack. |

**Scope note (not a violation)**: The two-phase model (Confirm) was added via an authoritative
client clarification and amended into `spec.md` *before* this plan, honoring Principle I ("a change
in scope MUST start by amending the spec, not the code"). The constitution's idempotency clause
names reserve/release explicitly; the Confirm endpoint extends the same idempotency discipline and
introduces no principle conflict, so no constitution amendment/version bump is required.

**Gate result**: PASS — no violations. Complexity Tracking left empty.

## Project Structure

### Documentation (this feature)

```text
specs/001-stock-reservation/
├── plan.md              # This file (/speckit-plan output)
├── research.md          # Phase 0 output — technical decisions & rationale
├── data-model.md        # Phase 1 output — entities, schema, state transitions
├── quickstart.md        # Phase 1 output — how to run & test the stack
├── contracts/
│   └── openapi.yaml     # Phase 1 output — full REST API contract
├── checklists/
│   └── requirements.md  # Spec quality checklist (from /specify + /clarify)
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
backend/
├── cmd/server/main.go            # wiring: config, pgx pool, router, ws hub, ttl sweeper
├── internal/
│   ├── domain/                   # entities (Item, Reservation), status enum, errors
│   ├── store/                    # pgx repositories + the atomic conditional SQL
│   ├── api/                      # chi handlers + middleware (idempotency, user-id, recovery)
│   ├── realtime/                 # websocket hub (register/unregister/broadcast)
│   └── ttl/                      # background expiration sweeper + lazy expiration
├── migrations/                   # golang-migrate SQL (schema + indexes + constraints)
├── seed/                         # seed inventory (catalog from the visual reference)
├── openapi.yaml                  # served + source of the contract
└── *_test.go                     # concurrency, idempotency, ttl integration tests

frontend/
├── src/
│   ├── api/                      # REST client (sends Idempotency-Key, X-User-Id)
│   ├── hooks/                    # useWebSocket, useReservations, useCountdown
│   ├── components/               # InventoryDashboard, ItemCard, ReservationPanel, etc.
│   ├── lib/                      # userId (browser UUID, ~1d TTL), idempotency key gen
│   └── main.tsx
└── src/**/*.test.tsx             # timer unit, reserve happy-path, error-state component tests

docker-compose.yml                # db + backend + frontend, one command, seeded
```

**Structure Decision**: Web application layout (Option 2). The backend follows a thin
domain/store/api split — `store` owns the atomic SQL that is the heart of Principle II, `api` owns
HTTP concerns (idempotency middleware, validation, error mapping), `realtime` and `ttl` are
isolated concerns wired in `main.go`. This keeps correctness logic in one auditable place without
over-abstracting (Principle VII).

## Complexity Tracking

> No constitution violations. No entries required.
