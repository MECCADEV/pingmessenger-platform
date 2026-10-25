# Document upload retry contract

`POST /v1/documents` accepts an optional `Idempotency-Key` header. Mobile
clients should generate and durably persist one random key per upload before
the first request, then reuse that key and the exact original file for every
retry, including after app restart. The key is scoped to the authenticated
Platform owner and remains bound to the document tombstone; clients must not
reuse it for another upload.

For keyed requests, Platform fingerprints the sanitized filename, normalized
MIME type, byte count, and SHA-256 of the file bytes. The key and fingerprint
are committed with the reserved document row before the object-store write.
The repository's per-owner row lock serializes first attempts across server
replicas. A matching replay writes to the original object key and returns the
original document ID, so a retry can recover a response lost after storage or
complete an interrupted object write without reserving another document or
creating another object. Upload quota is charged only for the first reservation.

Responses:

- `201 Created`: first successful keyed or legacy (unkeyed) upload.
- `200 OK`: matching keyed replay; response shape is the same as `201`.
- `409 Conflict`: the owner already used the key with a different fingerprint.
- `410 Gone`: the original document was explicitly deleted or expired; replay
  never resurrects a tombstoned document.
- `422 Unprocessable Entity`: malformed key (1–128 visible ASCII characters).

The key ledger is stored with the document and cascades only when its owning
account or document row is removed. Document rows are retained as message
history tombstones, so keys are not reusable after the seven-day media window.
The header remains optional for older clients; unkeyed uploads retain their
legacy non-idempotent behavior.

The reservation and object store cannot participate in one distributed
transaction. For keyed requests, failures after reservation preserve the
reservation so an exact retry can overwrite the same object key and continue.
An incomplete reservation may consume the owner's retained-document slot
until normal expiry/cleanup. Operators should monitor failed object writes and
keep the client feature flag disabled until migration `0028` and this API
contract are deployed.
