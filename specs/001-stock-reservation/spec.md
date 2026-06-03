# Feature Specification: Stock Reservation System

**Feature Branch**: `main` (single-branch workflow — see Assumptions)

**Created**: 2026-06-02

**Status**: Draft

**Input**: User description: "Build a Stock Reservation System for a high-traffic flash sale.
Users hold items for a limited window (60s TTL). Prevent over-reservation under high concurrent
load. Support atomic reservations, manual release, idempotent reserve/release, conflict handling,
a live inventory dashboard, and a per-user view of active reservations."

## Clarifications

### Session 2026-06-03

- Q: Do reservations have a "confirm" step, or are they only temporary holds ended by release/expiration? → A: **Two-phase model.** "Reserve Item" creates a temporary **PENDING** hold (a pre-reservation); a **Confirm** action finalizes it into a **CONFIRMED** reservation that no longer expires. **Release** returns the unit to available stock. (Authoritative client clarification: the mockup's "release" returns the item; a "Confirm" control was missing and belongs above each pending line.)
- Q: Can a user reserve multiple units of the same product? → A: **Yes.** Each "Reserve Item" click adds one unit as a separate pending hold to the user's to-confirm list; pending holds are confirmed one at a time.
- Q: Is the reserve Idempotency-Key client- or server-generated? → A: **Generated on the frontend** (one fresh key per "Reserve Item" action) and sent in the request header; the **backend validates and manages it** (dedup of retries + key/payload conflict detection). The header is **required** (400 if missing), since the frontend always generates it.
- Q: How is the user identified? → A: A **browser-generated UUID with a 1-day TTL**; no authentication or server-side session handling is in scope. The "my reservations" view is scoped to that UUID.
- Q: Should the pending-reservation TTL countdown reset when a new item is added? → A: **Yes**, the countdown resets on each add. This is a **configurable** behavior via a `RESET_TTL_ON_ADD` flag: enabled = reset the pending window on each add; disabled = each pending hold keeps its original `creation + TTL` expiration.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reserve stock without ever over-selling (Priority: P1)

A shopper sees an item with available stock and reserves N units. A successful reserve creates a
**PENDING** hold (a pre-reservation) that decrements available stock immediately and must later be
confirmed (US8) or it expires (US3). The reservation succeeds only if at least N units are actually
available at that instant; otherwise it is cleanly rejected. The UI issues one unit per "Reserve
Item" click, so a user may accumulate several pending holds on the same item; the API also accepts a
quantity N in a single request (used by the concurrency tests). Under a flash-sale stampede, the
total number of active (pending + confirmed) reserved units for an item can never exceed its total
stock — no double-sell, ever.

**Why this priority**: This is the core promise of the system. Everything else is secondary to
the guarantee that stock is never over-reserved. Without it, the product has no reason to exist.

**Independent Test**: Seed one item with a known total. Fire many concurrent reserve requests and
assert the sum of successful reservations equals the stock and never exceeds it; available stock
never goes negative.

**Acceptance Scenarios**:

1. **Given** an item with 5 available units, **When** a user reserves 3 units, **Then** the
   reservation is created and available becomes 2.
2. **Given** an item with 2 available units, **When** a user requests 3 units, **Then** the
   request is rejected with an "insufficient stock" error and available stays 2.
3. **Given** an item with exactly 1 available unit, **When** 50+ users simultaneously request that
   last unit, **Then** exactly one reservation succeeds and all others are rejected with a conflict
   error; available becomes 0 and never negative.
4. **Given** an item with 10 available units, **When** 100 users each concurrently request 1 unit,
   **Then** exactly 10 reservations succeed, 90 are rejected, and available becomes 0.
5. **Given** a request for 0, a negative quantity, or a non-integer quantity, **When** submitted,
   **Then** it is rejected with a validation error and no stock changes.

---

### User Story 2 - See live, accurate inventory (Priority: P1)

A shopper views a dashboard listing each item with its name, total stock, reserved count, and
available count (available = total − reserved). The numbers reflect the true backend state and
update on their own as other shoppers reserve, release, or let reservations expire — no manual
refresh required.

**Why this priority**: A flash sale where the screen lies about availability recreates the exact
double-sell frustration the system exists to prevent. Accurate, live numbers are essential to the
reserve flow being usable.

**Independent Test**: Open the dashboard, have a second actor reserve units of an item, and assert
the first client's available count decreases within a bounded time without a manual refresh.

**Acceptance Scenarios**:

1. **Given** the dashboard is open, **When** another user reserves units of an item, **Then** that
   item's available count decreases on screen within ~1 second without refreshing.
2. **Given** an item whose available reaches 0, **When** displayed, **Then** its reserve action is
   shown as unavailable ("Out of Stock").
3. **Given** a reservation expires or is released elsewhere, **When** it frees stock, **Then** the
   affected item's available count increases on screen within ~1 second.

---

### User Story 3 - Pending reservations expire automatically after 60 seconds (Priority: P2)

When a user creates a pending hold but does not confirm or release it, the **pending** reservation
automatically expires 60 seconds after creation (or after its last reset window — see the
`RESET_TTL_ON_ADD` clarification). On expiration it is permanently removed, can no longer be
interacted with, and its units return to the available pool exactly once. **Confirmed reservations
do not expire** — confirming a hold stops its TTL and locks the units.

**Why this priority**: TTL is what keeps stock liquid during a flash sale; abandoned holds must
not lock inventory forever. It depends on US1 existing first.

**Independent Test**: Create a reservation, wait past 60 seconds, and assert the units are back in
available, the reservation is gone, and any attempt to act on it is rejected as "not found / expired."

**Acceptance Scenarios**:

1. **Given** an active reservation, **When** 60 seconds elapse without release, **Then** the
   reservation is removed and its units return to available exactly once.
2. **Given** a reservation that has just expired, **When** a user tries to release it, **Then** the
   system responds with a well-defined result and does not return stock a second time.
3. **Given** an expired reservation, **When** the dashboard updates, **Then** it disappears from the
   user's active reservations list.

---

### User Story 4 - Manually release a reservation at any time (Priority: P2)

A user can cancel/release their reservation whenever they want, including after the displayed timer
has hit zero (the UI clock may be out of sync with the server). Releasing returns the units to the
available pool exactly once and is safe to retry.

**Why this priority**: Gives users control and handles UI/server clock skew gracefully. Builds on
US1 and interacts with US3.

**Independent Test**: Create a reservation, release it, then release it again; assert stock returns
exactly once and the second release is a safe no-op.

**Acceptance Scenarios**:

1. **Given** an active reservation, **When** the user releases it, **Then** its units return to
   available and it leaves the active list.
2. **Given** an already-released reservation, **When** the user releases it again, **Then** the
   call succeeds as a no-op and stock is not returned a second time.
3. **Given** a reservation whose timer shows 00:00 in the UI but the user clicks Release, **Then**
   the release is accepted and resolves cleanly (either frees the units if still active, or is a
   no-op if already expired) — never an unhandled error.

---

### User Story 5 - View my active reservations with a live countdown (Priority: P2)

A user sees a panel of their own active reservations, each showing the item, units held, a live
countdown to expiration, and a Release button.

**Why this priority**: This is how a user understands and manages what they are holding. Depends on
US1 and US4.

**Independent Test**: Reserve two items, open the panel, assert both appear with decreasing
countdowns and working Release buttons.

**Acceptance Scenarios**:

1. **Given** a user with two active reservations, **When** they open their reservations panel,
   **Then** both are listed with item name, units held, and a countdown.
2. **Given** a reservation in the panel, **When** its countdown reaches zero, **Then** it is removed
   from the panel in sync with the backend expiration.

---

### User Story 6 - Idempotent reserve and release under retries (Priority: P1)

Network retries, double-clicks, and at-least-once delivery must never cause duplicate state
changes. A reserve request carries an idempotency key; repeating it returns the same reservation
without decrementing stock twice. Releasing the same reservation repeatedly returns stock at most
once.

**Why this priority**: In a flash sale, retries and double-clicks are the norm. Without idempotency
the concurrency guarantee of US1 is undermined by the client's own retries.

**Independent Test**: Send the same reserve request (same idempotency key, same payload) twice in
parallel; assert one reservation and one stock decrement. Release the same reservation twice;
assert stock returns once.

**Acceptance Scenarios**:

1. **Given** a reserve request with idempotency key K and payload P, **When** it is sent twice with
   the same K and P, **Then** both responses describe the same reservation (same id, same outcome)
   and stock is decremented exactly once.
2. **Given** a prior reserve with key K and payload P, **When** a new request reuses K with a
   different payload, **Then** it is rejected with a clear "idempotency key conflict" error and no
   new reservation is created.
3. **Given** two reserve requests with the same K and P sent at the same instant, **When** processed
   concurrently, **Then** exactly one reservation is created and one decrement occurs.

---

### User Story 7 - Graceful conflict and error feedback (Priority: P2)

When a reserve fails because the item was just taken, the quantity is invalid, or stock is
insufficient, the user sees a clear, specific message (e.g., "Item Taken — reserved by another
user") rather than a silent failure or a generic crash. All async actions show loading and error
states.

**Why this priority**: Directly tied to the rubric's conflict-handling and usability criteria and
the provided visual reference.

**Independent Test**: Force each failure type (conflict, insufficient stock, invalid quantity) and
assert a distinct, user-readable message and a recovered (non-blocked) UI.

**Acceptance Scenarios**:

1. **Given** an item that another user reserves first, **When** the user's reserve loses the race,
   **Then** a conflict message is shown and the dashboard reflects the true remaining stock.
2. **Given** any reserve/release in flight, **When** the request is pending, **Then** a loading
   state is shown and the action is guarded against double submission.
3. **Given** a transient backend/network error, **When** an action fails, **Then** a non-blocking
   error message is shown and the user can retry.

---

### User Story 8 - Confirm a pending reservation into a final hold (Priority: P1)

A user reviews their pending holds and confirms them one at a time. Confirming transitions a
reservation from **PENDING** to **CONFIRMED**: the units stay held (no stock change), the TTL stops,
and the reservation can no longer expire. The Confirm control sits above the Release control on each
pending line. Confirming is the second phase of the two-phase reserve model.

**Why this priority**: Confirmation is the action that turns a temporary hold into a committed
reservation — it is half of the core reserve flow the client explicitly requires, not an optional
extra.

**Independent Test**: Create a pending hold, confirm it, wait past the TTL, and assert the
reservation is still present as CONFIRMED, its units remain held, and it never expired.

**Acceptance Scenarios**:

1. **Given** a pending reservation, **When** the user confirms it, **Then** its status becomes
   CONFIRMED, its units remain held (available count unchanged by the confirm), and its countdown
   stops.
2. **Given** a confirmed reservation, **When** 60+ seconds elapse, **Then** it does NOT expire and
   its units stay held.
3. **Given** a pending reservation that has already expired or been released, **When** the user tries
   to confirm it, **Then** the confirm is rejected with a clear "not found / no longer pending"
   result and no stock changes.
4. **Given** an already-confirmed reservation, **When** confirm is sent again with the same
   idempotency key, **Then** it is a safe no-op returning the same confirmed reservation.

---

### Edge Cases

- **Last-unit stampede**: 50+ concurrent requests for a single remaining unit → exactly one wins;
  the rest get a conflict, never negative stock.
- **Oversell pressure**: 100 concurrent requests for 10 units → exactly 10 succeed, 90 rejected.
- **Duplicate idempotency key, same payload (sequential or parallel)** → one reservation, one
  decrement; identical response both times.
- **Duplicate idempotency key, different payload** → rejected with a typed conflict error.
- **Double release** → second release is a safe no-op; stock returned exactly once.
- **Release after expiration** → accepted as a no-op (or well-defined result); stock not
  double-returned. This covers UI/server clock skew (timer shows 0 but user clicks Release).
- **Expire-vs-release race**: TTL sweep and a manual release fire at the same instant for the same
  reservation → the units return to available exactly once, regardless of which wins.
- **Acting on an already-expired/removed reservation** (release or any interaction) → rejected or
  no-op with a clear result; never a second stock mutation.
- **Reserve on an out-of-stock or zero-total item** → rejected with insufficient-stock error.
- **Invalid quantity** (zero, negative, non-integer, exceeding total) → validation error, no state
  change.
- **Realtime channel drops** → the client reconnects and reconciles to the true backend state
  (it must not display stale stock indefinitely).
- **Missing idempotency key on reserve** → rejected with a `400` validation error; no hold created.
- **Confirm after expiration/release** → rejected with a clear "not found / no longer pending"
  result; no stock change (the units were already returned by the expiration/release).
- **Confirm an already-confirmed reservation (same idempotency key)** → safe no-op returning the
  same confirmed reservation; no double effect.
- **Confirm-vs-expire race**: the TTL sweep and a manual confirm fire at the same instant for the
  same pending reservation → exactly one outcome wins atomically (either it confirms and is kept, or
  it expires and returns stock once) — never both.
- **TTL reset on add**: with `RESET_TTL_ON_ADD` enabled, adding a new pending hold refreshes the
  pending-window countdown; with it disabled, each pending hold keeps its original `creation + TTL`
  expiration. The configured behavior MUST be consistent between the backend sweep and the UI clock.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST display each item with name, total stock, reserved count, and available
  count, where available = total − reserved and is never negative.
- **FR-002**: Users MUST be able to reserve N units of an item, succeeding only if at least N units
  are available at the moment of reservation. A successful reserve creates a **PENDING** hold (a
  pre-reservation) and decrements available immediately. The UI issues one unit per "Reserve Item"
  click, so a user MAY hold multiple pending reservations; the API also accepts a quantity N per
  request.
- **FR-003**: System MUST guarantee that, under arbitrary concurrent load, the sum of active
  reserved units for an item never exceeds its total stock (zero over-sell).
- **FR-004**: System MUST reject reserve requests that exceed available stock or use an invalid
  quantity, with a clear, typed error and no state change.
- **FR-005**: System MUST expire any **PENDING** reservation 60 seconds after its creation (or after
  its last reset window, per FR-017) if it has not been confirmed or released, permanently removing
  it and returning its units to available exactly once. **CONFIRMED reservations MUST NOT expire.**
- **FR-006**: System MUST prevent any interaction with an expired reservation.
- **FR-007**: Users MUST be able to manually release a reservation at any time, including after its
  TTL has elapsed, returning its units to available exactly once.
- **FR-008**: Release MUST be idempotent: repeated releases of the same reservation succeed as a
  well-defined no-op and never return stock more than once.
- **FR-009**: Reserve MUST be idempotent via a **frontend-generated** idempotency key sent in the
  request header (one fresh key per "Reserve Item" action): identical key + payload returns the same
  reservation with a single stock decrement. The key is **required**; a missing key MUST be rejected
  with a `400` error. The backend is responsible for validating, deduplicating, and detecting
  conflicts on the key.
- **FR-010**: System MUST reject a reserve that reuses an idempotency key with a different payload,
  with a clear conflict error.
- **FR-011**: System MUST keep all clients' views of stock and reservations synchronized with the
  backend in near-real-time without requiring a manual refresh.
- **FR-012**: System MUST provide each user a view of their own active reservations, including units
  held and time remaining, with a release action.
- **FR-013**: System MUST surface distinct, user-readable feedback for success, conflict (item
  taken), insufficient stock, invalid quantity, and transient errors, plus loading states for all
  async actions.
- **FR-014**: System MUST expose a documented REST API contract (OpenAPI) describing all endpoints,
  request/response shapes, the idempotency-key mechanism, and error formats.
- **FR-015**: System MUST be reproducibly runnable as a whole (database, backend, frontend) and ship
  with seed inventory data for review.
- **FR-016**: Users MUST be able to **confirm** a pending reservation, transitioning it from PENDING
  to CONFIRMED. Confirming MUST keep the units held (no stock change), stop its TTL, and make the
  reservation non-expiring. Confirm MUST be idempotent (repeating it for an already-confirmed
  reservation is a safe no-op) and MUST be rejected with a clear typed result if the reservation is
  no longer pending (expired/released/not found).
- **FR-017**: System MUST support a configurable `RESET_TTL_ON_ADD` behavior: when enabled, adding a
  new pending reservation resets the pending-window countdown; when disabled, each pending hold keeps
  its original `creation + TTL` expiration. The chosen setting MUST be applied consistently by the
  backend expiration sweep and surfaced to the frontend countdown, and MUST be documented.
- **FR-018**: System MUST identify a user by a **browser-generated UUID persisted for ~1 day**
  (client-side TTL); no authentication or server-side session is in scope. The "my reservations"
  view MUST be scoped to that UUID.

### Key Entities *(include if feature involves data)*

- **Item**: A product available for reservation. Attributes: stable identifier, display name, total
  stock (fixed capacity), reserved count (sum of active reservations). Available is derived as
  total − reserved.
- **Reservation**: A hold by one user on N units of one item. Attributes: identifier, the item, the
  holding user, quantity, status, creation time, expiration time (creation + 60s, only meaningful
  while PENDING). Lifecycle: **PENDING** (pre-reservation, TTL ticking, decrements available) →
  **CONFIRMED** (finalized, units stay held, no longer expires) OR **RELEASED** (returned by the
  user) OR **EXPIRED** (TTL elapsed without confirm/release). CONFIRMED, RELEASED, and EXPIRED are
  terminal for expiration/release/confirm interactions; RELEASED and EXPIRED return the units to
  available exactly once.
- **Idempotency Record**: Links a client-supplied idempotency key to the reservation it produced and
  the payload it was created with, so retries return the same outcome and conflicting reuse is
  detected.
- **User**: The actor holding reservations, identified by a **browser-generated UUID persisted for
  ~1 day** (client-side TTL); no authentication or server-side session in scope. The "my
  reservations" view is scoped to that UUID.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a test firing 100 concurrent reserve requests for an item with 10 units, exactly 10
  succeed and 90 are rejected, with available never below 0.
- **SC-002**: In a test firing 50+ concurrent requests for a single remaining unit, exactly 1
  succeeds.
- **SC-003**: Sending the same reserve (same idempotency key + payload) twice in parallel results in
  exactly 1 reservation and exactly 1 stock decrement.
- **SC-004**: Releasing the same reservation twice returns its units to available exactly once.
- **SC-005**: After another actor changes stock (reserve, release, or expiration), every connected
  client reflects the new available count within ~1 second without a manual refresh.
- **SC-006**: Reservations free their stock no later than ~1 second after their 60-second TTL
  elapses.
- **SC-007**: Each failure mode (conflict, insufficient stock, invalid quantity) produces a distinct,
  user-readable message, and the UI remains usable (no blocked or crashed state) after the error.
- **SC-008**: A confirmed reservation still exists with its units held after 60+ seconds elapse (it
  does not expire), and confirming the same reservation twice (same idempotency key) yields exactly
  one confirmed reservation with no extra stock effect.
- **SC-009**: With `RESET_TTL_ON_ADD` enabled, adding a new pending hold extends the pending-window
  expiration to a fresh TTL; with it disabled, each pending hold expires at its own `creation + TTL`.
  Backend sweep and UI countdown agree on the configured behavior.

## Assumptions

- **Single-branch workflow**: All Spec Kit artifacts and implementation live on `main` in linear,
  workflow-ordered commits, instead of a per-feature branch. Rationale: the evaluation inspects Git
  history for architecture-first ordering, and a linear history tells that story most clearly. (This
  is a documented deviation from Spec Kit's default per-feature-branch flow.)
- **User identity**: A user is identified by a browser-generated UUID persisted client-side for ~1
  day (client TTL); no login/auth or server-side session is in scope. The "my reservations" view is
  scoped to that UUID. (Resolved in `/clarify` 2026-06-03.)
- **Two-phase reservation model**: Clarified with the client (2026-06-03) — a "Reserve Item" click
  creates a temporary PENDING hold, and a separate **Confirm** action finalizes it into a CONFIRMED
  reservation that no longer expires. "Release" returns the unit. This **supersedes** the earlier
  assumption that confirmation was out of scope. A user may hold multiple pending units and confirms
  them one at a time.
- **Idempotency key on reserve**: Frontend-generated (one fresh key per "Reserve Item" action), sent
  in the request header, **required** — a missing key returns `400`. The backend validates,
  deduplicates, and detects key/payload conflicts. (Resolved in `/clarify` 2026-06-03.)
- **TTL reset on add**: The `RESET_TTL_ON_ADD` behavior **defaults to enabled** (adding a pending
  hold resets the pending-window countdown), and is configurable to disabled (each hold keeps its
  own `creation + TTL`). Documented as a configurable non-functional behavior. (Resolved in
  `/clarify` 2026-06-03.)
- **Reservation history retention**: Expired/released reservations may be hard-removed (the brief
  says expiration "permanently removes" them); no audit-history requirement is assumed.
- **Seed data**: A small catalog mirroring the visual reference (e.g., Vintage Camera, Mechanical
  Watch, Acoustic Guitar, Smart Flask, Running Shoes, Gaming Mouse) with varied stock levels,
  including at least one out-of-stock item.
- **Scale**: Sized for a realistic flash sale on a single database instance; horizontal scaling of
  the backend is supported by keeping all correctness guarantees in the database, but multi-region
  is out of scope.
