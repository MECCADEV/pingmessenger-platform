# Notification preferences API contract

This is the maintained Platform/OpenIM contract for server-managed chat push
preferences. The source and migration in this checkout are not deployed until
they are merged and rolled out.

## Authenticated user routes

- `GET /v1/settings/notifications` returns effective account defaults.
- `PUT /v1/settings/notifications` updates one or more supplied fields.
- `GET /v1/conversations/{conversationID}/notifications` returns the effective
  account settings after per-conversation overrides.
- `PUT /v1/conversations/{conversationID}/notifications` updates the supplied
  override fields. Omitted fields are preserved. `DELETE` on the same path
  removes all overrides and restores inheritance from account settings.

All user routes require the Platform Bearer token. Supported settings are:

```json
{
  "enabled": true,
  "mode": "all",
  "sound": "default",
  "max_update_frequency_seconds": 2
}
```

`mode` is `all`, `mentions_only`, or `none`; `sound` is `default` or `silent`;
the frequency is 2, 5, or 10 seconds. Missing account rows resolve to
`enabled/all/default/2`. Conversation overrides inherit each unset field.
Deleting an override resets every field to the account-level value. This is
single-tenant today: the owner is the authenticated Platform user and the
opaque conversation ID is scoped to that owner. The handler does not validate
conversation membership against OpenIM; the value cannot read or modify any
other user's settings.

## Private OpenIM resolver

`POST /v1/internal/notifications/resolve` requires the dedicated
`X-OpenIM-Service-Token` header, configured on Platform as `OPENIM_SERVICE_TOKEN`
and on OpenIM as `notificationPreferences.serviceToken`. Use a high-entropy
shared secret over a private TLS/network path. When Platform has no token
configured, the route returns `503`; an incorrect token returns `401`.

Request:

```json
{
  "openim_user_ids": ["openim-user-1", "openim-user-2"],
  "conversation_id": "group-or-single-chat-id"
}
```

There must be 1–1000 recipient IDs (each at most 128 characters);
`conversation_id` is 1–256 characters. The response maps only known, active,
non-deleted Platform users to their effective preference:

```json
{
  "preferences": {
    "openim-user-1": {
      "enabled": true,
      "mode": "mentions_only",
      "sound": "silent",
      "max_update_frequency_seconds": 5
    }
  }
}
```

Unknown and inactive IDs are omitted; the current OpenIM sender suppresses
those recipients and does not treat omission as a resolver outage. The resolver
trusts the authenticated OpenIM service to provide the recipient set and
conversation ID and does not cross-check OpenIM membership. Platform OpenAPI
documents this route with the dedicated service-auth scheme; generated
`openapi/openapi.json` is checked in.

The SQL write uses one `INSERT ... ON CONFLICT` statement, merging supplied
fields atomically so simultaneous updates to different fields do not lose each
other. Database constraints enforce allowed values and frequency choices.
Global rows and per-conversation rows are owned by the user UUID and cascade on
account deletion. `internal/notifications/preferences_integration_test.go`
exercises overlapping updates, inheritance/reset, and resolver filtering with
`TEST_DATABASE_URL` against an isolated schema.

## Delivery guarantees and remaining deployment work

This contract only stores and resolves preferences. It does not itself send
FCM, update native Android channels, coalesce pushes, clear OS notifications on
read, or guarantee a background delivery. OpenIM's sender/coalescer must consume
the resolver response. The failure mode (`fail-open` or `fail-closed`) must be
explicitly configured on OpenIM; selecting a failure policy is a product
decision not encoded by the API. End-to-end delivery, channel behavior, read
cleanup, and restart/race behavior require deployment and integration tests.
