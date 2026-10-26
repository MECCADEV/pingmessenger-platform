# Friend contact discovery contract

`POST /v1/friends/discover-network` checks whether supplied email addresses or
phone numbers match verified contacts belonging to other active PingMessenger
accounts. This document describes the FastHTTP implementation in
`internal/httpapi/routes_friends.go`; the generated OpenAPI contract is
`openapi/openapi.json`.

## Request

- Authentication: `Authorization: Bearer <access-token>`. The token must refer
  to an active session.
- Content type: `application/json`.
- Body fields are independently optional:

```json
{
  "emails": ["person@example.test"],
  "mobile_no": ["+12025550123"]
}
```

Each field may contain at most 100 strings. At least one field must contain at
least one value. An empty object, omitted fields, or two empty arrays return
422. Unknown JSON fields and trailing additional JSON values are rejected.

Email values must pass email validation, then are trimmed and lowercased before
matching. Phone values may contain at most 32 characters before trimming; they
are trimmed and otherwise compared exactly, and are not canonicalized by this
endpoint.
Contacts added through the account contact-verification flow are stored as
E.164 phone numbers (for example, `+14155552671`), so clients should submit
phone numbers in that same form to match reliably.

## Response and matching

On success, the response is HTTP 200:

```json
{"open_im_user_ids":["openim-user-id"]}
```

The values are distinct OpenIM user IDs, not Platform account UUIDs. The
database match excludes the requesting account and only returns users whose
matching contact is verified, whose account status is `active`, and whose
OpenIM user ID is present. The result order is unspecified. When there are no
matches, the current implementation returns `{"open_im_user_ids":null}` (a
nil Go slice encoded as JSON `null`), not an empty array.

This is a read-only lookup, but it is not a snapshot: results can change as
contacts are verified, accounts change status, or OpenIM identities are linked.
The handler does not currently enforce a discovery-specific rate limit.

## Errors

| HTTP status | Meaning |
| --- | --- |
| 401 | Bearer authentication is missing, invalid, or its session is inactive. |
| 422 | Malformed/invalid JSON, invalid contact values, either list exceeds its limit, or both lists are empty. |
| 500 | The contact lookup failed in the database. |

The endpoint does not currently return 429 from its handler. Do not treat the
generic 429 response listed for some other Platform operations as part of this
contract.

## Client guidance

After contact permission is granted or restored, the client can submit its
eligible local contacts in bounded batches of at most 100 per field. Deduplicate
and normalize values on the client, especially phone numbers to E.164, before
submitting. Treat the response IDs as OpenIM identities; use the documented
friends/profile mapping APIs if Platform account profile data is needed. Do not
assume a match for every supplied contact, and do not infer that an empty or
null result means the local contact list was empty.

Regenerate the checked-in schema after changing the typed contract with
`make openapi`.
