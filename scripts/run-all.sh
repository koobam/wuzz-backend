#!/usr/bin/env bash
# Runs the four Go services on this machine in one terminal. Ctrl+C stops all of them.
#
#   scripts/run-all.sh                  reads .env
#   ENV=staging scripts/run-all.sh      reads .env.staging   (same as: make run ENV=staging)
#
# Every line is prefixed with the service name. The services come up through dev-env.sh,
# so they read the env file exactly like `make run SERVICE=<name>`. This does not start
# the infrastructure (`make dev`), the chat service or the web apps (`make local` does all of it).
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

services=(user catalog transaction gateway)
bin="$(mktemp -d "${TMPDIR:-/tmp}/wuzz-run.XXXXXX")"

cleanup() {
	trap - INT TERM EXIT
	# Each service wrote its own pid to a file as it started (see below). Signalling the
	# service itself matters: `$!` of a pipeline is the last command in it, not the service.
	for f in "$bin"/*.pid; do [ -e "$f" ] && kill "$(cat "$f")" 2>/dev/null; done
	wait 2>/dev/null
	rm -rf "$bin"
}
trap cleanup INT TERM EXIT

echo "building ${services[*]} ..."
for svc in "${services[@]}"; do
	go build -o "$bin/$svc" "./cmd/$svc" || { echo "run-all: $svc did not build" >&2; exit 1; }
done

# The local web apps' origins, unless the env file sets its own.
if ! grep -qs '^CORS_ORIGINS=' "${ENV_FILE:-.env}"; then
	export CORS_ORIGINS="http://localhost:5174,http://localhost:5175,http://localhost:5176"
fi

prefix() { # service: one write per line, so lines from different services do not interleave
	# Ctrl+C reaches this too; keep reading until the service closes its output, or its
	# last lines (the shutdown messages) would be lost.
	trap '' INT
	while IFS= read -r line; do printf '[%s] %s\n' "$1" "$line"; done
}

# One banner for all of them instead of four.
scripts/dev-env.sh true

export DEV_ENV_QUIET=1
for svc in "${services[@]}"; do
	# sh records its pid, then execs dev-env.sh, which execs the service: same pid throughout.
	( sh -c 'echo $$ > "$1"; shift; exec "$@"' sh "$bin/$svc.pid" scripts/dev-env.sh "$bin/$svc" ) 2>&1 | prefix "$svc" &
done

echo "running: ${services[*]}   (Ctrl+C stops them)"
wait
