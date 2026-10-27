# Production deployment

The production deployment is intentionally manual and runs from the GitHub
`production` environment. Add the repository's release approvers as environment
reviewers before enabling unattended production promotion. The workflow is
`.github/workflows/deploy-production.yml`.

## GitHub environment secrets

Configure this secret on both the protected `production` environment and the
unattended `production-maintenance` environment:

- `VAULT_TOKEN`

The workflows use the fixed, non-sensitive Vault endpoint
`https://vault.meainternal.com`; `VAULT_TOKEN` should be a renewable,
least-privilege token. It is used by the
Vault action and by the scheduled token-renewal workflow; its value is never
written to a file or step summary.

The deploy job may require reviewers through `production`; the maintenance
environment should not require a reviewer because scheduled renewals must run
unattended.

## Vault contract

The deploy token must be able to read KV v2 path
`secret/data/pingmessenger/platform/deploy`, containing:

`REGISTRY_USER`, `REGISTRY_PASSWORD`, `IMAGE_REPOSITORY`,
`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, `EKS_CLUSTER`,
`KUBE_NAMESPACE`, `SERVICE_NAME`, `DEPLOYMENT_NAME`, `CONTAINER_NAME`,
`MIGRATION_CONTAINER`, and `ROLLBACK_ON_FAILURE`.

The least-privilege policy template is
`deploy/vault/github-deploy-policy.hcl`; it grants only deployment-config read
and self lease lookup/renew capabilities.

The values should identify the existing production deployment. For the current
cluster they are the `pingmessenger` namespace, `pingmessenger-api` service and
deployment, `api` container, and `migrate` init container. The workflow updates
only those two image references, so the existing runtime Secret, service
account, ingress, PVC, and Helm release remain intact.

## Deploying

Run **Deploy PingMessenger platform to production** from the GitHub Actions UI,
select the branch/ref, and optionally provide an immutable image tag. The job
builds `linux/amd64`, pushes the image to the private registry, configures an
isolated EKS kubeconfig, updates the deployment, waits for rollout, and checks
`/healthz` plus `/openapi.json`. A failed rollout can be automatically undone
when `ROLLBACK_ON_FAILURE` is `true`.

The scheduled **Renew PingMessenger deployment Vault token** workflow runs
weekly. It renews the GitHub environment token for 31 days and fails closed if
the token is non-renewable or Vault returns a short lease. If the token is not
renewable, replace the GitHub environment secret before the next deployment.

The workflow does not print application or registry secret values and does not
recreate the production database or application Secret.

## Document message storage

The feature-specific AWS and IRSA record is
[`docs/config/deployment/document-message-storage.md`](config/deployment/document-message-storage.md).

## Public profile photos

Profile photos use stable URLs under `S3_PUBLIC_BASE_URL/profiles/`. The
application saves these URLs in `profile_assets` and OpenIM `faceURL`; friends,
search results, and profile responses reuse the same URL. Keep the configured
host and `/profiles/` path readable without login. For the AWS bucket configured
by `storage.bucket`, one option is this bucket policy, replacing the bucket
name with the deployed value:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "PublicReadProfilePhotosOnly",
    "Effect": "Allow",
    "Principal": "*",
    "Action": "s3:GetObject",
    "Resource": "arn:aws:s3:::pingmessenger-openim-403048695675-ap-south-1/profiles/*"
  }]
}
```

This grants anonymous reads only for `profiles/*`; it does not grant access to
story media or documents. If S3 Block Public Access prevents this policy, keep
the bucket private and configure the CDN with an origin access identity scoped
to `profiles/*`, then point `storage.publicBaseURL` at that CDN. Do not use a
public bucket-wide policy. The current URL shape stays unchanged, so existing
database URLs and OpenIM `faceURL` values need no rewrite.

The S3 adapter also supports expiring presigned GET URLs (`PresignGet`). The
platform uses those for private story and document downloads, but profile
photos do not have a signed-URL issuance endpoint. A presigned URL is a bearer
link that expires, so it should not be stored in profile or OpenIM records as a
permanent avatar URL.

The platform document API uses a dedicated private S3 bucket (for example
`pingmessenger-message-documents-production`) and a pod workload identity/IRSA
role. OpenIM's `openim-rpc-third` object service keeps its existing separate
OpenIM object bucket and has its own IRSA role. Grant the Platform role only
`s3:PutObject`, `s3:GetObject`, and `s3:DeleteObject` on the document bucket
prefix; grant the OpenIM role the equivalent access only to its OpenIM bucket.
Set `DOCUMENT_S3_BUCKET` and the document policy variables in the runtime
Secret or Helm values; do not add AWS access keys to either pod.

The public OpenIM gateway must include this exact rewrite route:

```text
/openim/object(/|$)(.*)  ->  openim-api /object/$2
```

The checked-in `openim-client-api` HTTPRoute also exposes the authenticated
`/openim/msg` group. That group is not part of the Platform OpenAPI document:
it is an OpenIM gateway surface and must be described and versioned with the
OpenIM protocol. Mobile clients should send normal chat messages over the
authenticated OpenIM WebSocket; the Platform service may use the internal
`/msg/send_msg` HTTP call for server-created projections. Neither path is
idempotent by `clientMsgID` today, so callers must not retry an unknown send
without a client-side deduplication strategy and a matching backend contract.
Clients upload through `POST /v1/documents`, then send the returned `fileElem`
over their authenticated OpenIM WebSocket. Platform cleanup deletes document
objects at seven days or when the owner explicitly deletes them while retaining
message history.

Account deletion is intentionally a soft-delete. `POST /v1/account/delete`
locks the user row, sets `users.status=deleted` and `users.deleted_at`, and
timestamps revocation/consumption of all platform-issued session, OpenIM-token,
MFA, and password-challenge rows. It does not call OpenIM `user/delete_users`,
delete application rows, or delete S3 objects. Login, passwordless login,
refresh, and session creation all require an active, non-deleted user, so a
deleted identity cannot be reactivated or reused. Retained OpenIM/payment
history remains available to the configured retention/audit process.
