# Story retry semantics

This documents the behavior implemented by the Platform story handlers and
PostgreSQL repository.

## Create (`POST /v1/stories`)

`Idempotency-Key` is an optional request header, scoped to the authenticated
owner. Keys are 1–128 visible non-whitespace ASCII characters and are retained for 30 days.
Without the header, behavior remains legacy: every successful request creates a
new story, so clients must not automatically retry an ambiguous result.

With a key, the server fingerprints the normalized request, including media
bytes, type, caption/text, background, visibility, selected user IDs, and
effective expiry. The same key and fingerprint returns the original story ID
and a newly signed media URL when media is still available. The database stores
the key, fingerprint, story row, recipients, and events transactionally; the
owner row lock serializes concurrent first attempts across replicas. A reused
key with different content returns `409 Conflict`. A matching key whose story
has been deleted or expired returns `410 Gone`; the server never recreates it.
After 30 days, keys may be reused. The caller must persist the key before the
first request and reuse it only for retries of the same unchanged draft.

For `visibility=allow` or `visibility=exclude`, `user_ids` is a JSON array or
comma-separated list of Platform account UUIDs. It is not a list of OpenIM
user IDs. Authenticated `GET /v1/friends` returns both `user_id` (Platform UUID)
and `openim_user_id`; clients with OpenIM contact IDs must resolve selections
through that response and fail closed if a selected identity has no mapping.
The server only applies selected IDs that are in the owner's current friend
set.

The preflight lookup avoids another media upload for sequential retries. Two
simultaneous first attempts may both temporarily upload media before the
transactional winner is known; the losing object is deleted after the winner is
returned. A process crash between object upload and database commit can still
leave an unreferenced object. The losing-race object delete is best-effort and
logged on failure; generic object-store lifecycle/garbage collection is still
needed to guarantee orphan cleanup. This is not covered by story idempotency.

## View and reaction (`POST /v1/stories/{storyID}/view`)

The database primary key `(story_id, viewer_id)` and repository upsert ensure
there is at most one current view row for each viewer/story pair. A subsequent
request updates that row's `viewed_at` timestamp and replaces its `reaction`.
The request's omitted or empty reaction is stored as an empty string, so
replaying an earlier request can clear a later reaction and refresh the timestamp.
This is row-deduplicated, but not idempotent as a state update. `GET
/v1/stories/{storyID}/views` exposes the current rows to the story owner; there
is no per-request replay result or event history for view/reaction updates.

## Story sync and media

The story feed is the authenticated `GET /v1/stories/ws` WebSocket; there is no
HTTP list/feed route. The connection is default-subscribed to eligible network
events and permits at most eight concurrent connections per authenticated
user. Clients send strict JSON messages:

```json
{"type":"sync","global_cursor":0,"user_cursors":{},"limit":100}
```

`type` may be `sync` or `get`; both replay from the supplied cursors. The
effective page size is 100 when `limit` is omitted, non-positive, or above 200;
otherwise it is the supplied value. A successful page is a `stories.batch`
message with `events`, `global_cursor`, `user_cursors`, and `has_more`. Each
event has `event_id`, `type` (`created` or `deleted`), `story_id`, `owner_id`,
and `created_at`; created events also contain the story object. Live deliveries
use `stories.event` and carry the event plus `global_cursor`.

Persist all events from a page and the returned cursors atomically before
acknowledging them. Then send the same cursor fields with `type:"ack"`. The
server stores global and per-owner cursors transactionally and rejects cursor
regression. If `has_more` is true, request the next page with the cursors from
the just-persisted batch. On reconnect, start from locally committed cursors;
do not acknowledge a cursor for events that have not reached durable local
storage. WebSocket errors include invalid requests, cursor regression, sync or
ack failure, media signing failure, and rate limiting; the connection can also
be closed when the user exceeds the connection limit.

The `media_url` in create responses and created events is a private S3
presigned URL cached for ten minutes. It is a temporary bearer capability, not
a durable job URL. For offline/background download, use the authenticated
`GET /v1/stories/{storyID}/media` proxy with the current bearer token. That
route rechecks recipient authorization and streams the complete image/video
bytes; it has no signed-URL expiry but does not support byte ranges or cache
validators. A durable queue must defer for authentication when no current
session exists and must treat 404 as terminal (deleted, expired, or no longer
authorized). The WebSocket sync includes only currently eligible, non-expired
created stories and retained deletion events.

## Evidence and scope

- `internal/httpapi/routes_stories.go` parses and validates the optional header,
  fingerprints the normalized multipart request, and returns replay/conflict
  responses.
- `internal/stories/repository.go` stores create keys and story/event rows in
  one transaction and upserts views with `viewed_at=now()` and
  `reaction=EXCLUDED.reaction`.
- `migrations/0010_stories.sql` defines the `(story_id, viewer_id)` primary key.
- `migrations/0026_story_create_idempotency.sql` stores owner-scoped fingerprints
  and 30-day tombstones independent of story retention.
- `openapi/openapi.json` is generated from `internal/openapi/huma.go` with
  `make openapi`; the operation documents `Idempotency-Key`, 409, and 410.
- `docs/story-sync-contract.md` and the `/v1/stories/ws` OpenAPI description
  document the WebSocket request/replay/ack protocol and the authenticated
  media proxy used by background downloads.
