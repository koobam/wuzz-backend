#!/usr/bin/env bash
# Runs ON the VM (deploy.yml pipes it over ssh): rolls one environment's backend
# services to an image tag.
#
#   deploy-remote.sh <environment> <tag> <registry user>
#
# The registry token arrives in $REGISTRY_TOKEN. The VM is logged in to ghcr.io only
# for the pull and logged out again, so no long-lived credential stays on it.
set -euo pipefail

env_name="${1:?environment}"
tag="${2:?tag}"
registry_user="${3:?registry user}"
: "${REGISTRY_TOKEN:?REGISTRY_TOKEN is required}"

cd "/opt/wuzz/$env_name"
compose() { docker compose --env-file "envs/$env_name.env" -p "wuzz-$env_name" --profile tunnel "$@"; }

# wuzz-support publishes its own image from its own repository; not rolled here.
services="gateway user catalog transaction migrate-user migrate-catalog migrate-transaction"

printf '%s' "$REGISTRY_TOKEN" | docker login ghcr.io -u "$registry_user" --password-stdin >/dev/null
trap 'docker logout ghcr.io >/dev/null 2>&1 || true; rm -f "$HOME/.docker/config.json"' EXIT

sed -i "s/^TAG=.*/TAG=$tag/" "envs/$env_name.env"
# shellcheck disable=SC2086
compose pull $services
compose up -d --no-build --remove-orphans
compose ps
