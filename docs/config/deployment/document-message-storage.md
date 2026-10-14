# Document message storage deployment record

This record is the source of truth for the AWS and Kubernetes changes required
by PingMessenger document messages. It deliberately contains names and policy
templates, never credentials.

The production resources in this record were provisioned in account
`403048695675`, region `ap-south-1`, on 2026-10-14. Re-running the commands is
safe; IAM policies are applied with `put-role-policy` and bucket settings are
declarative.

## Resources

| Resource | Production value | Owner |
| --- | --- | --- |
| Document bucket | `pingmessenger-message-documents-production` | Platform API |
| Platform namespace | `pingmessenger` | Platform API |
| Platform service account | `pingmessenger-api` | Platform API |
| Platform IRSA role | `arn:aws:iam::403048695675:role/shark-pingmessenger-api` | Platform API |
| OpenIM namespace | `openim` | OpenIM |
| OpenIM object service | `openimserver-openim-rpc-third` | OpenIM |
| OpenIM object bucket | `pingmessenger-openim-403048695675-ap-south-1` | OpenIM |
| OpenIM IRSA role | `arn:aws:iam::403048695675:role/shark-openim-rpc-third` | OpenIM |

The `openim/openim-rpc-third` service account has been created and annotated
with the OpenIM role. The currently running Helm release still uses the
`default` service account and its existing MinIO configuration; upgrade the
OpenIM release from the document-support Helm worktree before enabling AWS
object traffic. This avoids an untracked live-patch that Helm would overwrite.

The document bucket must remain separate from the OpenIM object bucket. The
platform cleanup worker is the only component responsible for deleting message
document objects immediately after a document is deleted or expires. The
document bucket also has a seven-day S3 lifecycle rule as a defense-in-depth
cleanup for abandoned objects; OpenIM does not own document retention metadata.

## Platform role policy

Attach this policy to the existing `shark-pingmessenger-api` role. The bucket
ARN is intentionally explicit and the object permissions are limited to the
`message-documents/` prefix.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "GetDocumentBucketLocation",
      "Effect": "Allow",
      "Action": "s3:GetBucketLocation",
      "Resource": "arn:aws:s3:::pingmessenger-message-documents-production"
    },
    {
      "Sid": "ListDocumentPrefix",
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::pingmessenger-message-documents-production",
      "Condition": {"StringLike": {"s3:prefix": ["message-documents/*"]}}
    },
    {
      "Sid": "ManageDocumentObjects",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::pingmessenger-message-documents-production/message-documents/*"
    }
  ]
}
```

The application does not need `s3:CreateBucket`, `s3:PutBucketPolicy`, ACL
permissions, or public-read permissions. Keep Block Public Access enabled and
use the platform's SigV4 URLs for `fileElem.sourceUrl`.

The existing `pingmessenger-private-s3` policy on the platform role is retained
for legacy avatar/story media in the OpenIM bucket. It is not used by document
messages and does not grant the document bucket outside the policy above.

## OpenIM role policy

The OpenIM role is separate and applies only to the existing OpenIM object
bucket. Its policy must not grant access to the document bucket:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "OpenIMBucketMetadata",
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketLocation",
        "s3:ListBucket",
        "s3:ListBucketMultipartUploads"
      ],
      "Resource": "arn:aws:s3:::pingmessenger-openim-403048695675-ap-south-1"
    },
    {
      "Sid": "ManageOpenIMObjects",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts"
      ],
      "Resource": "arn:aws:s3:::pingmessenger-openim-403048695675-ap-south-1/*"
    }
  ]
}
```

The `OpenIMBucketMetadata` statement also grants `s3:GetBucketLocation`,
`s3:ListBucket`, and `s3:ListBucketMultipartUploads` on that bucket because the
OpenIM adapter uses multipart uploads. Do not reuse the platform document
policy.

The role trust policy is restricted to the EKS OIDC provider and the
`system:serviceaccount:openim:openim-rpc-third` subject:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::403048695675:oidc-provider/oidc.eks.ap-south-1.amazonaws.com/id/F8040131F608B63A5A931CA13CB30C0A"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "oidc.eks.ap-south-1.amazonaws.com/id/F8040131F608B63A5A931CA13CB30C0A:aud": "sts.amazonaws.com",
        "oidc.eks.ap-south-1.amazonaws.com/id/F8040131F608B63A5A931CA13CB30C0A:sub": "system:serviceaccount:openim:openim-rpc-third"
      }
    }
  }]
}
```

## Apply order

Run these steps with an AWS administrator session, then deploy the application
and OpenIM Helm releases:

1. Create the document bucket in `ap-south-1` if it does not already exist.
2. Enable S3 Block Public Access and default encryption on both buckets.
3. Attach the platform policy above to `shark-pingmessenger-api`.
4. Create/attach the OpenIM policy to `shark-openim-rpc-third`.
5. Annotate `pingmessenger/pingmessenger-api` with the platform role and
   `openim/openim-rpc-third` with the OpenIM role.
6. Apply migration `0017_message_documents.sql` through the normal platform
   migration job.
7. Deploy the platform image and verify the document environment variables:
   `DOCUMENT_S3_BUCKET`, `DOCUMENT_MAX_BYTES`, `DOCUMENT_MAX_COUNT`,
   `DOCUMENT_MAX_BYTES_24H`, and `DOCUMENT_RETENTION`.

Example bucket setup (safe to rerun after checking existence):

```bash
aws s3api head-bucket --bucket pingmessenger-message-documents-production || \
  aws s3api create-bucket \
    --bucket pingmessenger-message-documents-production \
    --region ap-south-1 \
    --create-bucket-configuration LocationConstraint=ap-south-1

aws s3api put-public-access-block \
  --bucket pingmessenger-message-documents-production \
  --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

aws s3api put-bucket-encryption \
  --bucket pingmessenger-message-documents-production \
  --server-side-encryption-configuration \
  '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"},"BucketKeyEnabled":true}]}'

aws s3api put-bucket-ownership-controls \
  --bucket pingmessenger-message-documents-production \
  --ownership-controls 'Rules=[{ObjectOwnership=BucketOwnerEnforced}]'

aws s3api put-bucket-lifecycle-configuration \
  --bucket pingmessenger-message-documents-production \
  --lifecycle-configuration \
  '{"Rules":[{"ID":"expire-message-documents","Status":"Enabled","Filter":{"Prefix":"message-documents/"},"Expiration":{"Days":7}},{"ID":"abort-incomplete-multipart","Status":"Enabled","Filter":{},"AbortIncompleteMultipartUpload":{"DaysAfterInitiation":1}}]}'
```

## Verification

```bash
aws s3api head-bucket --bucket pingmessenger-message-documents-production
aws iam get-role-policy --role-name shark-pingmessenger-api \
  --policy-name pingmessenger-message-documents
aws iam get-role-policy --role-name shark-openim-rpc-third \
  --policy-name pingmessenger-openim-s3
kubectl -n pingmessenger get sa pingmessenger-api \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}{"\n"}'
kubectl -n openim get sa openim-rpc-third \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}{"\n"}'
kubectl -n openim get deploy openimserver-openim-rpc-third \
  -o jsonpath='{.spec.template.spec.serviceAccountName}{"\n"}'
```

The applied IRSA probes successfully assumed both roles and performed
put/head/delete against their respective buckets. After the Helm deployment,
upload a small allowed document, confirm the returned `fileElem.sourceUrl`
downloads successfully, delete it, and verify the object is absent from S3
while the `message_documents` row remains as a tombstone.
