# OpenAI subscription connection: production admission

Status: implementation in progress; runtime admission remains **closed**. This increment is validated only with local fixtures. No real login, native Keychain diagnostic, catalog query, inference, refresh, retry, or paid-API fallback is authorized by installing it.

## Two independent gates

The existing Settings → Models workbench and connection approval ledger remain in place. A production connection requires both a trusted runtime admission policy and a fresh, single-use connection approval. Neither replaces the other.

1. A constructor-owned policy names one enabled OpenAI subscription account and its immutable `live` route, pins the existing official SIWC profile, and requires a host-owned credential-readiness check. The shipped application supplies no enabled policy. No HTTP field, environment variable, CLI flag, saved account setting, or synthetic diagnostic can enable it in this increment.
2. A proposal freezes the account revision, route, credential/authorization metadata, requested scopes/resource, admission generation, approval expiry, and login lifetime in its material digest. Approval does not start a listener or open a browser. Explicit execution durably consumes the approval before starting one owned connection attempt.

Admission is for `connection` + `subscription_only` + `browser_handoff` only. Catalog, inference, task dispatch, refresh, automatic retries, scripts, Git writes, and paid-API fallback retain their existing closed boundaries. Kimi is unchanged.

## Provenance and lifetime

Production material has an explicit production provenance and a process-owned admission generation. Local simulation material, receipts, credentials, routes, and approvals cannot be promoted into production. Recreating the admitted controller requires a new proposal; old approval material is not silently rebound.

The credential-readiness predicate is trusted host integration, not client-supplied evidence: absent, failing, or stale readiness denies admission. This release does not expose a mechanism to assert native readiness from JSON, import a probe report, or perform a Keychain probe at startup. A future operator activation must establish this prerequisite after separate user confirmation. Synthetic readiness in tests is never evidence about this machine.

Approval material expires after 10 minutes. Explicit execution starts a separate maximum 600-second human-login window; individual protocol requests retain their 30-second bound. Admission policy and current material are rechecked at execution. The adapter independently verifies the durable running claim and reserves its single dispatch before callback binding, host registration, credential access, or provider I/O. Duplicate dispatches and recovered/interrupted claims cannot replay.

## Protocol and evidence

The existing fixed protocol adapter is reused without endpoint customization. It requests `openid profile email offline_access resource.invoke chatgpt.tokens.use.direct` with resource `https://api.openai.com/v1`, uses fresh state/nonce/PKCE S256, and validates the issued client ID and signed identity before credential activation. Account and workspace selection remain the user's browser action. Requested permissions are not proof of granted permissions or available quota. See the [official registration guide](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), checked 2026-10-20.

Only bounded, sanitized connection evidence enters the existing ledger. OAuth URLs, codes, state, verifier, tokens, and provider response bodies do not. UI coordination exposes only the owned loopback handoff, never a raw authorization URL. A valid identity without plan permission does not authorize inference; no failure chooses another billing path. See [official errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery).

## Failure and recovery

Cancellation, login expiry, process shutdown, and receipt failures reuse the owned callback join and credential cleanup journal. Unknown outcomes remain consumed and require manual investigation and a new proposal; they never imply rollback of a remote grant. Recovery closes mutation admission before reconciling unfinished claims. A readiness check failing after approval denies dispatch; it does not retry or start a replacement login. Local cleanup failures fail closed and remain visible through existing recovery evidence.

## Delivery and acceptance

- Preserve historical simulation digests and existing simulation behavior.
- Exercise default denial, forged policy/material, stale account/route/generation, wrong phase, duplicate dispatch, readiness loss, cancellation, expiry, interrupted execution, receipt failure, and cleanup failure using only memory credentials and local protocol fixtures.
- Verify production-profile protocol dispatch offline, and keep every shipped runtime closed by default.
- Keep navigation/layout unchanged. Show a read-only production-admission preparation summary in the existing account check, including the unperformed native check; do not offer an enable/login action.
- Run full tests, race checks, secret/vulnerability scans, a clean-source export, and browser acceptance before pushing reviewed public source. Check CI for that exact commit; do not create a Release.

## Next operator step (not performed)

Separately confirm native Keychain validation and then one real subscription connection, including the full requested scope set and the user's account/workspace selection. Only after those confirmations may operator activation be wired. Catalog and inference require later, separate approval and remain unavailable here.
