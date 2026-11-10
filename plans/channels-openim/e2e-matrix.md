# Channel end-to-end matrix

Run with `E2E_BASE_URL` pointed at the deployed PingMessenger platform. The deployment must use the matching OpenIM server change. Test-created users/channels should use unique names and IDs so the suite can run repeatedly.

| Scenario | Expected result | Status |
|---|---|---|
| Unauthenticated create/search/detail/pins | 401 | implemented; remote pending |
| Create public channel | 201, `visibility=public`, `openim_group_id` starts `ch_` | implemented; remote pending |
| Search for public channel | Present with photo/description/group ID and member count | implemented; remote pending |
| Public channel join policy | Direct join succeeds without admin approval | implemented; remote pending |
| Create private channel | 201, `visibility=private`, `openim_group_id` starts `ch_` | implemented; remote pending |
| Search for private channel | Not present in public search | implemented; remote pending |
| Private channel detail as non-member | 404 or 403; no private metadata leak | implemented; remote pending |
| Private channel invitation/review | Non-member join request does not grant membership | implemented; remote pending |
| Ordinary member post to channel | OpenIM rejects send while channel mute is on | implemented; remote pending |
| Owner/admin post to channel | OpenIM accepts send while channel mute is on | implemented for owner; remote pending |
| Owner/admin tries to unmute channel | OpenIM rejects `cancel_mute_group` / SDK mute=false | implemented for owner; remote pending |
| OpenIM app admin tries to unmute channel | OpenIM rejects it too | guard implementation; remote pending |
| Ordinary group mute/unmute | Existing behavior still works | guard compatibility unit test; remote pending |
| Pin a channel message as owner/admin | 201; appears in pinned list | implemented; remote pending |
| Pin as non-admin | 403 | pending |
| Unpin a message | 204; no longer listed | implemented; remote pending |
| Duplicate pin | Idempotent success or 409 as documented; no duplicate row | repository unit test; remote pending |
| OpenIM unavailable during channel create | Upstream error; no channel row left behind | implementation; remote pending |

Evidence to record for each deployed run: platform build/version, OpenIM build/version, timestamp, scenario status, and sanitized response/error details. Never record access tokens.
