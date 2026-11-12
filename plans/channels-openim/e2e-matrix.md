# Channel end-to-end matrix

Run with `E2E_BASE_URL` pointed at the deployed PingMessenger platform. The deployment must use the matching OpenIM server change. Test-created users/channels should use unique names and IDs so the suite can run repeatedly.

| Scenario | Expected result | Status |
|---|---|---|
| Unauthenticated create/search/detail/pins | 401 | Passed live |
| Create public channel | 201, `visibility=public`, `openim_group_id` starts `ch_` | Passed live |
| Search for public channel | Present with photo/description/group ID and member count | Passed live |
| Public channel join policy | Direct join succeeds without admin approval | Passed live |
| Create private channel | 201, `visibility=private`, `openim_group_id` starts `ch_` | Passed live |
| Search for private channel | Not present in public search | Passed live |
| Private channel detail as non-member | 404 or 403; no private metadata leak | Passed live, including after a join request |
| Private channel invitation/review | Non-member join request does not grant membership | Passed live |
| Ordinary member post to channel | OpenIM rejects send while channel mute is on | Passed live over the app WebSocket protocol |
| Owner post to channel | OpenIM accepts send while channel mute is on | Passed live over the app WebSocket protocol |
| Group admin post to channel | OpenIM accepts send while channel mute is on | Passed live after promoting the joined reader |
| Owner tries to unmute channel | OpenIM rejects `cancel_mute_group` | Passed live for public and private channels |
| OpenIM app admin tries to unmute channel | OpenIM rejects it too | Guard unit test; not directly exercised live |
| Ordinary group mute/unmute | Existing behavior still works | Guard compatibility unit test |
| Pin a channel message as owner/admin | 201; appears in pinned list | Passed live as owner |
| Pin as non-admin | 403 | Not directly exercised live |
| Unpin a message | 204; no longer listed | Passed live |
| Duplicate pin | Idempotent success or 409 as documented; no duplicate row | Repository integration test |
| OpenIM unavailable during channel create | Upstream error; no channel row left behind | Handler test |

Live run on 2026-11-12 used platform main `b25033329e57a664b9445bcb9bfc004072679b1c` and OpenIM image `registry.meainternal.com/pingmessenger/openim-rpc-group:channels-3a5287b80a54` (source `3a5287b80a54759949d442c373cd67054cd5203f`). The passing run exercised client WebSocket sends by an ordinary member, a promoted group admin, and the owner. Command: `E2E_BASE_URL=https://api-platform-pingmessenger.meainternal.com go test -tags=e2e ./tests/e2e -run '^TestChannelsAgainstDeployedAPI$' -count=1 -v`.

Evidence to record for each deployed run: platform build/version, OpenIM build/version, timestamp, scenario status, and sanitized response/error details. Never record access tokens.
