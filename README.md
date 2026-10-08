# PingMessenger API

FastHTTP + PostgreSQL bootstrap for the architecture in `arch.md`.

```bash
cp .env.example .env
# create the database/user, then apply the tracked migration(s)
make migrate
go run ./cmd/api
curl http://localhost:8080/healthz
```

Environment variables override `.env`; see `.env.example`. Never commit `.env`
or deploy its example JWT secret. The initial database migration is run by the
deployment migration step, not by each API replica at startup.

Endpoint contracts and the phased implementation plan live in
`.agents/plans/pingmessenger-auth-bootstrap/00-plan.md`.

The checked-in OpenAPI 3.1 contract is [openapi/openapi.json](openapi/openapi.json).
Regenerate it with `make openapi`. It is generated from Huma typed schemas only;
the production FastHTTP router and handlers are not wrapped, replaced, or given
additional documentation routes.

The deployed document is served read-only at
`https://api-platform-pingmessenger.meainternal.com/openapi.json`.

## Account authentication

`username` is required at signup and is case-insensitively unique among active
accounts. Clients may preflight it with `POST /v1/auth/verify-username`:

```json
{"username":"alice"}
```

The database unique index remains the authority, so clients must still handle
`409 Conflict` on a concurrent signup. `email` and `phone` are optional contact
methods; neither is required to create an account or to sign in. The primary
sign-in route is `POST /v1/auth/login`:

```json
{"username":"alice","password":"correct-horse-battery-staple","platform_id":"android","device_name":"Pixel"}
```

It returns the normal access token, refresh token, and device session. The
legacy `/v1/auth/login/start` and `/v1/auth/login/verify` routes remain for
optional email-OTP login. A signup may include `nickname`; it is a non-unique
display name stored in PostgreSQL. Read it with `GET /v1/profile/` and update
it (with or without an image) through authenticated multipart
`POST /v1/profile/update` using the `nickname` form field.

Schema history is maintained by [sqlmig](https://github.com/Shaik-Sirajuddin/sqlmig),
not by calling `psql` manually: `make migrate` runs `cmd/db`, which delegates to
that module. Development/test fixtures run through `make seed` and are refused
outside `APP_ENV=development` or `test`. SQL files remain ordered in
`migrations/` and `seeds/`; the runner records checksums and uses a PostgreSQL
advisory lock.

`tools/omni` is not a dependency of this module. Omni is AGPL-3.0 and its
migrator targets SQLite, so it is not imported here.

## Shark deployment endpoints

The production API is live at
`https://api-platform-pingmessenger.meainternal.com`. Its health endpoint is
`GET /healthz`. The API is deployed as two replicas with a gp3-backed
PostgreSQL instance; it calls OpenIM only over the cluster network at
`http://openimserver-openim-api.openim.svc.cluster.local`.

OpenIM client APIs are publicly available only under
`https://api-platform-pingmessenger.meainternal.com/openim/`. The gateway
strips the `/openim` prefix before forwarding to OpenIM. Client-safe API
groups (`user`, `friend`, `group`, `conversation`, `msg`, `third`, and
`auth/parse_token`) are routed; OpenIM admin/token-minting endpoints remain
cluster-private. Use an OpenIM user token in its required `token` header and
send an `operationID` header on OpenIM requests.

Its MongoDB (three members), Kafka (two brokers, replication factor two), and
shared Redis dependency are cluster-private. Avatar/object writes use the private S3 bucket
`pingmessenger-openim-403048695675-ap-south-1` through IRSA; do not add AWS
credentials to Helm values or application environment files.

## Mobile app telemetry

Shark has a minimal single-replica Loki deployment. Its public Loki JSON push
endpoint is `https://loki.meainternal.com/loki/api/v1/push`; its in-cluster
endpoint is `http://loki.loki.svc.cluster.local:3100/loki/api/v1/push`.

Loki's push API is **not OTLP** and must not be called directly by a Flutter
app: this minimal endpoint has no per-app authentication or rate limiting.
Do not send Flutter logs to Tempo either; the installed Tempo gateway is an
internal trace backend (`tempo-gateway.tempo.svc.cluster.local:80`), not an
OTLP log receiver.

The production endpoint contract for apps, once the collector rollout is
installed, is:

| Purpose | Endpoint | Protocol |
| --- | --- | --- |
| Flutter logs | `https://otel.meainternal.com/v1/logs` | OTLP/HTTP + protobuf |
| Flutter traces | `https://otel.meainternal.com/v1/traces` | OTLP/HTTP + protobuf |
| Collector → Loki | internal only | Loki push API |

The public collector must authenticate every app request (for example, a
short-lived app telemetry token); never embed Loki credentials in a Flutter
binary. The app should batch, retry with backoff, and omit message bodies,
auth tokens, emails, phone numbers, and other personal data from attributes.
`otel.meainternal.com` remains a planned hostname: no OTLP Collector is
currently deployed or publicly routed. A future Collector must authenticate
and rate-limit requests before forwarding logs to the private Loki service.
