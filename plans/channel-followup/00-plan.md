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
- A renewable least-privilege Vault token was created with access limited to `secret/data/pingmessenger/platform/deploy`, verified for self-lookup, config read, and renewal, then installed in the `production` and `production-maintenance` GitHub environments. Renewal workflow [#44053718859](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44053718859) passed with a 31-day lease.
- Platform deployment run [#44053772607](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44053772607) completed successfully after retrying a transient registry manifest 503 and deployed main commit `40412308a6ac28b8e6efac89cd88d8d564c24437` (OpenAPI 0.16.0). The live channel E2E then exposed a real metadata update bug: the Platform sent `groupInfo` instead of OpenIM's required `groupInfoForSet` wrapper. Platform PR [#15](https://github.com/MECCADEV/pingmessenger-platform/pull/15) fixed it and merged as `d6fdcc170e74041f8b9db7219a89f12e6f94b3fc`; deployment [#44055569538](https://github.com/MECCADEV/pingmessenger-platform/actions/runs/44055569538) then completed build, rollout, health and OpenAPI smoke checks. Both deploy runs had transient infrastructure 503s that passed on retry.
- The production channel E2E passed on 2026-11-17 against `https://api-platform-pingmessenger.meainternal.com` after the fix. It covered public/private creation and search visibility, OpenIM group lookup, owner metadata update, private join request and owner approval, Likes, pin/unpin, photo upload, member/admin/owner posting rules, and rejection of `cancel_mute_group` for `ch_` groups. The harness creates disposable production users and channels. The Go test retries bounded Envoy pre-upstream 503s/timeouts; persistent failures still fail the run.
- The app's 1–24 hour retention is local to each installation; it does not delete OpenIM messages for every channel member. Production Flutter SDK approval and device-local retention are not covered by the platform Go harness; live OpenIM owner approval is covered, and the SDK path remains the client implementation.

## Verification matrix

| Request | Evidence/implementation target | Verification |
| --- | --- | --- |
| Auto-delete 1–24h | Existing chat retention expires cached channel posts on that installation only; it is not a server-wide deletion timer. | Verify local Flutter behavior; server-wide expiry remains unimplemented if required |
| Private invite link | Client deep link carries the OpenIM group ID; SDK requests to join and OpenIM admins approve. No searchable directory entry. | Client link parsing plus live private lookup/privacy and OpenIM owner approval E2E passed |
| Like reactions | Persist idempotent user/message reaction or use supported OpenIM reaction API | set/unset/list authorization and E2E |
| Edit/delete/photo | Admin-only channel patch/delete and authenticated photo upload flow | owner/admin/reader cases and E2E |
| My channels/group lookup | Membership-backed list and exact group ID resolver | member visibility and nonmember/private leakage tests |
| Admin/join approval via SDK | Join and request approval through OpenIM SDK; role changes remain SDK-owned | Flutter approval flow audited; live OpenIM owner approval passed |
| OpenIM guard deployment | Confirm deployed OpenIM image/source and run direct guard E2E | Live WebSocket posting and mute-off guard checks passed for `ch_` groups |

The deployed API E2E ran after successful production rollout and health/OpenAPI checks. It creates disposable test accounts and channel records and exercises the server-supported rows above. Server-wide message expiry remains unimplemented; channel retention currently affects only each Flutter installation's local cache.

## Decisions and constraints

- Keep OpenIM groups as the authority for membership, admin roles, join requests, message delivery, and group message reactions where native APIs support them.
- Keep PingMessenger channel metadata and invitation records in platform Postgres.
- A deep link is an entry path, not an authorization grant; private metadata remains hidden until membership is granted by OpenIM.
- OpenIM's `ch_` message posting guard is a server invariant and must remain deployed independently of platform feature rollout.
