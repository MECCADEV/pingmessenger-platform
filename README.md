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

Schema history is maintained by [sqlmig](https://github.com/Shaik-Sirajuddin/sqlmig),
not by calling `psql` manually: `make migrate` runs `cmd/db`, which delegates to
that module. Development/test fixtures run through `make seed` and are refused
outside `APP_ENV=development` or `test`. SQL files remain ordered in
`migrations/` and `seeds/`; the runner records checksums and uses a PostgreSQL
advisory lock.

`tools/omni` is not a dependency of this module. Omni is AGPL-3.0 and its
migrator targets SQLite, so it is not imported here.

For local MicroK8s, the Helm values reuse the existing `openim` namespace:
OpenIM API (`openimserver-openim-api`), MongoDB, Redis, Kafka, and MinIO are
already deployed there. The platform calls the namespace-qualified API service
rather than provisioning duplicate stateful dependencies.
