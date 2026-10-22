#!/bin/sh
# Sets up the environment for running one service on this machine, then execs it.
#
#   scripts/dev-env.sh <command...>
#
# Reads .env (gitignored) first, so a value there wins over the defaults in
# internal/pkg/config. Pick another environment with ENV (the Makefile turns it into
# ENV_FILE=.env.<ENV>), e.g. to run a service on this machine against staging's addresses:
#
#   make run SERVICE=gateway ENV=staging       # reads .env.staging
#
# Every dependency is chosen on its own line, so an env file can mix: Postgres local while
# Redis comes from somewhere else is just two lines. A file you named with ENV that does not
# exist is an error, never a silent fall back to the defaults.
#
# Anything already exported in your shell still beats the file, so a one-off like
# `HTTP_ADDR=:9999 make run SERVICE=gateway` keeps working.
set -eu

file="${ENV_FILE:-.env}"

if [ -n "${ENV:-}" ] && [ ! -f "$file" ]; then
	echo "dev-env: ENV=$ENV but $file does not exist (copy .env.example, or .env.${ENV}.example if there is one)" >&2
	exit 1
fi

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
