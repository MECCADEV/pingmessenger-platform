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

The current contract version is `0.8.0`. Story uploads accept image/video MIME
types up to 10 MiB and are capped at 10 active stories per user; deleted or
expired media is removed from S3 with retryable cleanup. Cluster E2E coverage is in
`tests/e2e/` and includes optional-username signup, concurrent claims, MFA,
SES delivery with a test mirror, password recovery, and OpenIM interoperability.

## Account authentication

`username` is optional at signup. When supplied it is case-insensitively unique
among active accounts. Clients may preflight it with `POST /v1/auth/verify-username`:

```json
{"username":"alice"}
```

The database unique index remains the authority, so clients must still handle
`409 Conflict` on a concurrent signup. Every signup returns a 12-digit numeric
`ping_id` plus access, refresh, and device-session tokens. `platform_id` defaults
to `web`; clients should send their device name/ID when available. `email` and
`phone` are optional contact methods; neither is required to create an account
or to sign in. If email is used as the login identifier, it must first be
OTP-verified; an unverified email receives the same unauthorized result as an
invalid password. The primary sign-in route is `POST /v1/auth/login`:

```json
{"email":"alice@example.com","password":"correct-horse-battery-staple","platform_id":"android","device_name":"Pixel"}
```

Supply exactly one of `username`, `ping_id`, or an attached `email`; it returns
the normal access token, refresh token, and device session. The
legacy `/v1/auth/login/start` and `/v1/auth/login/verify` routes remain for
optional email-OTP login. A signup may include `nickname`; it is a non-unique
display name stored in PostgreSQL. Read it (along with `username` and `ping_id`)
with `GET /v1/profile/`. Change the optional username with authenticated
`PATCH /v1/profile/username`; change the nickname (with or without an image)
through multipart `POST /v1/profile/update` using the `nickname` form field.

Password recovery starts at `POST /v1/auth/password/recovery/start` with exactly
one `username`, `ping_id`, `email`, or `phone`. A valid account receives the same
one-time code at every verified email/phone contact; complete it with
`POST /v1/auth/password/recovery/complete`. Responses deliberately do not reveal
whether an identifier exists. Authenticated users use
`POST /v1/auth/password/change`; accounts with verified contacts complete the
returned step-up challenge at `/v1/auth/password/change/verify`, while accounts
without a verified contact can change the password with the current password.

MFA and recovery email use AWS SES directly in production (`EMAIL_PROVIDER=ses`,
sender `AWS_SES_FROM_EMAIL`) and SMTP/Mailpit locally. SNS is not the recipient
inbox path. `EMAIL_MIRROR_SMTP_ADDRESS` is an explicit test-only mirror and must
remain unset in production.

Username endpoints:

- `POST /v1/auth/verify-username` — HTTP preflight, `{ "username": "alice" }`.
- `PATCH /v1/profile/username` — authenticated username update.
- `GET /v1/auth/username/availability` over WebSocket — connect to
  `wss://api-platform-pingmessenger.meainternal.com/v1/auth/username/availability`
  and send `{ "type":"check_username", "request_id":"r1", "username":"alice" }`.
  The response is `{ "type":"username_availability", "request_id":"r1", "username":"alice", "available":true }`.
  The socket accepts at most 60 checks and never returns account metadata.

## User directory search

Authenticated clients can call `POST /v1/friends/search` with exactly one
search field:

```json
{"query":"ali","field":"username","limit":20}
```

`username` is a case-insensitive partial match. `email`, `phone`, and
`ping_id` are exact matches; email and phone results require a verified
contact. The response is `{ "results": [...], "total": n }` with OpenIM-shaped
`userID`, `nickname`, `faceURL`, `remark`, and `relationship` fields, plus
`username` and `ping_id`. `relationship` is `-1` because this is a global
platform directory lookup and does not evaluate the caller's local OpenIM
friend/blacklist graph. Contact values are never returned, and the requester
is excluded from results.

Phone verification uses `POST /v1/mfa/challenge` with
`{ "phone":"+...", "purpose":"signup_contact_verification" }`, then
`POST /v1/mfa/verify`. Production sends through AWS SNS transactional SMS;
`SMS_MIRROR_TOPIC_ARN` is an opt-in test-only SNS/SQS mirror for inspecting
codes and must remain empty in production.

MFA challenge issuance is limited to one request per minute and five requests
per hour for both the normalized contact and the connection IP. A throttled
request returns `429 Too Many Requests` with a `Retry-After` header. Requests
rejected by the limiter do not replace an active challenge; after a challenge
is successfully consumed, a new flow may start immediately subject to the
hourly limit.

If a user omitted a contact during signup, an authenticated session can add
one with `POST /v1/mfa/contacts` using
`{"kind":"email|phone","value":"..."}`. The response includes a challenge
ID; complete it with `POST /v1/mfa/verify`. A contact remains unverified until
that challenge is consumed, and an existing verified contact cannot be
replaced.

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

## Stories

Stories are owned by Platform/PostgreSQL and are delivered only to users who
have a one-to-one OpenIM conversation with the author. `POST /v1/stories` is a
multipart upload (`media`, optional `caption`, `visibility=contacts|allow|exclude`,
optional JSON-array `user_ids`, and optional `expires_in_seconds`); expiry
defaults to 24 hours and media is limited to 10 MiB. A story deletion or
expiry is a durable `deleted` event, so offline clients do not retain stale
stories. `POST /v1/stories/{storyID}/view` records an idempotent view/reaction;
the owner can read those records from `GET /v1/stories/{storyID}/views`.

Clients consume `/v1/stories/ws` with `sync` and `ack` frames. The server
returns monotonic `event_id` cursors globally and per story owner, and rejects
acknowledgements that move either cursor backwards.

The initial conversation graph snapshot is fetched over OpenIM's native
conversation gRPC service (`OPENIM_CONVERSATION_GRPC_TARGET`). To enable
push-based conversation-graph refresh, configure OpenIM's
`webhooks.yml` with `transport: grpc`, `grpcTarget: <platform-service>:9090`,
and enable the friend/conversation after-callbacks. HTTP callbacks remain the
available by setting `transport: http` when a deployment does not run the
Platform gRPC receiver.

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

## Payments

Payment creation records an application intent. The platform then optionally
indexes the referenced chain transaction through `SOLANA_RPC_URL` and stores a
normalized `chain_transactions` row plus one or more `chain_transfers` rows.
This supports transactions containing multiple native and token movements;
client-supplied `asset` and `amount_atomic` are never treated as chain facts.
Indexing is idempotent on `(chain_id, transaction_id)` and updates the matching
payment's `chain_transaction_id` without changing its status. Fetch the
observed transaction and transfers with:

`GET /v1/payments/{paymentID}/chain`

The endpoint is participant-authorized and returns 404 until the RPC fetch has
completed. Solana `jsonParsed` instructions, inner instructions, token mints,
decimals, native lamports, fee, slot, and terminal RPC status are indexed.
Additional chains should implement `internal/chainindex.Indexer` and reuse the
same repository tables.

The platform owns the payment ledger and exposes it as an authenticated
conversation API. Create a pending record with
`POST /v1/conversations/{conversationID}/payments`, then retrieve it newest
first with `GET /v1/conversations/{conversationID}/payments?to=latest&limit=50`.
Results are reverse chronological. `to=latest` (or a payment ID) is the
inclusive upper anchor; `last_known_payment_id` is an exclusive payment
cursor, and `last_block_id` is an exclusive chain-block cursor. Continue a
page with the returned `next_to` and `next_block_id`. The WebSocket endpoint is
`wss://api-platform-pingmessenger.meainternal.com/v1/conversations/{conversationID}/payments/ws`;
it emits participant-filtered `payment.updated` frames. Both the sender and
recipient receive the pending record and each later chain success/failure
transition; the one-second timeout sweep sends a terminal `status=failed`
frame with `update_reason=timeout` to both connected participants. Clients
must recover missed events from the HTTP list endpoint. The equivalent
recovery endpoint is `GET /v1/conversations/{conversationID}/payments/sync`;
an individual record is available at `GET /v1/payments/{paymentID}`.

Payment records are idempotent by `payment_id`, begin as `pending`, and carry a
monotonically increasing version. PingPay-user transaction updates will enter
through a private watcher adapter; it does not own conversation history or
client delivery. OpenIM carries the resulting `pingmessenger.payment.v1`
JSON envelope in its existing CustomElem message type. The private callback is
`POST /v1/internal/payments/events` with an `X-PingPay-Signature` HMAC-SHA256
header over the exact request body; configure its key as `PINGPAY_EVENT_SECRET`.

On creation and each accepted status transition, the platform projects one
`pingmessenger.payment.v1` CustomElem message into OpenIM. The envelope includes
the payment ID, version, status, amount/asset, transaction and block metadata,
and `occurred_at_ms`; timeout projections additionally carry `reason=timeout`.
Projection delivery is idempotent by `(payment_id, kind, version)`; if OpenIM is
temporarily unavailable, the ledger remains durable and the client recovers the
state from the sync endpoint. The operator-controlled
`stale_payment_failed_after` timer defaults to 180 seconds, while an earlier
payment `expires_at` can fail a record sooner.

## Timers, disappearing messages, and notifications

Authenticated clients can read operator-controlled values from
`GET /v1/settings/timers` (or `GET /v1/settings/timers/{timerKey}`). Current
keys are `message_edit_window`, `stale_payment_failed_after`, and
`disappearing_message_default`.

Per-conversation disappearing messages are managed with:

- `GET /v1/conversations/{conversationID}/disappearing`
- `PUT /v1/conversations/{conversationID}/disappearing` with
  `{"duration_seconds":86400,"enabled":true}`
- `DELETE /v1/conversations/{conversationID}/disappearing`
- `wss://api-platform-pingmessenger.meainternal.com/v1/conversations/{conversationID}/disappearing/ws`

The WebSocket emits `disappearing.updated` events so connected clients can
update their local expiry timer immediately. Settings are stored per user and
conversation; deleting a setting disables it and subsequent reads fall back to
the global default.

## Production deployment automation

Production build and rollout automation is documented in
[docs/production-deployment.md](docs/production-deployment.md). The GitHub
Actions job fetches deployment-only values from Vault, pushes an immutable
image, updates the existing EKS rollout, and verifies health and OpenAPI before
completing.
