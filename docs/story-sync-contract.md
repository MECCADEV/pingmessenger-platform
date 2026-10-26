# Story sync and background media contract

The Platform API exposes no HTTP story-feed/list operation. Clients consume
story events over authenticated `GET /v1/stories/ws`, as described in the
generated OpenAPI entry. The server subscribes each connection to that user's
eligible network events; a user may have at most eight concurrent story
WebSockets. Requests are strict JSON objects limited to 64 KiB, and the
per-request `user_cursors` map may contain at most 100 entries.

## Replay protocol

Send a `sync` (or equivalent `get`) request with the last locally committed
cursors:

```json
{"type":"sync","global_cursor":0,"user_cursors":{},"limit":100}
```

`global_cursor` and every per-owner cursor are non-negative event IDs. A page
size from 1 through 200 is honored; missing, non-positive, or larger limits
use 100. The server replies with:

```json
{
  "type":"stories.batch",
  "events":[{"event_id":12,"type":"created","story_id":"…","owner_id":"…","created_at":"…","story":{"id":"…","owner_id":"…","media_type":"image","media_url":"…","content_type":"image/jpeg","byte_size":123,"caption":"…","visibility":"contacts","expires_at":"…","created_at":"…"}}],
  "global_cursor":12,
  "user_cursors":{"owner-uuid":12},
  "has_more":false
}
```

The schema example is illustrative; nullable/optional story fields follow the
JSON tags on `internal/stories/model.go`. Events have type `created` or
`deleted`. Live updates arrive as `{"type":"stories.event","event":{…},"global_cursor":12}`.

The client must transactionally persist the batch and returned cursors, then
send the corresponding `ack`:

```json
{"type":"ack","global_cursor":12,"user_cursors":{"owner-uuid":12}}
```

Acknowledgement persists global and per-owner progress and rejects regression.
Only after the local transaction commits may a client acknowledge; after a
crash it resumes from its own durable cursor, not an in-memory cursor. Continue
requesting pages with the last committed cursors while `has_more` is true.
This makes duplicate delivery safe and prevents an acknowledgement from
skipping events lost before persistence.

The PostgreSQL-backed regression `internal/stories/repository_sync_integration_test.go`
verifies that a page requested again before ACK replays the same event, ACKed
pages advance bounded pagination, and both sync requests and acknowledgements
reject cursor regression. This exercises the durable repository contract; it
is complemented by `internal/httpapi/routes_stories_ws_integration_test.go`,
which exercises the authenticated `/v1/stories/ws` handler over a real local
WebSocket, including reconnect replay, ACK, next-page delivery, and the
`cursor_regression` wire error. These are local PostgreSQL/server integration
tests; deployed WebSocket behavior remains unverified.

## Media and durable downloads

Created story events include a private S3 `media_url` with a ten-minute
presigned lifetime. Do not persist that URL as a durable background job source.
Instead enqueue the documented authenticated route
`GET /v1/stories/{storyID}/media`; it streams the entire object as the response
body, requires an active bearer session, and checks current recipient
authorization on each request. Image and video content types are preserved.
The proxy has no URL expiry but provides no byte ranges or cache validators;
the current rate limits are 120 requests per authenticated user and 600 per
peer IP per minute when Redis is configured. A mobile worker must wait for a
valid signed-in session, and should treat 404 as terminal because the story may
have expired, been deleted, or become unauthorized.

## Source of truth

- Runtime handshake, `sync`/`get`, `ack`, cursor validation, batch replies, and
  live events: `internal/httpapi/routes_stories.go` and
  `internal/httpapi/story_live.go`.
- Durable event/cursor semantics: `internal/stories/repository.go` and
  `internal/stories/model.go`.
- Authenticated full-object media proxy: `internal/httpapi/routes_stories.go`.
- Generated API description: `/v1/stories/ws` and
  `/v1/stories/{storyID}/media` in `openapi/openapi.json`.
