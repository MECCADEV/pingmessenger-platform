# Blocked-user status and visibility

## Goal

Allow user-specific lookups to signal when the target has blocked the caller. Filter those accounts from broad/random search, preserve exact search, and prevent profile images and stories from being shown to the blocked caller. Do not expose a list endpoint for incoming blockers. Keep the existing outgoing list, block, and unblock APIs. Bump the Platform API minor version, maintain the OpenAPI contract and changeset, add E2E coverage, and deploy to production.

## Design

Use OpenIM's blacklist as the source of truth. Add an internal directional status check usable by Platform; the public Platform API returns the stable status code `BLOCKED_BY_USER` on a targeted profile/status lookup and in exact-search results. Do not return the target's image or stories when the code is present. Broad username/discovery search filters those targets out. A caller can unblock only accounts in their own outgoing block list using the existing DELETE endpoint; an incoming block remains controlled by its owner.

## Phases

1. Add directional block-status lookup to OpenIM's internal API and Platform's OpenIM client.
2. Add Platform user status/profile endpoint and filter broad search while preserving exact search with the status code.
3. Enforce story/profile visibility and add end-to-end cases for both block directions and caller unblock.
4. Bump the API minor version, update OpenAPI and changeset.
5. Review implementation and verify the plan against the diff.
6. Deploy all required images and verify the production API and visibility behavior.
