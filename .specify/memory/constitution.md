<!--
SYNC IMPACT REPORT
==================
Version change: (template) → 1.0.0
Bump rationale: Initial ratification of the project constitution (MAJOR baseline).
Modified principles: all placeholders replaced with concrete, project-specific principles.
Added sections:
  - Core Principles (I–VII)
  - Technology Constraints
  - Development Workflow & Quality Gates
  - Governance
Removed sections: none.
Templates requiring updates:
  - .specify/templates/plan-template.md ............ ✅ reviewed (Constitution Check aligns)
  - .specify/templates/spec-template.md ............ ✅ reviewed (mandatory sections compatible)
  - .specify/templates/tasks-template.md ........... ✅ reviewed (test-first categories supported)
Deferred TODOs: none.
-->

# Atomic Inventory Constitution

A Stock Reservation System for high-traffic flash sales. These principles are
NON-NEGOTIABLE constraints that every specification, plan, task, and line of code
MUST satisfy. When a downstream artifact conflicts with this constitution, the
constitution wins.

## Core Principles

### I. Architecture-First (Spec-Driven Development)

No application code is written before the architecture is documented, validated,
and refined. The repository MUST contain, in this causal order recorded in Git
history: `constitution` → `spec.md` → (`clarify`) → `plan.md` → `tasks.md` →
implementation. Every implementation commit MUST trace back to a task. A change
in scope MUST start by amending the spec, not the code.

Rationale: In a double-sell-sensitive system, design errors are far cheaper to
fix on paper than after they have corrupted stock. The evaluator inspects Git
history to confirm this order; so do real incident post-mortems.

### II. Correctness Under Concurrency (NON-NEGOTIABLE)

Stock MUST never be over-sold. `available = total - reserved` MUST hold at all
times and MUST never go negative, regardless of concurrent load. Every mutation
of reserved stock MUST be atomic at the database layer — a single conditional
write that both checks availability and decrements it, never a read-then-write
across two statements. The system MUST be proven correct by load tests that
simulate at least 50 simultaneous requests for the last unit and 100 concurrent
requests for 10 units (exactly 10 succeed, 90 are rejected, no negative stock).

Rationale: Application-level locks and read-then-write logic silently break under
real concurrency. The only trustworthy guarantee comes from the database's own
atomicity and isolation, exercised by adversarial load tests.

### III. Idempotency by Design

`reserve` and `release` MUST be idempotent. `POST /reservations` MUST honor an
`Idempotency-Key` header: two requests with the same key and same payload return
the same reservation (same id, same outcome) and decrement stock at most once;
the same key with a different payload MUST be rejected with a clear, typed error.
`DELETE /reservations/:id` MUST be safe to call repeatedly: releasing an already
released or expired reservation succeeds as a well-defined no-op and returns
stock to the pool at most once.

Rationale: Networks retry, users double-click, and frontends deliver at-least-once.
Without idempotency, every retry is a potential double-decrement or double-refund.

### IV. Test-First on Critical Paths (NON-NEGOTIABLE)

The concurrency and idempotency guarantees of Principles II and III MUST be backed
by automated tests committed alongside (or before) the code they cover. The
mandatory backend suite includes: last-unit contention, the 100→10 oversell test,
parallel-duplicate-key reserve idempotency, and double-release idempotency. The
frontend MUST test reservation-timer logic (unit) plus at least one reserve
happy-path and one error-state component test. A critical-path feature is not
"done" until its test exists and passes.

Rationale: Concurrency bugs are non-deterministic; only repeatable adversarial
tests turn "seems fine" into "proven correct."

### V. Synced, Observable State

The UI MUST reflect backend truth in near-real-time without manual refresh.
Stock-level and reservation-status changes (including TTL expiration) MUST be
pushed to clients over WebSockets, with graceful reconnection. Every asynchronous
action MUST surface explicit loading, success, and error states; conflicts (an
item taken by someone else) MUST be shown clearly to the user.

Rationale: A flash sale where the screen lies about availability creates the exact
double-sell frustration the system exists to prevent.

### VI. Traceability & Clean History

Implementation MUST map 1:1 to `tasks.md`. Commits MUST be small, conventional,
and follow the workflow order. Assumptions and pivots MUST be recorded in
`spec-kit-notes.md` as they happen, not reconstructed afterward.

Rationale: Traceable history is how reviewers (and future maintainers) verify that
the system that shipped is the system that was designed.

### VII. Simplicity & Latest Stable Tooling (YAGNI)

Prefer the simplest design that satisfies the principles above. Use current stable
versions of the mandated stack. Add abstraction only when a concrete requirement
demands it. The whole system MUST be runnable with a single dockerized command.

Rationale: Complexity is where race conditions and idempotency gaps hide.

## Technology Constraints

- **Backend**: Go (latest stable) with the standard library or a lightweight router
  (Chi/Gin).
- **Database**: PostgreSQL. Race conditions MUST be handled at this layer.
- **Frontend**: React + Vite + TypeScript (latest stable).
- **Realtime**: WebSockets for stock/reservation synchronization.
- **Packaging**: Docker + Docker Compose MUST bring up the full stack (db, backend,
  frontend) reproducibly. Seed data MUST be provided for review.
- **Contract**: A complete OpenAPI specification MUST describe the REST API.
- **Naming**: The repository and artifacts MUST NOT contain the originating company
  name.

## Development Workflow & Quality Gates

1. Each Spec Kit phase produces its artifact and a dedicated commit before the next
   phase begins.
2. `spec.md` MUST enumerate and resolve edge cases (expiration races, double release,
   concurrent last-unit, idempotency-key reuse, clock skew) — not only the happy path.
3. No implementation task may merge without its mandated tests passing.
4. The reservation TTL is 60 seconds; expired reservations are permanently removed,
   cannot be interacted with, and return their units to the available pool exactly once.
5. Manual release MUST work at any time, including after the TTL has elapsed.

## Governance

This constitution supersedes ad-hoc decisions. Amendments require: a written
rationale, a semantic version bump, and propagation to dependent templates and
artifacts. Versioning: MAJOR for principle removals/redefinitions, MINOR for new
principles or materially expanded guidance, PATCH for clarifications. Every plan
and task review MUST verify compliance with Principles I–VII; a violation is a
blocking defect, not a style nit.

**Version**: 1.0.0 | **Ratified**: 2026-06-02 | **Last Amended**: 2026-06-02
