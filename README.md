# wuzz-backend

Go backend for wuzz, replacing the current production backend `apps/wuzz`
(customer app) talks to, and serving the POS and back-office. Architecture
mirrors `reddog-backend`: a public `gateway` (REST, gin) in front of internal
gRPC services, hexagonal-shaped (`domain` / `app` / `adapters` / `ports`),
Postgres via pgx (one database per service), Redis for sessions and one-time
codes, Kafka via the transactional outbox, Ed25519 JWTs signed only by `user`.

| Service | Owns | Notes |
| --- | --- | --- |
| `gateway` | REST edge | routes grouped by audience, see below |
| `user` | accounts, sessions, staff roles | phone + SMS code, no passwords |
| `catalog` | outlets, laundry services, service areas | public reads (menu and areas cached 30 s), admin writes; all in Postgres |
| `transaction` | orders | idempotent create, status lifecycle, outlet scoping |

Chat and calls live in the sibling `wuzz-support` (Elixir/Phoenix + MongoDB).

## REST API (gateway)

Responses use one envelope: `{ "success": bool, "message": str, "data": … }`.

| Prefix | Audience | Auth |
| --- | --- | --- |
| `/app/v1` | customers (mobile app) | public catalog reads; `me` needs a token |
| `/backoffice/v1` | administrators | role `admin` |
| `/pos/v1` | outlet staff | role `outlet:<id>` (or `admin`) |
| `/station/v1` | Wuzzers: the technician who is also the driver (station app) | role `wuzzer` (or `admin`) |

`/app/register`, `/app/verify-otp`, … (unversioned) are the shipped mobile
client's contract and stay as they are; `/app/v1/*` is the same API for new
clients.

- **Staff sign-in** (both `/backoffice/v1` and `/pos/v1`): `POST /auth/send-code {phone}` →
  `POST /auth/verify-otp {phone, otp_code}` → `{user, access_token, refresh_token, expires_at}`;
  `POST /auth/refresh`, `POST /auth/logout {refresh_token}`, `GET /auth/me`.
  A customer's number gets `200` on `send-code` (no SMS is sent, so staff numbers cannot be probed)
  and `403` on `verify-otp`.
- **`/app/v1`**: `GET /outlets`, `GET /services`, plus the auth routes above.
- **`/pos/v1`**: `GET /catalog` (outlets + services to ring up an order), `POST /orders`
  (idempotent on `id`: `201` new, `200` already there), `GET /orders`, `GET /orders/:id`,
  `PATCH /orders/:id/status`.
- **`/backoffice/v1`**: `GET /users`, `PATCH /users/:id/status`; `GET|POST /outlets`, `PATCH /outlets/:id`;
  `GET|POST /services`, `PATCH /services/:id`; `GET /orders`, `GET /orders/:id`, `PATCH /orders/:id/status`.

Order lifecycle: `created → processing → ready → fulfilled`, or `cancelled` before `ready`.
The server recomputes totals from the lines; prices are the ones the POS sold at.

## Staff accounts

Staff sign in with phone + code like everyone else; roles come from the database.

Roles ride in the access token (`roles` claim): `admin`, `outlet:<id>`, `wuzzer`. wuzz-support
authorises chat against the same claim (it does not know `wuzzer` yet: a Wuzzer is treated as a
customer there).

Whoever has signed in once can be given a role in the `users` table of the `wuzz` database:

```sql
UPDATE users SET roles = '{wuzzer}'    WHERE phone = '628...';   -- a Wuzzer (station app)
UPDATE users SET roles = '{outlet:3}'  WHERE phone = '628...';   -- staff of outlet 3 (POS)
UPDATE users SET roles = '{admin}'     WHERE phone = '628...';   -- back office (also allowed in POS and station)
```

The roles apply from the next sign-in, since they are written into the token.

## Activity log

Every state-changing call made through `/backoffice/v1` or `/pos/v1` (create, update or delete of an outlet, service, variant or modifier group, material or recipe; stock receipts, waste, counts, transfers and reorder levels; an order made or moved on; a user's status; signing in and out) leaves one row in the gateway's own database, `wuzz_gateway`, table `activity_log`. The table is append-only (a trigger refuses UPDATE and DELETE). The list of what is logged, and how each route is described, is the table in `internal/services/gateway/activity/rules.go`: a new state-changing route is not logged until it is added there.

The gateway writes it after the response, off the request's path, so the log never slows or fails a call. An entry that cannot be stored (queue full, database down through three retries) is logged at ERROR as `ACTIVITY LOG ENTRY LOST` with its full content and counted in `wuzz_activity_lost_total{reason}`: alert on that counter. Request bodies are kept with secrets removed (any key with token, password, secret, otp, pin, refresh or authorization in it) and long strings cut; sign-in bodies are not kept at all.

`GET /backoffice/v1/activity` (admins) reads it back: `entity_type`, `entity_id`, `actor_id`, `action`, `outcome` (`ok` or `failed`), `audience`, `q` (actor name, entity id or summary), `from` / `to` (days, `YYYY-MM-DD`, Asia/Jakarta, both included), `sort_by` (`created_at`, `action`, `entity_type`, `actor_name`, `outcome`; newest first by default), `sort_dir`, `limit` (25, at most 100), `offset`. The answer is `{items, total}`.

The gateway now needs `POSTGRES_DSN` (its own database, created like the other services' and migrated with `MIGRATE_SERVICE=gateway`; `migrate-gateway` in docker-compose does it).

## Pictures (uploads)

`POST /backoffice/v1/uploads` (admin) takes one picture as `multipart/form-data` in the field `file` (optional `folder`, only `outlets`
for now) and answers, in the usual envelope, `{url, key, file_name, content_type, size, width, height}`. Put the `url` in an outlet's `images`.

- **What is accepted** is decided by the bytes, never by the file name or the declared type: JPEG, PNG, WebP, AVIF; at most 5 MB
  (`MEDIA_MAX_UPLOAD_BYTES`), 8000 pixels on a side and 25 megapixels (the size is read from the header before anything is decoded, so a
  small file cannot unpack into gigabytes). Refused with 400 (empty, damaged, too many pixels, bad folder), 413 (too big), 415 (not a picture).
- **Metadata:** a JPEG is re-encoded with its orientation applied and EXIF/GPS left out; a PNG is re-encoded (text and profile chunks drop).
  WebP and AVIF are stored as sent (the standard library cannot decode them), so their metadata stays.
- **Keys** are `outlets/<yyyy>/<mm>/<32 random hex>.<ext>`: nothing the client sent is in them. Objects are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- **Where they go:** an S3-compatible bucket (`MEDIA_ENDPOINT`, `MEDIA_BUCKET`, `MEDIA_ACCESS_KEY_ID`, `MEDIA_SECRET_ACCESS_KEY`,
  `MEDIA_REGION`, `MEDIA_PUBLIC_BASE_URL`; requests are signed with AWS Signature V4 by the standard library). Cloudflare R2 and Google
  Cloud Storage's S3 interoperability API (HMAC keys) both work; staging and production use the GCS buckets the assets worker serves
  (`assets-stg.wuzz.care`, `assets.wuzz.care`; see `wuzz-deployments/cloudflare`). Nothing set means 503 "Uploads are not configured";
  a half-set bucket stops the gateway at start. There is no quiet fallback.
- **Locally** no cloud account is needed: `MEDIA_LOCAL_DIR=.data/media` (dev only, refused in staging and production) keeps the files
  on disk and the gateway serves them read-only at `http://localhost:8092/media/...` (`MEDIA_PUBLIC_BASE_URL`). A MinIO container
  would be the closer copy of a real bucket, but its images are no longer published to Docker Hub.
- **Outlet pictures** must be `https` addresses, except `http` on `localhost`/`127.0.0.1`/`::1`, so the local `/media` addresses are
  accepted by the outlet validation (a loopback address is never reachable by a customer).
- The upload is written to the activity log (`media` / `create`, with the file name, type and size, never the bytes).

## Search (Typesense)

The list endpoints that take a search, a sort and filters are answered by Typesense: orders (transaction service), and outlets, services,
materials, variant groups and modifier groups (catalog service). **Postgres stays the source of truth.** Typesense is a derived copy: a
search returns only the ids of the page and the total, and the rows are read back from Postgres in that order, so a row never drifts from
the database.

- **Config**: `TYPESENSE_URL`, `TYPESENSE_API_KEY` on the catalog and transaction services (local: `http://localhost:8109`, key in `.env.example`;
  `TYPESENSE_RECONCILE_INTERVAL`, default 5m). With no URL the lists read Postgres directly, as before. With a URL, a service starts only once Typesense
  answers, and **a search that cannot reach it is a 503** ("Search is temporarily unavailable") — it never falls back to Postgres.
- **Consistency**: after every create/update/delete the row is re-read and written to the index right after the commit, so a list shows a
  just-saved record at once (a service change also refreshes the groups it links, whose "used by" is part of their document). A failed write is
  logged loudly (`search index write failed`) and repaired by the reconcile, which every few minutes upserts every row and drops documents whose row is gone.
- **Rebuild**: a service fills any collection that is missing, empty or at an older schema version when it starts. By hand:
  `make reindex SERVICE=catalog [COLLECTIONS="outlets services"]`, `make reindex SERVICE=transaction`. A rebuild fills a new collection, moves the alias
  (`catalog_outlets` -> `catalog_outlets_v1`) and drops the old one, so searches keep working. Changing a collection's fields means bumping
  `SchemaVersion` in `adapters/searchindex`.
- **Matching**: text is matched inside words, ignoring case, with one typo forgiven in words of five letters or more (`detergen` finds `Deterjen`).
  An order summary that has a text search counts the orders the index found, so it always agrees with the list.
- **Outlets and services**: `GET /backoffice/v1/outlets` and `/services` return the whole list as a bare array (the dashboard map and the pickers read it)
  until `limit` is given; then they return `{items, total}` and take `q`, `sort_by`, `sort_dir`, `limit`, `offset`, `active=true|false`, and for services
  `category_id` and `unit`. `sort_by` is `id, name, address, phone, active` for outlets and `id, name, unit, price, category_id, active` for services.

## Events

Every write that matters appends an event to the service's `outbox` table in the same
transaction; a relay in each command service drains it to Kafka (`LISTEN/NOTIFY` wake-up,
±20% jittered backstop timer, `FOR UPDATE SKIP LOCKED` so every replica can run one).
Topics (prefixed `wuzz.`): `user.events`, `outlet.events`, `service.events`, `order.events`.
Delivery is at-least-once; consumers dedupe on the envelope `id`.

## Local development

Everything on one machine, one command:

```
make local          # infra, migrations, all Go services, support, POS/backoffice/landing
make local-status
make local-down
```

| | URL |
| --- | --- |
| API (gateway) | http://localhost:8092 |
| Chat / calls (wuzz-support) | http://localhost:4000 |
| POS | http://localhost:5175 |
| Backoffice | http://localhost:5174 |
| Landing | http://localhost:5176 |

The catalog (outlets, the service menu, service areas) comes from migrations, so a migrated database already has it.
Staff roles are set in the `users` table.
No Twilio needed locally: the one-time code is logged by `user` (`/tmp/wuzz-local/user.log`,
look for `verification code`). Logs for every process are in `/tmp/wuzz-local/`.

Needs Docker, Go, pnpm, and the sibling repos `wuzz-support` and `wuzz-frontend`.

By hand: `make dev` (postgres, redis, kafka, mongo + migrations), then
`make run SERVICE=<user|catalog|transaction|gateway>` in separate terminals.

`make run`, `make migrate` and `make local` read `.env` first (gitignored; copy
`.env.example`), so a value there wins over the defaults in `internal/pkg/config`.
What you export in your shell still wins over the file.

### Several environments on one machine

`ENV` picks the env file, so the same checkout can run as another environment:

```
make run SERVICE=gateway                 # one service, reads .env
make run SERVICE=gateway ENV=staging     # reads .env.staging
make run                                 # no SERVICE: user, catalog, transaction and gateway in one terminal
make migrate                             # no SERVICE: the user, catalog and transaction databases
make migrate SERVICE=user ENV=staging
make local ENV=staging                   # everything: infra, services, chat, web apps
```

`make run` without `SERVICE` prefixes every line with the service name and stops all four on Ctrl+C.
It needs the infrastructure already up (`make dev`); `make local` starts that too, plus the chat and
the web apps.

Each `ENV` has its own run directory (`/tmp/wuzz-local-staging`), so their pid files and logs do
not mix. An `ENV` whose file does not exist is an error, not a silent fall back to the defaults.

Every dependency is its own line in the file, so a file can mix: Postgres on this machine while
Redis or Kafka point somewhere else is just two lines. A value you export in the shell still beats
the file (`HTTP_ADDR=:9999 make run SERVICE=gateway ENV=staging`).

Two environments at the same time need different ports, since the defaults are fixed
(`HTTP_ADDR`, `GRPC_ADDR` and the infra ports in `docker-compose.yml`).

`.env` is for local, `.env.staging` and `.env.production` are for those
environments (the VM reads them through Docker Compose), and neither of those is
ever committed.
Or the whole stack in containers: `make up` (then `make down` / `make reset`).

## Seeing a change on staging

Same idea as the branch picker on reddog's jobs, with GitHub Actions:

```
feature branch -> PR to develop -> CI -> (Deploy workflow) -> staging runs it -> look at it -> merge
```

1. Work on a branch (`feature/<what>`), open a PR to `develop`. CI runs on the PR.
2. To look at it on staging: **Actions > Deploy > Run workflow**. Under *Use workflow from*
   pick your branch, environment `staging`, leave *tag* empty. It builds your branch's images
   and rolls staging to them. API: https://api-stg.wuzz.care (Swagger at `/docs`).
3. Staging keeps running that branch until the next deploy. Merging to `develop` puts develop
   back (automatically after the publish when `AUTO_DEPLOY_STAGING=true`), and so does the
   08:00 WIB weekday schedule.
4. Production never builds. It deploys a tag you name, one staging has already run.

Images are tagged with the commit and with the branch name (`feature-x`); only `develop` moves
`:latest`. One staging runs one version at a time. To try something without taking staging, run it
locally (below).

Needs the repository secrets `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY` (see `deploy.yml`).

## Backups, and trying a migration before it is real

`scripts/vm-ops.sh` lives on the VM at `/opt/wuzz/bin/` (the deploy workflow installs it) and does
three things, all with Postgres' own tools and no extra service:

| | |
| --- | --- |
| `backup <env> [label]` | `pg_dump` of every database and a `mongodump` of the chat, gzipped into `/opt/wuzz/backups/<env>/`, each checked before it counts. Keeps the last 5 `pre-deploy` and 7 of every other label. |
| `rehearse <env> [tag]` | Copies each database into a scratch one, runs the migrate image of that tag on the **copy**, reports, and drops the copy. (`CREATE DATABASE ... TEMPLATE` does not work on a live database: it needs the source to have no other connections.) |
| `install-cron <env>` | A daily backup at 03:00 WIB (`/etc/cron.d/wuzz-backup-<env>`). |

Every deploy runs them in order: **back up, pull, rehearse the migrations on a copy of the real data,
and only then roll the services.** If the backup or the rehearsal fails, nothing running was touched.
The rehearsal also catches a migration that works on an empty database and fails on real rows.

To bring a backup back (a scratch database first, to look before you overwrite):

```
gunzip -c /opt/wuzz/backups/staging/pre-deploy-wuzz_catalog-<time>.sql.gz | \
  docker compose -p wuzz-staging --env-file envs/staging.env exec -T postgres psql -U wuzz -d <scratch database>
```

The backups stay on the VM, so they do not survive losing it; copying them off the machine (to a
private bucket, never the public media one) is the next step. Old backup files and unused Docker
images (a week old) are pruned automatically.

## Regenerating gRPC code after editing a `.proto`

```
make tools   # once, installs buf + protoc-gen-go + protoc-gen-go-grpc
make proto
```

Generated code under `gen/` is committed, so a fresh clone builds with
`go build ./...` alone.
