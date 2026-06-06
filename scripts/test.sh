#!/usr/bin/env bash
#
# Run the test suites INSIDE containers using docker-compose.test.yml.
#
#   ./scripts/test.sh            # backend + frontend, then tear down
#   ./scripts/test.sh backend    # backend only
#   ./scripts/test.sh frontend   # frontend only
#
# Exit code is non-zero if any selected suite fails.
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE="docker compose -f docker-compose.test.yml"
TARGET="${1:-all}"
status=0

cleanup() {
  echo "==> Tearing down test stack"
  $COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

run() {
  echo "==> Running ${1} tests"
  $COMPOSE run --rm "${1}-test" || status=1
}

case "$TARGET" in
  backend)  run backend ;;
  frontend) run frontend ;;
  all)      run backend; run frontend ;;
  *) echo "usage: $0 [backend|frontend|all]" >&2; exit 2 ;;
esac

if [ "$status" -eq 0 ]; then
  echo "==> ✅ All selected suites passed"
else
  echo "==> ❌ Some suites failed"
fi
exit "$status"
