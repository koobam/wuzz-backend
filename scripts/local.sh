#!/usr/bin/env bash
# Run the whole Wuzz stack on this machine.
#
#   scripts/local.sh up       start infra, migrate, then every service and app
#   scripts/local.sh down     stop everything this script started
#   scripts/local.sh status   what is listening where
#   scripts/local.sh logs     tail every log
#
# Expects the sibling repos wuzz-support and wuzz-frontend next to wuzz-backend.
set -euo pipefail

BACKEND="$(cd "$(dirname "$0")/.." && pwd)"
ROOT="$(dirname "$BACKEND")"
SUPPORT="$ROOT/wuzz-support"
FRONTEND="$ROOT/wuzz-frontend"
# One run directory per ENV, so `make local ENV=staging` and the default do not share pid files.
RUN="${WUZZ_RUN_DIR:-/tmp/wuzz-local${ENV:+-$ENV}}"
mkdir -p "$RUN"

PSQL=(docker compose -f "$BACKEND/docker-compose.yml" exec -T postgres psql -U wuzz -v ON_ERROR_STOP=1)

# name:port  (ports are what the apps and gateway default to in dev)
PORTS="user:9090 catalog:9091 transaction:9092 gateway:8092 support:4000 pos:5175 backoffice:5174 landing:5176"

port_of() { for p in $PORTS; do [ "${p%%:*}" = "$1" ] && echo "${p##*:}"; done; }

wait_port() { # name
  local port; port="$(port_of "$1")"
  for _ in $(seq 1 60); do nc -z localhost "$port" 2>/dev/null && return 0; sleep 1; done
  echo "!! $1 did not open :$port (see $RUN/$1.log)"; return 1
}

start() { # name, command...
  local name="$1"; shift
  if nc -z localhost "$(port_of "$name")" 2>/dev/null; then echo "   $name already up"; return; fi
  ( "$@" >"$RUN/$name.log" 2>&1 & echo $! >"$RUN/$name.pid" )
  wait_port "$name" && echo "   $name :$(port_of "$name")"
}

up() {
  echo "== env: ${ENV:-default} (${ENV_FILE:-.env})"
  echo "== infra"
  (cd "$BACKEND" && docker compose up -d postgres redis kafka mongo >/dev/null)
  until "${PSQL[@]}" -c 'select 1' >/dev/null 2>&1; do sleep 1; done
  for db in wuzz_catalog wuzz_transaction; do
    # Captured first: `grep -q` in a pipeline can SIGPIPE psql, which pipefail reports as "not found".
    exists="$("${PSQL[@]}" -tAc "select 1 from pg_database where datname='$db'")"
    [ "$exists" = "1" ] || "${PSQL[@]}" -c "create database $db" >/dev/null
  done

  # A fresh local database gets the seeded admin (see migrations/user/*_seed_staging_admin.sql).
  "${PSQL[@]}" -d postgres -c "ALTER DATABASE wuzz SET wuzz.seed_admin = 'on'" >/dev/null

  echo "== migrate"
  for svc in user catalog transaction; do
    (cd "$BACKEND" && MIGRATE_SERVICE=$svc scripts/dev-env.sh go run ./cmd/migrate 2>&1 | grep -E "env file|migrated|no migrations|ERROR" || true)
  done
  echo "   admin: 081234500099 (backoffice, pos, station) on a fresh database. Code is printed in $RUN/user.log"

  echo "== go services"
  (cd "$BACKEND" && go build -o "$RUN/bin-user" ./cmd/user && go build -o "$RUN/bin-catalog" ./cmd/catalog \
    && go build -o "$RUN/bin-transaction" ./cmd/transaction && go build -o "$RUN/bin-gateway" ./cmd/gateway)
  # The local web apps' origins, unless the env file sets its own.
  if ! grep -qs '^CORS_ORIGINS=' "$BACKEND/${ENV_FILE:-.env}"; then
    export CORS_ORIGINS="http://localhost:5174,http://localhost:5175,http://localhost:5176"
  fi
  # Each service starts through dev-env.sh, so it reads the env file (.env, or
  # .env.$ENV) exactly like `make run` does.
  for svc in user catalog transaction gateway; do
    start "$svc" bash -c "cd '$BACKEND' && exec scripts/dev-env.sh '$RUN/bin-$svc'"
  done

  echo "== support (docker)"
  docker rm -f wuzz-support >/dev/null 2>&1 || true
  if nc -z localhost 4000 2>/dev/null; then echo "   :4000 busy, skipping support"; else
    docker run -d --name wuzz-support -v "$SUPPORT":/app -w /app -p 4000:4000 \
      hexpm/elixir:1.17.3-erlang-27.1.2-debian-bookworm-20241016-slim \
      sh -c "mix local.hex --force >/dev/null && mix local.rebar --force >/dev/null && mix deps.get >/dev/null && mix phx.server" >/dev/null
    wait_port support && echo "   support :4000"
  fi

  echo "== frontends"
  for app in pos backoffice landing; do
    start "$app" bash -c "cd '$FRONTEND/apps/$app' && exec pnpm dev --host localhost"
  done

  echo
  echo "POS        http://localhost:5175"
  echo "Backoffice http://localhost:5174"
  echo "Landing    http://localhost:5176"
  echo "API        http://localhost:8092   Chat http://localhost:4000"
}

down() {
  for f in "$RUN"/*.pid; do [ -e "$f" ] || continue; pkill -P "$(cat "$f")" 2>/dev/null || true; kill "$(cat "$f")" 2>/dev/null || true; rm -f "$f"; done
  for name in user catalog transaction gateway pos backoffice landing; do
    for pid in $(lsof -tiTCP:"$(port_of "$name")" -sTCP:LISTEN 2>/dev/null); do kill "$pid" 2>/dev/null || true; done
  done
  docker rm -f wuzz-support >/dev/null 2>&1 || true
  (cd "$BACKEND" && docker compose stop >/dev/null) && echo "stopped (data kept; 'make reset' wipes it)"
}

status() { for p in $PORTS; do printf '%-12s :%-5s %s\n' "${p%%:*}" "${p##*:}" "$(nc -z localhost "${p##*:}" 2>/dev/null && echo up || echo down)"; done; }

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  logs) tail -n 20 -F "$RUN"/*.log ;;
  *) echo "usage: $0 up|down|status|logs"; exit 1 ;;
esac
