# Spec Kit Notes

Running log of the Spec Kit workflow for the Atomic Inventory reservation system:
commands used, assumptions made, and pivots taken. Written as it happens, per
Constitution Principle VI (Traceability).

## LLM used

- **Claude Opus 4.8 (1M context)** via Claude Code CLI.
- Why: strong long-context reasoning for keeping the constitution, spec, plan, and
  tasks coherent across a multi-phase workflow, and reliable Go/PostgreSQL
  concurrency reasoning (the highest-risk part of this challenge).

## Environment bootstrap

- Spec Kit was **not** installed globally; ran it through `uvx` (no global install):
  `uvx --from git+https://github.com/github/spec-kit.git specify init --here --integration claude --force`
- Go was not installed → installed `go1.26.3` via Homebrew (`brew install go`).
- Node v22.11.0 / npm 11.5.1 and Docker 28.5.1 / Compose v2.40 were already present.

## Phase log

### 0 — Init (`specify init`)
- Initialized Spec Kit in the existing repo (claude integration). Templates, the git
  extension, and workflow skills were installed under `.specify/` and `.claude/skills/`.
- **Assumption**: the challenge brief PDF carries the originating company name, so it is
  git-ignored and never published (challenge NOTE 1). The repo name is `inventory-go`.
- Commit: `chore: scaffold Spec Kit workflow via specify init`.

### 1 — Constitution (`/speckit-constitution`)
- Authored 7 non-negotiable principles. The two marked NON-NEGOTIABLE are
  **Correctness Under Concurrency** (atomic DB writes, zero over-sell, proven by load
  tests) and **Test-First on Critical Paths**.
- **Decision**: realtime sync via **WebSockets** (chosen over polling/SSE) to maximize
  the "perfectly in sync" rubric criterion.
- Version 1.0.0, ratified 2026-06-02.
- Commit: `docs: add project constitution v1.0.0`.

## Assumptions (consolidated)

- A "user" is identified by a client-supplied identifier (no auth system in scope); the
  reservation list is scoped per user id. To be confirmed/refined in `/specify`.
- TTL is fixed at 60s as stated; expiration permanently removes the reservation.
- Seed data: a handful of items mirroring the visual reference (Vintage Camera,
  Mechanical Watch, etc.) with varied stock levels including an out-of-stock item.

## Pivots

- _(none yet)_
