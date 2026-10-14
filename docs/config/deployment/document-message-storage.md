# Document message storage deployment record

This record is the source of truth for the AWS and Kubernetes changes required
by PingMessenger document messages. It deliberately contains names and policy
templates, never credentials.

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

The document bucket must remain separate from the OpenIM object bucket. The
platform cleanup worker is the only component responsible for deleting message
document objects; OpenIM does not own document retention metadata.

## Platform role policy

Attach this policy to the existing `shark-pingmessenger-api` role. The bucket
ARN is intentionally explicit and the object permissions are limited to the
`message-documents/` prefix.

```json
{
  "Version": "2012-10-17",
  "Statement": [
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

## OpenIM role policy

The OpenIM role is separate and applies only to the existing OpenIM object
bucket. Its policy must not grant access to the document bucket:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ManageOpenIMObjects",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::pingmessenger-openim-403048695675-ap-south-1/*"
    }
  ]
}
```

If the OpenIM adapter requires bucket listing during startup, add a separate
`s3:ListBucket` statement for that bucket only. Do not reuse the platform
document policy.

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
  '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
```

## Verification

```bash
aws s3api head-bucket --bucket pingmessenger-message-documents-production
kubectl -n pingmessenger get sa pingmessenger-api \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}{"\n"}'
kubectl -n openim get deploy openimserver-openim-rpc-third \
  -o jsonpath='{.spec.template.spec.serviceAccountName}{"\n"}'
```

After deployment, upload a small allowed document, confirm the returned
`fileElem.sourceUrl` downloads successfully, delete it, and verify the object
is absent from S3 while the `message_documents` row remains as a tombstone.
