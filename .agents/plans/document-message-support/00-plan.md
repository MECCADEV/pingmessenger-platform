# Document message support

## Scope

Make client document sharing reliable across the PingMessenger platform, OpenIM deployment, and Helm configuration:

- authenticated platform upload endpoint with MIME/size/count/rolling-24-hour policy;
- durable document metadata and cleanup state in PostgreSQL;
- S3 objects isolated under a document prefix/bucket and deleted on user deletion or retention expiry;
- OpenIM object API exposed through the public gateway and configured for AWS S3/IRSA;
- OpenAPI, tests, and deployment defaults that make the contract explicit.

The OpenIM WebSocket remains the message transport. The platform upload response supplies the signed/public object URL and metadata that the client puts into a `contentType=105` file message.

## Decisions

- Default policy: 100 MiB per document, 20 retained documents per user, 1 GiB uploaded bytes per rolling 24 hours, seven-day retention.
- Server-side policy is authoritative and configurable through environment variables; limits are validated before object persistence.
- Document records are retained after S3 deletion as tombstones so message history can remain while the object is gone.
- Cleanup is a retryable platform worker with an indexed due-time query. User-requested delete marks the row immediately and schedules the same S3 cleanup path.
- The OpenIM object route is `/openim/object/*` and rewrites to the OpenIM API's `/object/*` path.

## Verification matrix

1. Unit tests for policy parsing, MIME/size checks, rolling quota, cleanup transitions, and S3 deletion calls.
2. HTTP tests for authenticated upload/delete responses and OpenAPI entries.
3. Helm template/lint checks for object route, AWS object settings, IRSA annotation, and document bucket values.
4. Go test suites for platform and OpenIM server.

## Known boundary

OpenIM does not persist platform document ownership or retention metadata. The platform owns policy and cleanup; OpenIM only receives the resulting URL in a normal WebSocket file message.
