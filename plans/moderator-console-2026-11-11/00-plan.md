# Moderator console and non-blocking terms tracking

## Goal
Ship a React moderator console served by the platform API, protected by a pre-created HTTP Basic Auth moderator account, with report queue/review dashboards. Preserve terms version recording and responses while disabling request blocking for older app builds.

## Phases
1. Reconcile OpenIM deployment, existing report backend, and current production branch.
2. Implement moderator credential configuration, protected report API access/audit actor tracking, and embedded React dashboard.
3. Remove terms acceptance enforcement from chat, stories, and friend routes while retaining acceptance records and response metadata.
4. Add/update changeset, API docs, security and integration/E2E coverage; build and run requested verification.
5. Publish reviewable source changes, deploy through production workflow, and verify live UI/API behavior.

## Runtime gate
Production moderator credentials must be provisioned in the protected GitHub `production` environment and confirmed available to the deploy workflow before rollout. The workflow creates a Kubernetes Secret and the API hashes the password with bcrypt at startup; do not enable the panel without both values.
