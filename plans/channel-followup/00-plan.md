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
- The platform and Flutter changes are merged (`pingmessenger-platform` PR #14 at `33d0fed6b071d6de4270d8a39664b8aedf7140df`; `pingchat-flutter` PR #30 at `d7604532c9b971cf565203d59eda4c8f518d1165`). Platform Go CI passed in run [#44028809509](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44028809509). The OpenIM `ch_` guard was previously deployed in run [#43185829196](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/43185829196).
- Platform production rollout has not happened. Manual deploy runs [#44028852259](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44028852259), [#44029780567](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44029780567), and [#44031269683](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44031269683) passed build/test/OpenAPI preflight, then failed at Vault access (`lookup-self` returned 503; deploy config returned 403), before image build or rollout. Vault's unauthenticated health endpoint is up, but the GitHub `VAULT_TOKEN` secret or its Vault policy is unusable for deployment. The token renewal workflow [#43699013423](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/43699013423) also failed: Vault renewed the lease only to 6,661 seconds, below the workflow's required seven days. Production probes on 2026-11-17 were intermittent (`/healthz` alternated between 200 and 503); `/openapi.json` returned 0.15.0 while merged local code is 0.16.0. Production lacks the new `/v1/channels/by-openim-group/{groupID}`, `/v1/channels/{channelID}/photo`, and `/v1/channels/{channelID}/posts/{messageID}/like` routes. An unauthenticated channel-create request returned the expected 401. Rotate the GitHub production environment Vault token to a renewable token with the required deployment access, roll out 0.16.0, and confirm API health before running the full deployed E2E.
- `tests/e2e/channels_test.go` exercises API creation, search/privacy, lookup, admin metadata changes, Likes, pins, photo upload, and the OpenIM read-only posting guard. It creates disposable production accounts and channel records. The app's 1–24 hour retention is local to each installation; it does not delete messages for all channel members. Production Flutter SDK approval and client retention behavior are not covered by this Go E2E harness and require client-side runtime verification.

## Verification matrix

| Request | Evidence/implementation target | Verification |
| --- | --- | --- |
| Auto-delete 1–24h | Existing chat retention expires cached channel posts on that installation only; it is not a server-wide deletion timer. | Verify local Flutter behavior; server-wide expiry remains unimplemented if required |
| Private invite link | Client deep link carries the OpenIM group ID; SDK requests to join and OpenIM admins approve. No searchable directory entry. | link parsing, private lookup/privacy, pending request and approval E2E |
| Like reactions | Persist idempotent user/message reaction or use supported OpenIM reaction API | set/unset/list authorization and E2E |
| Edit/delete/photo | Admin-only channel patch/delete and authenticated photo upload flow | owner/admin/reader cases and E2E |
| My channels/group lookup | Membership-backed list and exact group ID resolver | member visibility and nonmember/private leakage tests |
| Admin/join approval via SDK | Join and request approval through OpenIM SDK; role changes remain SDK-owned | Flutter SDK integration and live E2E |
| OpenIM guard deployment | Confirm deployed OpenIM image/source and run direct guard E2E | AWS deployment status plus OpenIM/API E2E |

The deployed API E2E is intentionally gated on both successful platform rollout and production API health. It creates test accounts and channel records and exercises the rows above where the production harness supports them. It does not verify Flutter SDK join-request approval or device-local retention; these need client runtime coverage.

## Decisions and constraints

- Keep OpenIM groups as the authority for membership, admin roles, join requests, message delivery, and group message reactions where native APIs support them.
- Keep PingMessenger channel metadata and invitation records in platform Postgres.
- A deep link is an entry path, not an authorization grant; private metadata remains hidden until membership is granted by OpenIM.
- OpenIM's `ch_` message posting guard is a server invariant and must remain deployed independently of platform feature rollout.
