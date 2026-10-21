#!/usr/bin/env bash
# Loads the demo data into a database that has been migrated.
#
#   scripts/seed.sh            catalog: outlets, services, service areas
#   scripts/seed.sh member     the demo customer for the app's member card (dev only)
#
# The data is fake. It refuses to run unless APP_ENV is unset, "dev" or "staging".
#
# The database comes from CATALOG_DSN (member: MEMBER_DSN), defaulting to the local
# docker-compose Postgres. Needs `psql`, or a running local compose stack.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
what="${1:-catalog}"

case "${APP_ENV:-dev}" in
  dev|staging) ;;
  *) echo "seed: refusing to load demo data into APP_ENV=${APP_ENV}" >&2; exit 1 ;;
esac

case "$what" in
  catalog) file="$here/seed-local.sql";        dsn="${CATALOG_DSN:-postgres://wuzz:wuzz@localhost:5433/wuzz_catalog?sslmode=disable}"; db=wuzz_catalog ;;
  member)
    [ "${APP_ENV:-dev}" = dev ] || { echo "seed: the demo member is for dev only" >&2; exit 1; }
    file="$here/seed-member-local.sql";        dsn="${MEMBER_DSN:-postgres://wuzz:wuzz@localhost:5433/wuzz?sslmode=disable}"; db=wuzz ;;
  *) echo "usage: scripts/seed.sh [catalog|member]" >&2; exit 2 ;;
esac

if command -v psql >/dev/null 2>&1; then
  psql "$dsn" -v ON_ERROR_STOP=1 -q -f "$file"
elif docker compose -f "$here/../docker-compose.yml" ps --status running postgres 2>/dev/null | grep -q postgres; then
  docker compose -f "$here/../docker-compose.yml" exec -T postgres psql -U wuzz -d "$db" -v ON_ERROR_STOP=1 -q <"$file"
else
  echo "seed: need psql, or the local compose stack running (make dev)" >&2
  exit 1
fi
echo "seeded $what"
