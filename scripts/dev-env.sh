#!/bin/sh
# Sets up the environment for running one service on this machine, then execs it.
#
#   scripts/dev-env.sh <command...>
#
# Reads .env (gitignored) first, so a value there wins over the defaults in
# internal/pkg/config. Point at another file with ENV_FILE, e.g. to run a service
# on this machine against staging's addresses:
#
#   ENV_FILE=.env.staging make run SERVICE=gateway
#
# Anything already exported in your shell still beats the file, so a one-off like
# `HTTP_ADDR=:9999 make run SERVICE=gateway` keeps working.
set -eu

file="${ENV_FILE:-.env}"

if [ -f "$file" ]; then
	# Source the file, then put back anything the shell had already exported: the
	# file supplies values, it does not override ones you typed on the command line.
	snapshot="$(export -p)"
	set -a
	# shellcheck disable=SC1090
	. "./$file"
	set +a
	eval "$snapshot"
	loaded="$file"
else
	loaded="none (using the defaults in config.go)"
fi

printf '\n'
printf '  env file  %s\n' "$loaded"
printf '  app env   %s\n' "${APP_ENV:-dev}"
printf '  postgres  %s\n' "${POSTGRES_DSN:-per-service default (localhost:5433)}"
printf '  redis     %s\n' "${REDIS_ADDR:-localhost:6380}"
printf '  kafka     %s\n' "${KAFKA_BROKERS:-localhost:29092}"
printf '\n'

exec "$@"
