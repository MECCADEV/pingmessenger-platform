# PingChat backend request subset

This plan implements only request cards 1, 3.2, 5.1, 5.4, 6, and 7 from the pasted PingChat request. Cards 2, 3.1, 4, 5.2, and 5.3 are intentionally out of scope.

## Decisions

- Password change is one authenticated request. The current session remains active and every other session is revoked. The current-password field is length-bounded but not subject to the new-password strength validator, so every mismatch is a 401.
- Text statuses are stored without an object. They require non-empty text and a validated `#RRGGBB` background color; image/video status behavior remains unchanged.
- `GET /v1/friends` resolves the authenticated user's OpenIM friend IDs and joins them to active platform profiles. The existing owner-only `GET /v1/stories/{storyID}/views` remains the “viewed by” endpoint; its documentation will say so explicitly.
- Blocks are authoritative in OpenIM and the platform route translates PingID to OpenIM user IDs. The route never exposes contact values.
- Account deletion is password-confirmed, requires `accept_terms=true`, revokes sessions/tokens, removes application data and storage objects, and retains only a tombstone for PingID, username, and linked contact identifiers so they cannot be reused. The OpenIM integration is invoked before the platform tombstone is committed.

## Verification matrix

- Unit: password validation and session-preserving rotation; text-story validation/model round-trip; profile delete; friend/block response mapping.
- Database: migration applies cleanly; account-delete transaction preserves identity reservations and removes user-owned application rows.
- HTTP: authenticated/unauthenticated, invalid password, invalid text background, image-delete, friends, blocks, and account-delete responses.
- OpenAPI: generated document contains the new routes and text-story multipart fields; checked-in schema is regenerated.
- Race: `go test -race ./...` plus concurrent account-delete/password-change and story-create tests where repository seams permit.

## Known integration constraint

The current OpenIM fork does not expose a native account-delete API in its HTTP router. The platform first calls `user/delete_users`; when an older fork returns a route-level 404, it calls OpenIM `auth/force_logout` for every platform ID and completes the platform tombstone. Other OpenIM failures remain fail-closed. A matching fork endpoint is still required for physical OpenIM identity removal.
