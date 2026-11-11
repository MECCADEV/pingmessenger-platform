# Terms acceptance tracking and enforcement

## Scope

Add per-account Terms of Use acceptance version/time; allow optional acceptance at signup and explicit signed-in acceptance; expose accepted/current versions in profile and login responses; block OpenIM chat token issuance plus story and friend APIs until the currently configured terms version is accepted. Keep profile, acceptance, logout, password change, and account deletion available. Deploy the configured version `2026-11-10` and return the changeset’s main-branch link.

## Design

- Store `terms_accepted_version` and `terms_accepted_at` on `users` so acceptance follows the account across sessions/devices.
- Configure `TERMS_REQUIRED_VERSION`, defaulting to `2026-11-10`, and expose it consistently in login/signup/profile responses.
- Acceptance endpoint records the submitted version with server time and only accepts the currently required version.
- A shared route guard checks current acceptance and returns HTTP 403 `{ "error": "terms_not_accepted" }`.
- Apply the guard to chat token issuance, all authenticated story endpoints, and all friend endpoints. Leave explicitly exempt account/profile/auth operations open.
- Update OpenAPI, Helm config, migration, integration coverage, and release changeset.

## Verification

- Focused tests cover optional signup, account acceptance, response fields, exact-version enforcement, and gated/ungated routes.
- Full Go test/vet/e2e compile and generated OpenAPI comparison pass.
- CI passes, production migration/rollout succeeds, and live health/OpenAPI plus version-enforcement behavior are checked.
