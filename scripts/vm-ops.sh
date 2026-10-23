#!/usr/bin/env bash
# Runs ON the VM; the deploy workflow installs it as /opt/wuzz/bin/vm-ops.sh.
#
#   vm-ops.sh deploy <env> <tag> <registry user>   roll the backend services to a tag
#   vm-ops.sh migrate <env> <tag> <registry user> migrate only: back up, rehearse, apply; no service is touched
#   vm-ops.sh backup <env> [label]                 dump every database (label: daily, pre-deploy, ...)
#   vm-ops.sh rehearse <env> [tag]                 run the migrations of an image on a COPY of the data
#   vm-ops.sh install-cron <env>                   daily backup at 03:00 WIB
#
# A deploy is: back up, pull, rehearse the migrations on a copy of the real data, and only then
# touch the running services. Either of the first steps failing stops it with nothing changed.
#
# deploy takes the registry token in $REGISTRY_TOKEN. The VM is logged in to ghcr.io only for
# the pull and logged out again, so no long-lived credential stays on it.
set -euo pipefail

cmd="${1:?usage: vm-ops.sh deploy|migrate|backup|rehearse|install-cron <env> ...}"
env_name="${2:?environment}"
shift 2

root=/opt/wuzz
dir="$root/$env_name"
backups="$root/backups/$env_name"
cd "$dir"

compose() { docker compose --env-file "envs/$env_name.env" -p "wuzz-$env_name" --profile tunnel "$@"; }
env_value() { grep -m1 "^$1=" "envs/$env_name.env" | cut -d= -f2-; }

# database -> the service that owns it
dbs="wuzz:user wuzz_catalog:catalog wuzz_transaction:transaction"
keep_pre_deploy=5
keep_daily=7

# </dev/null: exec -T would otherwise read this script's own stdin (the deploy workflow sends the registry token there).
psql_admin() { compose exec -T postgres psql -U wuzz -d postgres -v ON_ERROR_STOP=1 -q "$@" </dev/null; }

backup() { # label
	local label="${1:-daily}" ts keep pair db
	ts="$(date -u +%Y%m%dT%H%M%SZ)"
	case "$label" in pre-deploy) keep=$keep_pre_deploy ;; *) keep=$keep_daily ;; esac
	mkdir -p "$backups" && chmod 700 "$root/backups" "$backups"

	for pair in $dbs; do
		db="${pair%%:*}"
		local out="$backups/$label-$db-$ts.sql.gz"
		if ! compose exec -T postgres pg_dump -U wuzz -d "$db" --no-owner </dev/null | gzip >"$out" || ! gzip -t "$out" || [ "$(stat -c %s "$out")" -lt 200 ]; then
			rm -f "$out"
			echo "backup: $db FAILED" >&2
			return 1
		fi
		# shellcheck disable=SC2012
		ls -1t "$backups/$label-$db-"*.sql.gz | tail -n +$((keep + 1)) | xargs -r rm -f
		echo "backup: $db  $(du -h "$out" | cut -f1)  $out"
	done

	# Chat history (wuzz-support) lives in MongoDB.
	local mongo_out="$backups/$label-mongo-$ts.archive.gz"
	if ! compose exec -T mongo mongodump --quiet --archive --gzip --db wuzz_support </dev/null >"$mongo_out" || [ "$(stat -c %s "$mongo_out")" -lt 20 ]; then
		rm -f "$mongo_out"
		echo "backup: mongo FAILED" >&2
		return 1
	fi
	# shellcheck disable=SC2012
	ls -1t "$backups/$label-mongo-"*.archive.gz | tail -n +$((keep + 1)) | xargs -r rm -f
	echo "backup: mongo  $(du -h "$mongo_out" | cut -f1)  $mongo_out"
}

# Copies each database into a scratch one (a dump piped into a restore: CREATE DATABASE ...
# TEMPLATE needs the source to have no other connections, which a live database never has),
# runs the migrate image on the copy, then drops the copy. Nothing real is touched.
rehearse() { # tag
	local tag="${1:-$(env_value TAG)}" registry password pair db svc scratch dsn failed=0
	registry="$(env_value REGISTRY)"
	password="$(env_value POSTGRES_PASSWORD)"
	local image="$registry/migrate:$tag"
	local network="wuzz-${env_name}_wuzz"

	cleanup() {
		for pair in $dbs; do psql_admin -c "DROP DATABASE IF EXISTS ${pair%%:*}_rehearsal WITH (FORCE)" >/dev/null 2>&1 || true; done
	}
	trap cleanup RETURN

	echo "rehearse: $image on copies of the real data"
	for pair in $dbs; do
		db="${pair%%:*}"; svc="${pair##*:}"; scratch="${db}_rehearsal"
		psql_admin -c "DROP DATABASE IF EXISTS $scratch WITH (FORCE)" -c "CREATE DATABASE $scratch"
		compose exec -T postgres pg_dump -U wuzz -d "$db" --no-owner </dev/null | compose exec -T postgres psql -U wuzz -d "$scratch" -q -v ON_ERROR_STOP=1 >/dev/null
		dsn="postgres://wuzz:${password}@postgres:5432/${scratch}?sslmode=disable"
		if out="$(docker run --rm --network "$network" --entrypoint /app/service \
			-e "APP_ENV=$(env_value APP_ENV)" -e "MIGRATE_SERVICE=$svc" -e "POSTGRES_DSN=$dsn" "$image" 2>&1)"; then
			echo "rehearse: $svc OK ($(echo "$out" | grep -c ' OK ') migration(s) applied to the copy)"
		else
			failed=1
			echo "rehearse: $svc FAILED on a copy of the real data; nothing real was changed:" >&2
			echo "$out" | tail -8 >&2
		fi
	done
	return $failed
}

deploy() { # tag, registry user
	local tag="${1:?tag}" registry_user="${2:?registry user}"
	: "${REGISTRY_TOKEN:?REGISTRY_TOKEN is required}"
	# wuzz-support publishes its own image from its own repository; not rolled here.
	local services="gateway user catalog transaction migrate-user migrate-catalog migrate-transaction"

	echo "== 1. back up"
	backup pre-deploy

	echo "== 2. pull $tag"
	printf '%s' "$REGISTRY_TOKEN" | docker login ghcr.io -u "$registry_user" --password-stdin >/dev/null
	trap 'docker logout ghcr.io >/dev/null 2>&1 || true; rm -f "$HOME/.docker/config.json"' EXIT
	# shellcheck disable=SC2086
	TAG="$tag" compose pull $services

	echo "== 3. rehearse the migrations on a copy of the real data"
	rehearse "$tag"

	echo "== 4. roll the services"
	# TAG goes to compose through the environment (it beats the env file) and is written to the
	# file only after everything came up: a deploy that fails halfway leaves the file at the last
	# good tag. wuzz-support has its own SUPPORT_TAG.
	TAG="$tag" compose up -d --no-build --remove-orphans
	sed -i "s/^TAG=.*/TAG=$tag/" "envs/$env_name.env"
	compose ps

	echo "== 5. clean up"
	# Every deploy pulls a new set of images. Remove the ones no container uses that are a week old
	# or more, so the disk does not fill; this week's stay, for a quick rollback.
	docker image prune -af --filter "until=168h" | tail -1
}

migrate() { # tag, registry user
	local tag="${1:?tag}" registry_user="${2:?registry user}" svc
	: "${REGISTRY_TOKEN:?REGISTRY_TOKEN is required}"

	echo "== 1. back up"
	backup pre-migrate

	echo "== 2. pull the migrate image $tag"
	printf '%s' "$REGISTRY_TOKEN" | docker login ghcr.io -u "$registry_user" --password-stdin >/dev/null
	trap 'docker logout ghcr.io >/dev/null 2>&1 || true; rm -f "$HOME/.docker/config.json"' EXIT
	TAG="$tag" compose pull migrate-user migrate-catalog migrate-transaction

	echo "== 3. rehearse on a copy of the real data"
	rehearse "$tag"

	echo "== 4. apply to the real databases"
	for svc in user catalog transaction; do
		TAG="$tag" compose run --rm --no-deps "migrate-$svc"
	done
}

case "$cmd" in
	deploy) deploy "$@" ;;
	migrate) migrate "$@" ;;
	backup) backup "$@" ;;
	rehearse) rehearse "$@" ;;
	install-cron)
		# 20:00 UTC is 03:00 WIB.
		printf '0 20 * * * root %s/bin/vm-ops.sh backup %s daily >>/var/log/wuzz-backup.log 2>&1\n' "$root" "$env_name" >"/etc/cron.d/wuzz-backup-$env_name"
		chmod 644 "/etc/cron.d/wuzz-backup-$env_name"
		echo "installed /etc/cron.d/wuzz-backup-$env_name"
		;;
	*) echo "unknown command: $cmd" >&2; exit 2 ;;
esac
