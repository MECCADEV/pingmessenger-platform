# Channel support backed by OpenIM groups

## Goal

Add channel records to PingMessenger while using OpenIM groups as the transport and membership layer. Each channel owns exactly one OpenIM group ID. New channel IDs use `ch_`; OpenIM-generated ordinary group IDs use `gp_`. Legacy group IDs remain valid and are treated as ordinary groups unless registered as channels.

Channels have public or private visibility. Authenticated users can search public channels; private channels do not appear in directory results and are accessible only to their members. Search results include channel name, description, photo, visibility, OpenIM group ID, and live member count. Creating a channel creates its OpenIM group with group-wide mute enabled, allowing only the owner and admins to post. The OpenIM server must reject any attempt to turn mute off for a `ch_` group, even when the caller is an OpenIM administrator.

Pinned posts are references to channel group messages. Pin, unpin, and list operations are exposed through PingMessenger APIs, with owner/admin authorization.

## Design decisions

- PostgreSQL `channels` is the source of truth for channel visibility and channel metadata; `openim_group_id` is unique and one-to-one with a channel.
- The OpenIM group ID prefix is the server-side guardrail classification. The platform catalog additionally requires a matching channel row before treating a group as a public/private channel.
- Use `ch_` and `gp_` delimiters (not bare `ch`/`gp`) so prefixes are unambiguous. New channel IDs are `ch_` plus a UUID; the OpenIM group server prefixes generated IDs with `gp_`.
- Channel creation must be atomic from the caller’s perspective: create the OpenIM group first, then persist the channel record; if persistence fails, attempt to dismiss the newly created group and return an error.
- Public visibility maps to direct join; private visibility maps to join approval and is omitted from public search. The API does not return private metadata to non-members.
- Group-wide mute is set as part of channel group creation in OpenIM. `CancelMuteGroup` rejects `ch_` IDs regardless of caller role. Ordinary groups keep current mute behavior.
- Member counts come from OpenIM group info, not a potentially stale platform counter.
- Pin storage contains only stable OpenIM message IDs and actor/timestamp metadata. The API does not duplicate message bodies.

## Implementation phases

1. **Tier 1 — OpenIM ID and mute invariants.** In the OpenIM group RPC, generate `gp_` IDs for ordinary groups, reserve `ch_` creation for trusted app-admin calls, create `ch_` groups muted, and reject unmute for every `ch_` group in `CancelMuteGroup`. Add focused unit tests for owner/admin/app-admin paths and ordinary-group compatibility.
2. **Tier 2 — channel persistence and OpenIM client.** Add the channel and pinned-message migrations/repositories, group create/info/member-role methods to the platform OpenIM client, plus unit/integration coverage for SQL constraints, visibility, pagination, and pin uniqueness.
3. **Tier 2 — authenticated channel APIs.** Add create, search, detail, pin, unpin, and pinned-list routes; enforce creator/group admin authorization, public/private visibility, rate limits, OpenAPI types, and public metadata shaping. Add handler tests for auth, validation, visibility and upstream failures.
4. **Tier 3 — e2e and deployment contract.** Add deployed API e2e coverage for channel creation and ID prefix, public/private search, direct vs review join policy, member counts, admin-only posting invariant, mute-off rejection, and pin/unpin/list behavior. Publish a deployment workflow that builds the selected OpenIM commit, rolls the group RPC with current shared discovery config, and waits for readiness before running the live matrix.
5. **Review gate.** Audit both repositories for authorization gaps, OpenIM lifecycle failure cleanup, ID classification collisions, query exposure, and migration/deployment safety. Fix findings and repeat the audit.
6. **Verification gate.** Compare the final code and tests to this plan and record all mismatches in `meta.json`.
7. **Loop gate.** Resolve every verification mismatch; close only when the verification gate has an empty `misalignments` list.
8. **Runtime gate.** After the remote platform and OpenIM deployments are live, run the matrix in `e2e-matrix.md` against the deployment, record request IDs/results, and close only when all required rows pass.

## API proposal

- `POST /v1/channels` — create a channel with `name`, optional `description`/`photo_url`, and `visibility` (`public` or `private`). Returns channel ID, `ch_` OpenIM group ID, profile fields, and visibility.
- `GET /v1/channels/search?q=&limit=&offset=` — authenticated search over public channels only, with rich profile and live member count.
- `GET /v1/channels/{channelID}` — public metadata for public channels; private channel metadata only for a member.
- `POST /v1/channels/{channelID}/pins` — owner/admin pins an OpenIM message ID.
- `DELETE /v1/channels/{channelID}/pins/{messageID}` — owner/admin unpins it.
- `GET /v1/channels/{channelID}/pins` — list pins for a channel member.

## End-to-end scenario matrix

See `e2e-matrix.md`. Production/deployed checks require `E2E_BASE_URL` and a deployment containing both the platform and OpenIM changes. The runtime gate remains open until the remote deployment is available.

## Known risks / deployment constraints

- Platform and OpenIM code are separate repositories and must be deployed compatibly. A platform rollout before OpenIM may fail channel creation; the new endpoints should report an upstream failure without persisting a false channel row.
- The platform production workflow rolls out the API but does not build or roll out the OpenIM group RPC. The channel runtime gate therefore depends on the dedicated OpenIM group rollout workflow, including refreshing mounted `discovery.yml`; the live group pod previously retained stale service discovery and called msg RPC port 80 while the live service exposes 10280.
- Existing group IDs are not rewritten. The `gp_` prefix applies to groups created after the OpenIM patch.
- The app-admin identity may be allowed by OpenIM authorization checks; the channel unmute guard must run before the role bypass.
- Search member counts require one OpenIM group-info lookup per page (batch endpoint preferred); upstream failure should fail the search rather than return fabricated counts.
- Private membership must be checked through OpenIM role/member data, not inferred from a client-supplied group ID.
- Pin routes enforce a message-ID length bound and uniqueness per channel, but OpenIM currently exposes no supported message-by-ID lookup through the platform client. They store references without verifying that the referenced message exists or belongs to the channel; clients should pin actual OpenIM `clientMsgID` values.
