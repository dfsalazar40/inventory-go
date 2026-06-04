# Phase 0 Research: Stock Reservation System

This document records the technical decisions that resolve the Technical Context, with rationale
and rejected alternatives. Every decision traces to a constitution principle and/or a spec
requirement.

## 1. Anti-oversell concurrency strategy (Principle II)

**Decision**: A single **atomic conditional `UPDATE`** is the sole gate for creating a hold:

```sql
UPDATE items
   SET reserved = reserved + $qty
 WHERE id = $item_id
   AND total_stock - reserved >= $qty
RETURNING id, total_stock, reserved;
```

If `0` rows are affected, availability was insufficient → reject with a typed conflict /
insufficient-stock error. If `1` row, the hold capacity is already secured; the `reservations` row
is then inserted **in the same transaction**. The `WHERE total_stock - reserved >= $qty` predicate
is the correctness guarantee — the check and the decrement happen in one statement, so no
interleaving can drive `available` negative.

**Rationale**: Postgres evaluates the `UPDATE … WHERE` atomically under row-level locking; two
concurrent requests for the last unit serialize on that row and exactly one sees the predicate
satisfied. This is the only trustworthy guarantee (Principle II) and needs no application lock.

**Alternatives considered**:
- **`SELECT … FOR UPDATE` then `UPDATE`** — rejected: two statements, longer lock hold, more
  round-trips, and easy to get wrong (the classic read-then-write window). The conditional UPDATE
  achieves the same guarantee in one statement with a shorter lock.
- **`SERIALIZABLE` isolation + retry** — rejected: under a flash-sale stampede this produces a
  serialization-failure retry storm; correctness becomes a function of retry tuning.
- **Application-level mutex / Redis lock** — rejected: moves the source of truth out of the DB,
  adds a failure mode, and violates "race conditions MUST be handled at the database layer."

## 2. Stock accounting: when does `reserved` change?

**Decision**: `reserved` is a column on `items`; `available` is derived as `total_stock − reserved`.
- **Reserve (create PENDING hold)** → `reserved += qty` (the atomic UPDATE above). Stock is
  committed at hold time — this is what prevents oversell during the pending window.
- **Confirm (PENDING → CONFIRMED)** → **no stock change**; only a status/expiry transition.
- **Release / Expire** → `reserved -= qty`, returned **exactly once** (see §3).

**Rationale**: Holding stock at pre-reservation time is what makes the flash-sale guarantee real;
confirming is a commitment that must not be able to fail for lack of stock. A confirmed reservation
keeps its units indefinitely (no TTL).

**Alternative rejected**: decrement only at confirm — rejected, it reopens the oversell window
during the pending phase (two users could both hold the last unit and both confirm).

## 3. Exactly-once stock return on release/expiration (Principles II, III)

**Decision**: Stock is returned by a **conditional state transition** that only the winner of the
race executes:

```sql
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

The `WHERE status = 'pending'` clause means the transition fires at most once; only that execution
decrements `reserved`. The expiration sweep uses the identical pattern with `status = 'expired'`.

**Rationale**: This makes double-release a safe no-op (second call matches 0 rows) and resolves the
expire-vs-release race — whichever transition commits first wins, the other matches 0 rows, and
stock returns exactly once (spec Edge Cases; Principle III). Releasing an already
expired/released/confirmed reservation is a well-defined no-op.

## 4. Idempotency mechanism (Principle III, FR-009/FR-010)

**Decision**: The frontend generates a UUID `Idempotency-Key` per "Reserve Item" action and sends
it as a header. The backend persists keys and enforces semantics atomically:

- Table `idempotency_keys(key PK, request_hash, reservation_id, created_at)`.
- On reserve, inside the transaction: `INSERT INTO idempotency_keys … ON CONFLICT (key) DO NOTHING`.
  - Insert succeeded → first time: proceed to create the hold, store `reservation_id` + payload hash.
  - Conflict (key exists) → load the stored row:
    - same `request_hash` → return the **existing** reservation (replay, no new decrement).
    - different `request_hash` → **409** idempotency-key conflict.
- Missing header → **400** (FR-009).

**Rationale**: The unique PK + `ON CONFLICT` makes concurrent duplicate-key reserves resolve to a
single winner at the DB layer (spec US6 scenario 3). The payload hash distinguishes safe replays
from conflicting reuse (FR-010).

**Alternatives considered**:
- **Server-generated key** — rejected during `/clarify`: it cannot dedupe a client retry (the retry
  carries no key the server can recognize), so it provides no real idempotency — it is just a
  reservation id.
- **In-memory key cache** — rejected: lost on restart and not shared across backend instances;
  correctness must survive in the DB.

## 5. TTL expiration: sweeper + lazy + configurable reset (Principle II/V, FR-005/FR-017)

**Decision**:
- **Background sweeper**: a Go goroutine with `time.Ticker` (≈1s) runs the conditional
  `pending → expired` transition (§3 pattern) for all rows where `status='pending' AND expires_at <= now()`,
  returning their units in the same statement. Meets SC-006 (~1s).
- **Lazy expiration on read**: any query that returns reservations treats `pending` rows past
  `expires_at` as expired, so a momentary sweeper lag never shows stale holds.
- **`RESET_TTL_ON_ADD`** (env flag, **default `true`**): when enabled, creating a new pending hold
  updates `expires_at = now() + ttl` for **all of that user's pending reservations** (one fresh
  shared window). When disabled, each hold keeps its original `created_at + ttl`. The same flag
  drives the value the frontend countdown renders.

**Rationale**: The sweeper guarantees prompt, server-authoritative expiration; lazy reads close the
sub-second gap. Making the reset behavior a documented flag satisfies the clarified NFR without
hard-coding a debatable policy.

**Alternatives considered**:
- **Postgres `pg_cron` / TTL extension** — rejected: extra infra dependency; a Go ticker is simpler
  and keeps the logic in one place (Principle VII).
- **Per-row timers** — rejected: doesn't scale and is fragile across restarts.

## 6. Realtime synchronization (Principle V, FR-011)

**Decision**: `gorilla/websocket` hub in the backend. Every committed stock/reservation mutation
(reserve, confirm, release, expire) publishes an event to the hub, which broadcasts to all
connected clients. On connect (and on reconnect), the client first fetches a REST snapshot
(`GET /items`, `GET /reservations?userId=…`) and then applies live deltas, so it always reconciles
to backend truth. The client auto-reconnects with backoff (spec Edge Case "realtime channel drops").

**Rationale**: WebSockets are mandated (Principle V). Snapshot-on-connect + delta-after is the
standard pattern that prevents permanent staleness after a dropped channel.

**Alternatives considered**:
- **SSE / long-polling** — rejected: the constitution mandates WebSockets; polling also lies about
  availability between intervals.
- **Per-client DB `LISTEN/NOTIFY` fan-out** — deferred: a single in-process hub is sufficient at the
  mandated single-instance scale; `LISTEN/NOTIFY` would be the path to multi-instance later.

## 7. User identity (FR-018)

**Decision**: The browser generates a UUID on first load, stores it in `localStorage` with a
timestamp, and treats it as expired after ~1 day (regenerating a new one). It is sent on every
request via an `X-User-Id` header. The backend trusts it; there is no auth or server session.

**Rationale**: Matches the clarified requirement and the client's "no auth needed for the
challenge"; a thin anonymous identifier is enough to scope the "my reservations" view.

## 8. Stack versions & tooling (Principle VII)

**Decision**: Go 1.24+, `chi` v5, `pgx/v5`, `gorilla/websocket`, `golang-migrate`; PostgreSQL 16+;
React 19 + Vite 6 + TypeScript 5 on Node 22; Vitest + React Testing Library; Docker Compose for the
full seeded stack.

**Rationale**: All current stable, all mandated by the constitution's Technology Constraints.
Lightweight router + driver, no heavyweight ORM (the atomic SQL is written by hand and must be
auditable).

## 9. Testing approach (Principle IV — Strict TDD)

**Decision**:
- **Backend concurrency** (Go `testing` + `sync.WaitGroup`, real Postgres): 50+ goroutines for the
  last unit (exactly 1 wins); 100 goroutines for 10 units (exactly 10 succeed, 90 rejected, never
  negative); assert `reserved ≤ total_stock` invariant after the storm.
- **Backend idempotency**: parallel duplicate-key reserve → one reservation, one decrement; reuse
  key with different payload → 409; double-release → stock returns once; release-after-expire no-op.
- **TTL**: pending expires and returns stock once; confirmed does NOT expire; expire-vs-release race
  returns stock once.
- **Frontend**: countdown timer unit test; reserve happy-path component test; error-state (conflict /
  insufficient stock) component test.

**Rationale**: Principle IV is NON-NEGOTIABLE; these are the suites that turn "seems fine" into
"proven correct." Tests are committed with or before the code they cover.
