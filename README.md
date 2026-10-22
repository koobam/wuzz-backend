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

`/app/register`, `/app/verify-otp`, … (unversioned) are the shipped mobile
client's contract and stay as they are; `/app/v1/*` is the same API for new
clients.

- **Staff sign-in** (both `/backoffice/v1` and `/pos/v1`): `POST /auth/send-code {phone}` →
  `POST /auth/verify-otp {phone, otp_code}` → `{user, access_token, refresh_token, expires_at}`;
  `POST /auth/refresh`, `POST /auth/logout {refresh_token}`, `GET /auth/me`.
  A customer's number gets `200` on `send-code` (no SMS is sent, so staff numbers cannot be probed)
  and `403` on `verify-otp`.
- **`/app/v1`**: `GET /outlets`, `GET /services`, plus the auth routes above.
- **`/pos/v1`**: `GET /catalog` (outlets + services for offline caching), `POST /orders`
  (idempotent on `id`: `201` new, `200` already there), `GET /orders`, `GET /orders/:id`,
  `PATCH /orders/:id/status`.
- **`/backoffice/v1`**: `GET /users`, `PATCH /users/:id/status`; `GET|POST /outlets`, `PATCH /outlets/:id`;
  `GET|POST /services`, `PATCH /services/:id`; `GET /orders`, `GET /orders/:id`, `PATCH /orders/:id/status`.

Order lifecycle: `created → processing → ready → fulfilled`, or `cancelled` before `ready`.
The server recomputes totals from the lines; prices are the ones the POS sold at.

## Staff accounts

Staff sign in with phone + code like everyone else; roles come from the database.

Roles ride in the access token (`roles` claim): `admin`, `outlet:<id>`. wuzz-support
authorises chat against the same claim.

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

`make run` and `make migrate` read `.env` first (gitignored; copy
`.env.example`), so a value there wins over the defaults in `internal/pkg/config`.
What you export in your shell still wins over the file. To run a service on this
machine against another environment's addresses:

```
ENV_FILE=.env.staging make run SERVICE=gateway
```

`.env` is for local, `.env.staging` and `.env.production` are for those
environments (the VM reads them through Docker Compose), and neither of those is
ever committed.
Or the whole stack in containers: `make up` (then `make down` / `make reset`).

## Regenerating gRPC code after editing a `.proto`

```
make tools   # once, installs buf + protoc-gen-go + protoc-gen-go-grpc
make proto
```

Generated code under `gen/` is committed, so a fresh clone builds with
`go build ./...` alone.
