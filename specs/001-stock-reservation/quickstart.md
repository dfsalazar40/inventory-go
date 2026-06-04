# Quickstart: Stock Reservation System

How to run, exercise, and test the full stack. The whole system comes up with a single command
(Principle VII).

## Prerequisites

- Docker + Docker Compose
- (For local dev outside containers) Go 1.24+, Node 22+

## Run the whole stack

```bash
docker compose up --build
```

This brings up three services, seeded and ready:

| Service    | URL                     | Notes                                          |
|------------|-------------------------|------------------------------------------------|
| `db`       | localhost:5432          | PostgreSQL 16+, migrations + seed applied      |
| `backend`  | http://localhost:8080   | Go API + WebSocket at `/ws`                    |
| `frontend` | http://localhost:5173   | React + Vite dashboard                         |

Seed inventory (varied stock, one out-of-stock item) loads automatically. Open the frontend and you
should see the catalog with live availability.

## Configuration

| Env var             | Default | Meaning                                                              |
|---------------------|---------|----------------------------------------------------------------------|
| `RESERVATION_TTL`   | `60s`   | Pending-hold time-to-live.                                           |
| `RESET_TTL_ON_ADD`  | `true`  | If true, adding a pending hold resets the user's pending TTL window. |
| `DATABASE_URL`      | (compose) | Postgres connection string.                                        |

## Try the API by hand

```bash
USER=$(uuidgen)

# 1) Create a PENDING hold (phase 1). Idempotency-Key is required.
KEY=$(uuidgen)
curl -s -X POST http://localhost:8080/reservations \
  -H "X-User-Id: $USER" -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"itemId":"vintage-camera","quantity":1}'

# Replaying the SAME key + payload returns the SAME reservation (no second decrement):
curl -s -X POST http://localhost:8080/reservations \
  -H "X-User-Id: $USER" -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"itemId":"vintage-camera","quantity":1}'

# 2) Confirm it (phase 2) — TTL stops, units stay held:
curl -s -X POST http://localhost:8080/reservations/<RES_ID>/confirm -H "X-User-Id: $USER"

# 3) Or release a (still pending) hold — returns stock exactly once, safe to repeat:
curl -s -X DELETE http://localhost:8080/reservations/<RES_ID> -H "X-User-Id: $USER"

# Missing Idempotency-Key on reserve -> 400:
curl -s -X POST http://localhost:8080/reservations \
  -H "X-User-Id: $USER" -H 'Content-Type: application/json' \
  -d '{"itemId":"vintage-camera","quantity":1}'
```

## Run the tests

### Backend (concurrency + idempotency — the proof of correctness)

```bash
cd backend
go test ./...            # full suite against a Postgres test instance
go test -race ./...      # race detector on
```

Key suites:
- **Last-unit contention**: 50+ goroutines for 1 unit → exactly 1 succeeds.
- **Oversell**: 100 goroutines for 10 units → exactly 10 succeed, 90 rejected, stock never negative.
- **Reserve idempotency**: parallel duplicate key → one reservation, one decrement.
- **Release idempotency**: double release → stock returns once; release-after-expire → no-op.
- **TTL**: pending expires & returns stock once; confirmed does NOT expire.

### Frontend (timer + components)

```bash
cd frontend
npm test                 # Vitest
```

- Countdown timer unit test, reserve happy-path component test, error-state (conflict /
  insufficient stock) component test.

## Manual realtime check (Principle V)

Open the dashboard in two browser tabs. Reserve units of an item in one tab; the other tab's
available count updates within ~1s without a manual refresh. Let a pending hold expire; both tabs
reflect the freed stock automatically.
