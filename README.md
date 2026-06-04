# Stock Reservation System

A high-concurrency flash-sale inventory service. Shoppers create temporary **PENDING** holds, then
**Confirm** them into final reservations or let them expire/release. The core guarantee:
`available = total_stock − reserved` **never goes negative under arbitrary concurrent load**,
enforced by a single atomic conditional `UPDATE` at the PostgreSQL layer.

---

## Run the whole stack

```bash
docker compose up --build
```

One command. Brings up Postgres 16, the Go backend, and the React frontend — seeded and ready.

| Service    | URL                          | Notes                                    |
|------------|------------------------------|------------------------------------------|
| Frontend   | http://localhost:5173        | React dashboard (nginx in Docker)        |
| Backend    | http://localhost:8080        | REST API + WebSocket at `/ws`            |
| OpenAPI    | http://localhost:8080/openapi.yaml | Full contract                      |
| Database   | localhost:5432               | PostgreSQL 16, migrations + seed applied |

Seed data (Vintage Camera, Mechanical Watch, Acoustic Guitar, Smart Flask, Running Shoes, Gaming
Mouse — varied stock, one out-of-stock item) loads automatically.

---

## Run the tests

### Backend

The backend integration tests use a real Postgres instance. They open a large connection pool per
package, so packages **must** run serially (`-p 1`) to avoid pool exhaustion and port contention.
The `-race` flag is required on concurrency-critical paths.

```bash
cd backend
TEST_DATABASE_URL=postgres://inventory:inventory@localhost:5432/inventory?sslmode=disable \
  go test -race -p 1 ./...
```

> **Why `-p 1`?** Each test package opens its own pgx pool (up to 110 connections). Running
> packages in parallel multiplies those pools and reliably exhausts the Postgres `max_connections`
> limit, causing spurious failures. `-p 1` keeps one package running at a time.

Key concurrency suites:

| Suite | What it proves |
|-------|----------------|
| Last-unit contention | 50+ goroutines compete for 1 unit → exactly 1 succeeds, stock never negative |
| Oversell | 100 goroutines for 10 units → exactly 10 succeed, 90 rejected |
| Reserve idempotency | Same key + payload in parallel → one reservation, one decrement |
| Release idempotency | Double release → stock returns once; release-after-expire → no-op |
| TTL | Pending expires and returns stock once; confirmed never expires |
| Confirm/expire race | TTL sweep and confirm fire simultaneously → exactly one outcome wins |

### Frontend

```bash
cd frontend
npm test
```

Covers: countdown timer unit test, reserve happy-path component test, error-state (conflict /
insufficient stock) component test.

---

## Concurrency strategy

### Zero over-sell: the atomic conditional UPDATE

Every reserve attempt goes through a single SQL statement — no read-then-write, no application lock:

```sql
UPDATE items
   SET reserved = reserved + $qty
 WHERE id = $item_id
   AND total_stock - reserved >= $qty
RETURNING id, total_stock, reserved;
```

Postgres evaluates the `WHERE` predicate and applies the increment atomically under row-level
locking. Two concurrent requests for the last unit serialize on that row; exactly one sees the
predicate satisfied. If `0` rows are affected, the hold is rejected with a typed
`insufficient_stock` or `conflict` error. The reservation row is then inserted in **the same
transaction**, so there is no window between decrementing the counter and recording the hold.

**Why at the DB layer?** Application-level locks (mutexes, Redis locks) add a failure mode and move
the source of truth out of the database. Serializable isolation adds retry storms under flash-sale
load. The conditional `UPDATE` achieves the correctness guarantee in one round-trip, with the
shortest possible lock hold time.

### Exactly-once stock return

Stock is returned by a conditional state transition that only the first writer of any release/expire
race can execute:

```sql
-- release path (expire path is identical with status = 'expired')
WITH released AS (
  UPDATE reservations
     SET status = 'released', released_at = now()
   WHERE id = $id AND status = 'pending'
  RETURNING item_id, quantity
)
UPDATE items i
   SET reserved = i.reserved - r.quantity
  FROM released r
 WHERE i.id = r.item_id;
```

The `WHERE status = 'pending'` predicate means the transition fires **at most once**. A double
release, a release-after-expiration, or a simultaneous TTL sweep all match 0 rows on the second
attempt — stock is returned exactly once regardless of the race.

### Idempotency

Each "Reserve Item" action in the frontend generates a fresh UUID sent as the `Idempotency-Key`
header. The backend atomically records the key alongside a hash of the request payload:

- **Same key + same payload** → return the existing reservation, no second decrement.
- **Same key + different payload** → `409 idempotency_key_conflict`.
- **Missing key** → `400`.

The `ON CONFLICT (key) DO NOTHING` insert resolves parallel duplicate-key requests at the DB layer
(spec US6 scenario 3).

---

## Architecture overview

### Backend (`backend/`)

```
cmd/server/main.go        — wiring: config, pool, router, hub, TTL sweeper
internal/
  domain/                 — entities (Item, Reservation), status enum, typed errors
  store/                  — pgx repositories; the atomic conditional SQL lives here
  api/                    — chi handlers, idempotency middleware, error mapping
  realtime/               — WebSocket hub (register/unregister/broadcast)
  ttl/                    — background expiration sweeper + lazy expiration on read
migrations/               — golang-migrate SQL (schema, indexes, constraints)
seed/                     — seed inventory SQL
openapi.yaml              — API contract (served at GET /openapi.yaml)
```

The `store` package owns the atomic SQL that is the heart of the correctness guarantee. The `api`
package owns HTTP concerns (validation, error mapping, idempotency header enforcement). `realtime`
and `ttl` are isolated concerns wired in `main.go`.

### Frontend (`frontend/src/`)

```
api/            — REST client (auto-attaches X-User-Id and Idempotency-Key)
hooks/          — useWebSocket, useReservations, useCountdown
components/     — InventoryDashboard, ItemCard, ReservationPanel
lib/            — userId (browser UUID, ~1-day TTL), idempotency key generator
```

### Two-phase reservation model

| Phase | Action | Stock effect | TTL |
|-------|--------|-------------|-----|
| Phase 1 | `POST /reservations` — creates **PENDING** hold | `reserved += qty` immediately | 60s countdown starts |
| Phase 2 | `POST /reservations/{id}/confirm` — transitions to **CONFIRMED** | none | TTL stops; hold is permanent |
| Release | `DELETE /reservations/{id}` | `reserved -= qty` exactly once | n/a |
| Expire | TTL sweeper runs the same conditional transition | `reserved -= qty` exactly once | n/a |

### Key design decisions

| Decision | What was chosen | Why |
|----------|----------------|-----|
| Anti-oversell gate | Single atomic conditional `UPDATE` at the DB layer | No application locks; proven correct under arbitrary interleaving |
| Stock accounting | Deducted at hold creation (PENDING), not at confirm | Prevent oversell during the pending window |
| Idempotency key source | Frontend-generated UUID per action | Server-generated keys cannot dedupe client retries |
| Confirm idempotency | By reservation state (re-confirm already-confirmed = 200 no-op) | No new key needed; the hold already carries the reserve key |
| TTL reset on add | `RESET_TTL_ON_ADD=true` default; per-user, per-item scope | Adds new hold resets countdown only for that item's pending holds |
| User identity | Browser-generated UUID, `localStorage`, ~1-day TTL | No auth in scope; UUID is unguessable enough for scoping reservations |
| Mutation ownership | Not enforced (`confirm`/`release` identify by UUID, not user) | No auth model; reservation UUIDs are unguessable |
| Realtime sync | WebSocket hub; snapshot-on-connect + delta-apply | Prevents permanent staleness after a dropped channel |

---

## Configuration

| Env var | Default | Meaning |
|---------|---------|---------|
| `DATABASE_URL` | (compose) | Postgres connection string |
| `RESERVATION_TTL` | `60` (seconds) | Pending-hold time-to-live |
| `RESET_TTL_ON_ADD` | `true` | Reset pending window on new hold (per-user, per-item) |

---

## LLM used and why

**Claude** (Anthropic) via **Claude Code CLI** — specifically the Opus model for architecture and
design phases, and Sonnet for implementation.

The full **Spec-Driven Development** flow was used:

1. Constitution (non-negotiable principles — concurrency correctness, test-first)
2. Spec (`/speckit-specify` + `/speckit-clarify` — two-phase model, idempotency key, TTL behavior)
3. Plan + research (concurrency strategy, alternatives evaluated)
4. Tasks (`/speckit-tasks` — dependency-ordered, Strict TDD enforced)
5. Analysis (`/speckit-analyze` — cross-artifact consistency check)
6. Implementation (`/sdd-apply` — TDD batches: tests written and failing before each behavior)

**Why Claude?** Strong long-context reasoning for keeping the constitution, spec, plan, and tasks
coherent across a multi-phase workflow, and reliable Go/PostgreSQL concurrency reasoning — the
highest-risk part of this challenge. The architecture-first approach (correctness guarantee designed
before any code was written) matched Claude's strength in structured, principle-driven reasoning.

---

## Time taken (approximate)

- **Planning phase** (constitution → spec → clarify → plan → tasks → analyze): ~3–4 hours
- **Implementation phase** (T001–T052, TDD on all concurrency/idempotency paths): ~10–14 hours
- **Total**: approximately 14–18 hours end-to-end
