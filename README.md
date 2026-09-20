# wuzz-backend

Go backend for wuzz, replacing the current production backend `apps/wuzz`
and `apps/backoffice` (in the `wuzz` monorepo) talk to. Architecture mirrors
`reddog-backend`: a public `gateway` (REST, gin) in front of internal gRPC
services, hexagonal/CQRS-shaped (`domain` / `app/command` / `app/query` /
`adapters` / `ports`), Postgres via pgx, Redis for sessions and one-time
codes, Ed25519 JWTs signed only by the `user` service.

Built service by service, starting with the one everything else depends on:

- ✅ `user` — phone + SMS one-time-code auth (no password/PIN — see
  `internal/services/user/domain/user.go`), matching the contract
  `apps/wuzz/src/core/api/auth/*` already calls: `/app/register`,
  `/app/verify-otp`, `/app/suggest-username`, `/app/check-username`,
  `/app/me`, `/refresh`, `/logout`.
- Everything else (outlets, laundry services/catalog, orders, promotions,
  gamification, notifications, media, payment) is not started yet.

## Local development

```
cp .env.example .env   # only if you want to override a default — see config.go
make dev               # postgres + redis, then runs the user migrations
make run SERVICE=user       # in one terminal
make run SERVICE=gateway    # in another
```

Or the whole stack in containers: `make up` (then `make down` / `make reset`).

No Twilio credentials needed locally — `TWILIO_DEV_MODE=true` (the default
outside a real staging/production environment) logs the verification code
instead of sending an SMS.

## Regenerating gRPC code after editing a `.proto`

```
make tools   # once, installs buf + protoc-gen-go + protoc-gen-go-grpc
make proto
```

Generated code under `gen/` is committed, so a fresh clone builds with
`go build ./...` alone — `make proto` is only needed after a proto change.
