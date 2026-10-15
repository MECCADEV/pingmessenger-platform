# Custom message formats

PingMessenger uses OpenIM's normal message transport. OpenIM forwards the
payload; the `pingmessenger.*.v1` schema identifies how a client renders it.
Clients must treat the schema and version as untrusted input and ignore an
unknown schema rather than guessing its meaning.

## Ownership and wire types

| Kind | Schema | OpenIM content type | Wire format | Producer |
| --- | --- | ---: | --- | --- |
| Contact | `pingmessenger.contact.v1` | 108 | native OpenIM `cardElem` | client |
| Location | `pingmessenger.location.v1` | 109 | native OpenIM `locationElem` | client |
| Attachment/document | `pingmessenger.attachment.v1` | 105 | native OpenIM `fileElem` | client, after platform upload |
| Image | `pingmessenger.image.v1` | 102 | native OpenIM `pictureElem` | client |
| Video | `pingmessenger.video.v1` | 104 | native OpenIM `videoElem` | client |
| Other media | `pingmessenger.media.v1` | 110 | custom JSON envelope | client |
| Status reply | `pingmessenger.status_reply.v1` | 110 | custom JSON envelope | client |
| Payment | `pingmessenger.payment.v1` | 110 | payment JSON projection | server only |

The Go definitions and validation helpers are in
`internal/messages/protocol.go`. They use `github.com/goccy/go-json`, the same
JSON implementation used by the platform's OpenIM adapter. The package rejects
payment payloads from client code and rejects attempts to encode native element
messages as custom JSON.

## Custom JSON envelope

The `content` sent to OpenIM contains the envelope JSON as its `data` string:

```json
{
  "schema": "pingmessenger.status_reply.v1",
  "type": "status_reply",
  "version": 1,
  "data": {
    "reply_to_message_id": "message-1",
    "status": "read",
    "updated_at_ms": 1730000000000
  }
}
```

Contact uses native `cardElem` fields `userID`, `nickname`, `faceURL`, and `ex`.
Location uses native `locationElem` fields `latitude`, `longitude`, and
`description`. The platform PingID/username may be carried in the card's `ex`
string when the client wants to share it. A status reply uses
`reply_to_message_id`, `status` (`sent`,
`delivered`, `read`, `failed`, `edited`, or `revoked`), optional `text`, and a
positive `updated_at_ms`.

Native examples (the SDK builders produce the same shapes) are:

```json
{"contentType":108,"cardElem":{"userID":"openim-user-id","nickname":"Alice","faceURL":"https://signed/avatar","ex":"{\"ping_id\":\"123456789012\"}"}}
{"contentType":109,"locationElem":{"latitude":20.1,"longitude":73.2,"description":"Pune"}}
{"contentType":105,"fileElem":{"sourceUrl":"short-lived-signed-url","fileName":"report.pdf","fileSize":123456,"uuid":"document-id"}}
```

For native images and videos, use the SDK's `pictureElem`/`videoElem` builders;
their source and thumbnail URLs are the signed URLs returned by the authenticated
upload flow. Do not substitute a permanent S3 object URL.

The media envelope is for clients that need a renderer outside OpenIM's native
elements:

```json
{
  "schema": "pingmessenger.media.v1",
  "type": "media",
  "version": 1,
  "data": {
    "media_type": "image",
    "source_url": "short-lived-signed-url",
    "thumbnail_url": "short-lived-signed-thumbnail-url",
    "mime_type": "image/jpeg",
    "size_bytes": 12345,
    "width": 640,
    "height": 480
  }
}
```

`media_type` may be `image`, `video`, or `audio`. Image/video clients should
prefer OpenIM's native `pictureElem`/`videoElem` (content types 102/104) when
they want SDK-native rendering.

## Documents, media URLs, and payments

Documents are uploaded through authenticated `POST /v1/documents`. The
response contains a retention-bound signed URL and metadata; send that metadata
as the native OpenIM `fileElem` with content type 105. Signed URLs are bearer
capabilities, so clients must not persist or share them and must request a new
URL after expiry. The same rule applies to image, video, and custom media URLs.

Payments are never client-created. The platform ledger creates
`pingmessenger.payment.v1` (`payment.created` and `payment.updated`) through
`internal/payments.EncodePaymentMessage`, projects it to OpenIM with content
type 110, and includes the monotonically increasing `version`. Clients replace
an existing payment card only when the version increases. A client-supplied
payment-shaped message is not a ledger event and must not be treated as one.

## Compatibility rules

1. Keep `schema` and `type` stable for a version; add optional fields only.
2. Increment the schema suffix for a breaking change and keep the old decoder
   during the client rollout window.
3. Unknown kinds, schemas, versions, and invalid coordinates/status values are
   ignored by clients.
4. OpenIM sender/receiver authorization still applies. The custom payload does
   not grant access to a user, conversation, or object.
