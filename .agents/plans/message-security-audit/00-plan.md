# Message/media security audit

## Scope

Audit the latest custom-message release and the immediately preceding media,
document, story, and OpenIM lifecycle changes for:

- authenticated ownership checks on reads, links, deletes, and message cleanup;
- user/IP rate-limit coverage and quota side effects;
- concurrent cleanup workers and story/document owner limits;
- PostgreSQL lock/claim correctness and retry behavior.

## Findings to resolve

1. Document links silently succeed when the document is missing, expired, or not
   owned by the caller because the `INSERT ... SELECT` result is not checked.
2. Document and story pending-media cleanup use an unclaimed `SELECT`, so two
   API replicas can process the same object concurrently.
3. Story URL cache entries are never bounded or expired from memory, allowing a
   long-lived API process to accumulate one entry per story object.
4. Signed-URL issuance is rate-limited after one document lookup; the route
   should charge the limit before the database lookup and avoid unnecessary
   enumeration work.
5. Story WebSocket origin matching used substring matching and had no
   connection/cursor caps; authenticated browsers could attempt CSWSH or
   consume excessive per-connection database work.

## Decisions

- Use PostgreSQL `FOR UPDATE SKIP LOCKED` CTE claims for cleanup work. Claims
  expire after five minutes so a crashed worker is recoverable.
- Keep existing owner/recipient authorization semantics; do not make document
  metadata public. Story media remains recipient-authorized.
- Preserve the fail-closed Redis limiter behavior. Limit ordering is IP first,
  then user, so a rejected abusive peer does not consume a user bucket.
- Bound the in-process story signed-URL cache and evict expired entries under
  the existing mutex; signed URL reuse remains object-key scoped.
- Require an exact story WebSocket origin host, cap active connections per
  user, cap cursor fan-out, and rate-limit connect/ack/delete/view operations.

## Verification matrix

1. Unit tests for link ownership/not-found behavior, limiter ordering, cache
   eviction, and cleanup claim SQL behavior where repository tests exist.
2. `go test ./...`, `go test -race ./...`, and `go vet ./...`.
3. OpenAPI generation comparison and migration SQL review.
4. Existing media/document E2E tests remain green.
