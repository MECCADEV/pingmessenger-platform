# Channels API

Channels are PingMessenger records backed by one OpenIM group. A channel has a stable UUID in PingMessenger and a one-to-one OpenIM group ID beginning with `ch_`. New ordinary OpenIM groups receive `gp_` IDs; existing legacy IDs remain supported.

Every endpoint below requires a PingMessenger bearer token. The platform creates OpenIM groups with group-wide mute enabled. OpenIM rejects attempts to clear mute on `ch_` groups, so ordinary members cannot post while the channel owner and admins can. Clients join or open the OpenIM conversation using the returned `openim_group_id`.

## Create a channel

`POST /v1/channels`

```json
{
  "name": "Product updates",
  "description": "Official product news",
  "photo_url": "https://cdn.example.com/product.png",
  "visibility": "public"
}
```

`visibility` is `public` or `private`. Public groups allow direct joining and appear in search. Private groups are hidden from search and require approval to join. The response contains the channel record, including its PingMessenger `id`, OpenIM `openim_group_id`, profile, and member count.

## Search public channels

`GET /v1/channels/search?q=product&limit=20&offset=0`

Search matches channel name and description. Only public channels are returned. Each result includes `photo_url`, `description`, `openim_group_id`, and a live `member_count` read from OpenIM. `limit` defaults to 20 and must be 1–50; `offset` defaults to 0 and is capped at 10,000.

## Read channel details

`GET /v1/channels/{channelID}`

Public details are visible to signed-in users. Private details are returned only to group members. A private channel that is missing or inaccessible returns 404.

## Pin posts

- `POST /v1/channels/{channelID}/pins` with `{"message_id":"<OpenIM clientMsgID>"}` pins a post. Only the channel owner or an OpenIM group admin can pin.
- `DELETE /v1/channels/{channelID}/pins/{messageID}` unpins a post. Only the channel owner or an OpenIM group admin can unpin.
- `GET /v1/channels/{channelID}/pins` lists pinned message IDs for channel members.

The platform stores a reference to the OpenIM message ID and pin metadata; message contents remain in OpenIM.

## Resolve a channel by its OpenIM group ID

`GET /v1/channels/by-openim-group/{groupID}` resolves a channel catalog record. Public profiles may be resolved by any signed-in user. Private profiles require current group membership, so a shared deep link does not expose private metadata or grant access.

## Edit or delete a channel

`PATCH /v1/channels/{channelID}` accepts any of `name`, `description`, `photo_url`, and `visibility`. Only the channel owner or a group admin may update it. Visibility changes also update the OpenIM join policy: public channels allow direct joining; private channels require admin approval.

`POST /v1/channels/{channelID}/photo` uploads multipart field `image` (JPEG, PNG, or WebP, up to 5 MiB). Only the owner or a group admin may change it. `DELETE /v1/channels/{channelID}` dismisses the OpenIM group and removes its channel record; only the owner or a group admin may delete it.

## Private-channel deep links

The app can share a deep link containing the channel's OpenIM group ID, for example `https://<app-link-host>/ch/<openim_group_id>`. Opening it calls the OpenIM SDK join-group operation. For private channels, OpenIM creates a pending join request; a channel owner or group admin approves or refuses it in the SDK. The link does not make private channels searchable and does not bypass the approval policy.

## Like a post

- `GET /v1/channels/{channelID}/posts/{messageID}/like` returns `count` and `liked_by_me`.
- `PUT /v1/channels/{channelID}/posts/{messageID}/like` adds the caller's Like idempotently.
- `DELETE /v1/channels/{channelID}/posts/{messageID}/like` removes it.

Only current channel members may read or change Like counts. The message ID is the OpenIM client message ID.

## Post retention

The current 1–24 hour auto-delete preference is the app's local chat-retention setting. It applies to channel messages cached by that app installation, just as it does to other chats. It does not revoke the OpenIM message for every member; channel-wide server-enforced deletion is not currently available.
