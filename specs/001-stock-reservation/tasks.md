---
description: "Task list for Stock Reservation System implementation"
---

# Tasks: Stock Reservation System

**Input**: Design documents from `/specs/001-stock-reservation/`

**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/openapi.yaml ✅

**Tests**: INCLUDED and TEST-FIRST. Constitution Principle IV (Test-First on Critical Paths) is
NON-NEGOTIABLE and Strict TDD Mode is active — concurrency and idempotency tests MUST be written and
MUST fail before the code that satisfies them.

**Organization**: Grouped by user story (priority order from spec.md) for independent, incremental
delivery.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: User story the task serves (US1–US8)
- Exact file paths are included in every task.

## Path Conventions

Web app (per plan.md): `backend/` (Go) and `frontend/` (React + Vite + TS) at repo root.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and reproducible tooling.

- [X] T001 Create the monorepo layout (`backend/`, `frontend/`) per plan.md Project Structure
- [X] T002 [P] Initialize Go module with chi, pgx/v5, gorilla/websocket, golang-migrate in `backend/go.mod`
- [X] T003 [P] Scaffold React 19 + Vite 6 + TypeScript app with Vitest + React Testing Library in `frontend/package.json`
- [X] T004 [P] Author `docker-compose.yml` (db + backend + frontend, single-command, seeded) at repo root
- [X] T005 [P] Add `backend/Dockerfile` and `frontend/Dockerfile`
- [X] T006 [P] Configure linting/formatting: `backend/.golangci.yml` and `frontend/.eslintrc` + Prettier

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, config, wiring, and base types that ALL stories depend on.

**⚠️ CRITICAL**: No user story work begins until this phase is complete.

- [X] T007 Write SQL migrations for `items`, `reservations`, `idempotency_keys` (constraints + indexes per data-model.md) in `backend/migrations/`
- [X] T008 [P] Create seed data (catalog incl. one out-of-stock item) in `backend/seed/seed.sql`
- [X] T009 [P] Implement config loader (`DATABASE_URL`, `RESERVATION_TTL`, `RESET_TTL_ON_ADD`) in `backend/internal/config/config.go`
- [X] T010 [P] Define domain entities (`Item`, `Reservation`), `ReservationStatus` enum, and typed errors in `backend/internal/domain/`
- [X] T011 Wire pgx pool + migration runner + graceful shutdown skeleton in `backend/cmd/server/main.go`
- [X] T012 Set up chi router with recovery/logging middleware and the `X-User-Id` middleware in `backend/internal/api/router.go`
- [X] T013 [P] Frontend: `userId` lib (browser UUID, ~1-day TTL) and idempotency-key generator in `frontend/src/lib/identity.ts`
- [X] T014 [P] Frontend: base REST client (auto-sends `X-User-Id` and `Idempotency-Key`) in `frontend/src/api/client.ts`

**Checkpoint**: Foundation ready — user stories can begin.

---

## Phase 3: User Story 1 - Reserve stock without ever over-selling (Priority: P1) 🎯 MVP

**Goal**: A reserve creates a PENDING hold and never allows `available` to go negative under load.

**Independent Test**: Seed one item; fire many concurrent reserves; sum of successes equals stock and never exceeds it.

### Tests for User Story 1 (write first, must FAIL) ⚠️

- [X] T015 [P] [US1] Last-unit contention test: 50+ goroutines for 1 unit → exactly 1 succeeds, stock never negative, in `backend/internal/store/reserve_concurrency_test.go`
- [X] T016 [P] [US1] Oversell test: 100 goroutines for 10 units → exactly 10 succeed, 90 rejected, `reserved <= total_stock` invariant holds, in `backend/internal/store/reserve_oversell_test.go`
- [X] T017 [P] [US1] Validation test: quantity 0 / negative / non-integer / exceeding total rejected with no state change, in `backend/internal/api/reserve_validation_test.go`

### Implementation for User Story 1

- [X] T018 [US1] Implement the atomic conditional reserve (`UPDATE items SET reserved = reserved + $q WHERE total_stock - reserved >= $q` + insert reservation in one tx) in `backend/internal/store/reservations.go`
- [X] T019 [US1] Implement reserve service mapping 0-rows → typed insufficient-stock/conflict error in `backend/internal/store/reservations.go`
- [X] T020 [US1] Implement `POST /reservations` handler with payload validation and typed error responses in `backend/internal/api/reservations.go`

**Checkpoint**: MVP — reserve is concurrency-safe and independently testable.

---

## Phase 4: User Story 6 - Idempotent reserve under retries (Priority: P1)

**Goal**: Duplicate reserve requests (same key + payload) never double-decrement; key reuse with a different payload is rejected.

**Independent Test**: Send the same reserve (same key + payload) twice in parallel → one reservation, one decrement.

### Tests for User Story 6 (write first, must FAIL) ⚠️

- [X] T021 [P] [US6] Parallel-duplicate-key test: same key+payload twice concurrently → one reservation, one decrement, in `backend/internal/api/idempotency_concurrency_test.go`
- [X] T022 [P] [US6] Conflict/required test: same key + different payload → 409; missing header → 400, in `backend/internal/api/idempotency_test.go`

### Implementation for User Story 6

- [X] T023 [US6] Implement idempotency store (`INSERT ... ON CONFLICT DO NOTHING`, payload hashing, replay vs conflict) in `backend/internal/store/idempotency.go`
- [X] T024 [US6] Enforce required `Idempotency-Key` and wire replay/conflict into `POST /reservations` in `backend/internal/api/reservations.go`

**Checkpoint**: Reserve is now both concurrency-safe and retry-safe.

---

## Phase 5: User Story 8 - Confirm a pending reservation (Priority: P1)

**Goal**: Confirm transitions PENDING → CONFIRMED, stops the TTL, keeps units held, and is idempotent.

**Independent Test**: Create a hold, confirm it, wait past TTL → still CONFIRMED, units held, never expired.

### Tests for User Story 8 (write first, must FAIL) ⚠️

- [X] T025 [P] [US8] Confirm test: pending→confirmed (no stock change, `expires_at` NULL); does NOT expire after TTL; re-confirm already-confirmed = 200 no-op (idempotent by reservation state); confirm released/expired/absent → 404/409; **confirm-vs-expire race**: TTL sweep and confirm fire simultaneously on the same pending row → exactly one outcome wins (confirmed & kept, or expired & stock returned once), never both, in `backend/internal/store/confirm_test.go`

### Implementation for User Story 8

- [X] T026 [US8] Implement confirm conditional transition (`... WHERE status='pending'`) in `backend/internal/store/reservations.go`
- [X] T027 [US8] Implement `POST /reservations/{id}/confirm` handler: conditional transition; on 0 rows re-read the row and return 200 no-op if already `confirmed`, else typed 404 not-found / 409 not-pending. Confirm carries no new idempotency key (idempotent by reservation state), in `backend/internal/api/reservations.go`

**Checkpoint**: The two-phase reserve flow (hold → confirm) is complete.

---

## Phase 6: User Story 2 - See live, accurate inventory (Priority: P1)

**Goal**: A dashboard shows live `available` per item and updates over WebSockets without refresh.

**Independent Test**: Open dashboard; a second actor reserves; the first client's available drops within ~1s without refresh.

### Tests for User Story 2 (write first, must FAIL) ⚠️

- [X] T028 [P] [US2] Backend test: `GET /items` returns derived available; a mutation publishes a broadcast event, in `backend/internal/api/items_test.go`

### Implementation for User Story 2

- [X] T029 [US2] Implement `GET /items` handler + store query (available = total − reserved) in `backend/internal/api/items.go`
- [X] T030 [US2] Implement the WebSocket hub (register/unregister/broadcast) in `backend/internal/realtime/hub.go`
- [X] T031 [US2] Publish stock/reservation events from reserve & confirm paths; mount `/ws` and wire the hub in `backend/cmd/server/main.go`
- [X] T032 [P] [US2] Frontend `useWebSocket` hook, **test-first**: write `frontend/src/hooks/useWebSocket.test.ts` asserting reconnect-with-backoff and snapshot-on-connect reconcile (simulate a dropped channel → client refetches the REST snapshot and reconciles to backend truth), make it FAIL, then implement connect/reconnect/reconcile in `frontend/src/hooks/useWebSocket.ts`
- [X] T033 [US2] Frontend: `InventoryDashboard` + `ItemCard` (live available, "Out of Stock") in `frontend/src/components/`

**Checkpoint**: Live, synced inventory is visible and reactive.

---

## Phase 7: User Story 3 - Pending reservations expire after 60s (Priority: P2)

**Goal**: PENDING holds expire on TTL and return stock exactly once; confirmed holds never expire; reset behavior is configurable.

**Independent Test**: Create a hold, wait past 60s → units back, reservation gone; confirmed hold survives.

### Tests for User Story 3 (write first, must FAIL) ⚠️

- [X] T034 [P] [US3] TTL test: pending expires & returns stock once; confirmed never expires; expire-vs-release race returns once; `RESET_TTL_ON_ADD` on/off behavior (per-user, per-item scope), in `backend/internal/ttl/sweeper_test.go`

### Implementation for User Story 3

- [X] T035 [US3] Implement TTL sweeper (`time.Ticker` ~1s) running the conditional `pending→expired` transition in `backend/internal/ttl/sweeper.go`
- [X] T036 [US3] Implement lazy expiration on read in reservation queries in `backend/internal/store/reservations.go`
- [X] T037 [US3] Implement `RESET_TTL_ON_ADD` (on a new hold, reset `expires_at` for the user's pending holds **of that same item only** — per-user, per-item; other items untouched) in the reserve path in `backend/internal/store/reservations.go`
- [X] T038 [US3] Broadcast expiration events to the WebSocket hub in `backend/internal/ttl/sweeper.go`

**Checkpoint**: Stock stays liquid; abandoned holds free themselves.

---

## Phase 8: User Story 4 - Manually release a reservation (Priority: P2)

**Goal**: Release returns units exactly once and is safe to repeat, even after the timer hits zero.

**Independent Test**: Create, release, release again → stock returns once, second call is a no-op.

### Tests for User Story 4 (write first, must FAIL) ⚠️

- [X] T039 [P] [US4] Release test: release returns stock once; double-release no-op; release-after-expire no-op (clock-skew case), in `backend/internal/store/release_test.go`

### Implementation for User Story 4

- [X] T040 [US4] Implement release conditional transition returning stock exactly once in `backend/internal/store/reservations.go`
- [X] T041 [US4] Implement `DELETE /reservations/{id}` handler + broadcast in `backend/internal/api/reservations.go`

**Checkpoint**: Users control their holds; release races resolve cleanly.

---

## Phase 9: User Story 5 - View my active reservations with a live countdown (Priority: P2)

**Goal**: A panel lists the user's holds with item, units, live countdown, and confirm/release actions.

**Independent Test**: Reserve two items, open the panel → both listed with decreasing countdowns and working buttons.

### Tests for User Story 5 (write first, must FAIL) ⚠️

- [ ] T042 [P] [US5] Frontend countdown unit test (counts down, removes at zero in sync with backend) in `frontend/src/hooks/useCountdown.test.ts`

### Implementation for User Story 5

- [ ] T043 [US5] Implement `GET /reservations` (scoped to `X-User-Id`, pending + confirmed) in `backend/internal/api/reservations.go`
- [ ] T044 [P] [US5] Frontend: `useReservations` + `useCountdown` hooks in `frontend/src/hooks/`
- [ ] T045 [US5] Frontend: `ReservationPanel` with Confirm (above) and Release (below) per line + live countdown in `frontend/src/components/ReservationPanel.tsx`

**Checkpoint**: Users see and manage exactly what they hold.

---

## Phase 10: User Story 7 - Graceful conflict and error feedback (Priority: P2)

**Goal**: Distinct, user-readable messages for conflict / insufficient stock / invalid quantity / transient errors, plus loading states and double-submit guards.

**Independent Test**: Force each failure type → a distinct message and a recovered, usable UI.

### Tests for User Story 7 (write first, must FAIL) ⚠️

- [ ] T046 [P] [US7] Frontend component tests: reserve happy-path; error-state (conflict + insufficient stock) renders the right message and keeps the UI usable, in `frontend/src/components/ItemCard.test.tsx`

### Implementation for User Story 7

- [ ] T047 [US7] Frontend: map typed API errors to messages, add loading/success/error states and double-submit guard across reserve/confirm/release in `frontend/src/components/` + `frontend/src/api/client.ts`

**Checkpoint**: Every async action communicates clearly; no silent failures.

---

## Phase 11: Polish & Cross-Cutting Concerns

- [ ] T048 [P] Serve `openapi.yaml` from the backend (e.g. `GET /openapi.yaml`) and keep it in sync with handlers in `backend/internal/api/`
- [ ] T049 [P] Write `README.md`: concurrency strategy, how to run, how to run tests, LLM used + why, time-taken
- [ ] T050 [P] Update `spec-kit-notes.md` with assumptions/pivots (two-phase model, frontend idempotency key, `RESET_TTL_ON_ADD`)
- [ ] T051 Run `quickstart.md` validation end-to-end: `docker compose up`, manual two-tab realtime check
- [ ] T052 Run full suites green: `go test -race ./...` (backend) and `npm test` (frontend); final cleanup

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup — BLOCKS all stories.
- **User Stories (Phases 3–10)**: all depend on Foundational. Within them:
  - US6 (idempotency) and US8 (confirm) build directly on US1's reserve path.
  - US2 (realtime) needs at least one mutation (US1) to broadcast.
  - US3 (TTL) and US4 (release) share the conditional-transition pattern in the store.
  - US5/US7 are frontend-facing and depend on the relevant endpoints existing.
- **Polish (Phase 11)**: depends on the desired stories being complete.

### Recommended build order (priority + dependency)

`Setup → Foundational → US1 → US6 → US8 → US2 → US3 → US4 → US5 → US7 → Polish`

### Within Each User Story

- Tests written and FAILING before implementation (Strict TDD).
- Store/SQL before service before handler; backend endpoint before its frontend consumer.

### Parallel Opportunities

- Setup: T002–T006 in parallel.
- Foundational: T008, T009, T010, T013, T014 in parallel (after T007/T011 scaffolding).
- Per story, all `[P]` test files can be written in parallel before implementation.

---

## Parallel Example: User Story 1

```bash
# Write all US1 tests first, in parallel (they must FAIL):
Task: "Last-unit contention test in backend/internal/store/reserve_concurrency_test.go"
Task: "Oversell test in backend/internal/store/reserve_oversell_test.go"
Task: "Validation test in backend/internal/api/reserve_validation_test.go"
# Then implement T018 → T019 → T020 to make them pass.
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup → 2. Phase 2 Foundational → 3. Phase 3 US1 → **STOP & VALIDATE** the concurrency
   guarantee (the core promise) → demo.

### Incremental Delivery

Add US6 (retry-safety) → US8 (confirm) → US2 (live UI) → then the P2 stories (TTL, release, my
reservations, error UX). Each phase is an independently testable increment that doesn't break the
previous ones.

### Delivery / PR sizing note

This is a solo challenge on a single `main` branch (per spec Assumptions). Commit per task or logical
group in workflow order (Principle VI). The full implementation comfortably exceeds a 400-line single
review; deliver it as a sequence of small, conventional commits grouped by phase rather than one
monolithic change.

---

## Notes

- `[P]` = different files, no incomplete dependency.
- `[Story]` label keeps each task traceable to a spec user story (Principle VI).
- Verify each critical-path test FAILS before implementing (Principle IV).
- Commit after each task or logical group, conventional messages, workflow order.
- The atomic conditional UPDATE (T018) and exactly-once transitions (T040, T035) are the heart of
  Principle II — review them adversarially.
