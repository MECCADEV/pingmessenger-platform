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
