# Channel follow-up capabilities

Audit deployed and repository support for requested channel behavior, implement missing platform APIs and client contracts, run focused automated and deployed E2E, then merge and deploy.

## Requested capabilities

1. Auto-delete timer for channel posts from 1–24 hours; verify current app-wide chat retention behavior and add an admin-controlled channel setting if it does not cover this need.
2. Private-channel invite links are client deep links containing the channel's OpenIM group ID. Private channels remain absent from search. Opening a link starts the OpenIM group join request; it never bypasses admin approval.
3. Like reactions on channel posts.
4. Edit/delete channel metadata and upload a photo rather than requiring an externally hosted URL.
5. List the caller's channels and resolve a channel by its OpenIM group ID.
6. Confirm channel admins and private-channel join requests are managed through the OpenIM SDK.
7. Verify production OpenIM group RPC has the `ch_` mute-off guard.

## Audit findings and implementation

- The app's 1–24 hour retention setting already expires cached channel posts on that installation. It does not delete the OpenIM message for every member.
- Private channels remain excluded from public search. `/ch/<openim_group_id>` is a client deep link and calls `joinGroup`; OpenIM applies the configured approval policy. The route can show pending-request feedback without resolving private metadata.
- The Flutter app now identifies `ch_` conversations as channels, creates channels through the Platform API, publishes text posts through OpenIM, and toggles Like through member-authorized Platform endpoints.
- Platform channel management now supports admin-only metadata/visibility updates, photo uploads, channel deletion, and lookup by OpenIM group ID. The Flutter settings screen includes metadata, visibility, photo, deletion, and OpenIM SDK join-request approval controls.
- App channel lists are derived from the signed-in user's OpenIM conversations. Public discovery remains through Platform search; group-ID lookup hides private metadata from nonmembers.
- The OpenIM Flutter SDK exposes join, application-list, accept/refuse, and role-update operations. Join-request approval is now wired through that SDK. Admin role assignment should also remain an SDK operation.
- Production is answering `/healthz` (200) and `/openapi.json` (200, version 0.15.0) but has not deployed these pending API changes yet. The first live E2E attempt hit a transient 503 on unauthenticated channel create; a subsequent direct probe returned the expected 401. Rerun the full deployed E2E after rollout.

## Verification matrix

| Request | Evidence/implementation target | Verification |
| --- | --- | --- |
| Auto-delete 1–24h | Verify and, if needed, add admin-controlled channel retention. Current app retention appears local/device scoped. | API/integration tests and live E2E expiry behavior |
| Private invite link | Client deep link carries the OpenIM group ID; SDK requests to join and OpenIM admins approve. No searchable directory entry. | link parsing, private lookup/privacy, pending request and approval E2E |
| Like reactions | Persist idempotent user/message reaction or use supported OpenIM reaction API | set/unset/list authorization and E2E |
| Edit/delete/photo | Admin-only channel patch/delete and authenticated photo upload flow | owner/admin/reader cases and E2E |
| My channels/group lookup | Membership-backed list and exact group ID resolver | member visibility and nonmember/private leakage tests |
| Admin/join approval via SDK | Join and request approval through OpenIM SDK; role changes remain SDK-owned | Flutter SDK integration and live E2E |
| OpenIM guard deployment | Confirm deployed OpenIM image/source and run direct guard E2E | AWS deployment status plus OpenIM/API E2E |

## Decisions and constraints

- Keep OpenIM groups as the authority for membership, admin roles, join requests, message delivery, and group message reactions where native APIs support them.
- Keep PingMessenger channel metadata and invitation records in platform Postgres.
- A deep link is an entry path, not an authorization grant; private metadata remains hidden until membership is granted by OpenIM.
- OpenIM's `ch_` message posting guard is a server invariant and must remain deployed independently of platform feature rollout.
