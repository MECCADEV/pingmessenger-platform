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

Keep `/openim/msg/send_msg` private: clients upload through `POST
/v1/documents`, then send the returned `fileElem` over their authenticated
OpenIM WebSocket. Platform cleanup deletes document objects at seven days or
when the owner explicitly deletes them while retaining message history.
