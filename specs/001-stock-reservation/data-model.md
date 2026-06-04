# Phase 1 Data Model: Stock Reservation System

Derived from the spec's Key Entities and Functional Requirements. The schema is the source of truth
for all correctness guarantees (Principle II).

## Entities

### Item

A product available for reservation.

| Field | Type | Constraints | Notes |
|-------|------|-------------|-------|
| `id` | `uuid` (or `text` slug) | PK | Stable identifier |
| `name` | `text` | NOT NULL | Display name |
| `total_stock` | `integer` | NOT NULL, `CHECK (total_stock >= 0)` | Fixed capacity |
| `reserved` | `integer` | NOT NULL DEFAULT 0, `CHECK (reserved >= 0)`, `CHECK (reserved <= total_stock)` | Sum of active (pending + confirmed) units |

- **Derived**: `available = total_stock − reserved` (computed in queries; never stored). The
  `CHECK (reserved <= total_stock)` is a defense-in-depth invariant — the atomic UPDATE predicate is
  the primary guard, but the constraint makes oversell impossible even on a logic bug.

### Reservation

A hold by one user on N units of one item.

| Field | Type | Constraints | Notes |
|-------|------|-------------|-------|
| `id` | `uuid` | PK | Reservation identifier |
| `item_id` | `uuid` | FK → `items.id`, NOT NULL | |
| `user_id` | `text` | NOT NULL | Browser UUID (FR-018) |
| `quantity` | `integer` | NOT NULL, `CHECK (quantity > 0)` | Units held |
| `status` | `text` | NOT NULL, `CHECK (status IN ('pending','confirmed','released','expired'))` | Lifecycle state |
| `created_at` | `timestamptz` | NOT NULL DEFAULT now() | |
| `expires_at` | `timestamptz` | NULL when confirmed | `created_at + TTL`; reset on add (per-user, per-item) if enabled; NULL once confirmed |
| `confirmed_at` | `timestamptz` | NULL | Set on confirm |
| `released_at` | `timestamptz` | NULL | Set on release |

**Indexes**:
- `(user_id)` — "my reservations" view.
- `(status, expires_at)` — sweeper scan for `pending` rows past expiry.
- `(item_id)` — per-item aggregation / event fan-out.

### IdempotencyKey

Links a frontend-generated key to the reservation it produced (Principle III, FR-009/FR-010).

| Field | Type | Constraints | Notes |
|-------|------|-------------|-------|
| `key` | `text` | PK | The `Idempotency-Key` header value |
| `request_hash` | `text` | NOT NULL | Hash of the normalized reserve payload |
| `reservation_id` | `uuid` | FK → `reservations.id`, NOT NULL | The reservation created for this key |
| `created_at` | `timestamptz` | NOT NULL DEFAULT now() | |

- Same `key` + matching `request_hash` → replay (return existing reservation).
- Same `key` + different `request_hash` → 409 conflict.

### User (implicit)

Not a table. A `user_id` is a browser-generated UUID with a ~1-day client-side TTL, carried in the
`X-User-Id` header. No authentication, no server-side session (FR-018).

## State Transitions (Reservation)

```text
                         confirm (status='pending' → 'confirmed')
                       ┌───────────────────────────────► CONFIRMED  (terminal; expires_at = NULL; units stay held)
                       │
   reserve             │
 (atomic UPDATE     PENDING ──────── release ───────────► RELEASED  (terminal; reserved -= qty, exactly once)
  reserved += qty,     │
  insert row)          └──────── TTL sweep / lazy ───────► EXPIRED   (terminal; reserved -= qty, exactly once)
```

**Transition rules**:
- **→ PENDING**: only via reserve. Guarded by `UPDATE items SET reserved = reserved + $q WHERE total_stock - reserved >= $q`; 0 rows ⇒ rejected (insufficient stock / conflict). Reservation row inserted in the same transaction.
- **PENDING → CONFIRMED**: `UPDATE reservations SET status='confirmed', confirmed_at=now(), expires_at=NULL WHERE id=$id AND status='pending'`. **No stock change.** Idempotency is **by reservation state** (the row already carries the idempotency key from its reserve — confirm sends no new key): if the conditional UPDATE matches 0 rows, re-read the row — if it is already `confirmed`, return it as a safe no-op (HTTP 200); if it is `released`/`expired`/absent, reject as "not found / no longer pending" (404/409).
- **PENDING → RELEASED**: conditional transition returns `quantity` to `items.reserved` exactly once (research §3). From any non-pending state ⇒ safe no-op.
- **PENDING → EXPIRED**: sweeper/lazy transition, same exactly-once return. Only `pending` rows expire; `confirmed` never expires (FR-005).
- All terminal states reject further confirm/expire; release on a terminal state is a no-op.

## Invariants

- **INV-1**: `0 ≤ reserved ≤ total_stock` at all times (DB CHECK + atomic predicate).
- **INV-2**: For any reservation, `reserved` reflects its `quantity` while `status ∈ {pending, confirmed}` and excludes it once `released`/`expired` — and the include→exclude transition happens **exactly once**.
- **INV-3**: A given `Idempotency-Key` maps to exactly one reservation.
- **INV-4**: `available = total_stock − reserved` is always derivable and never negative.

## Seed Data

A small catalog mirroring the visual reference, with varied stock and at least one out-of-stock
item, e.g.: Vintage Camera, Mechanical Watch, Acoustic Guitar, Smart Flask, Running Shoes (0 stock),
Gaming Mouse. Loaded via a migration/seed script so `docker compose up` yields a reviewable state.
