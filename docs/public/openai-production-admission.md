# OpenAI subscription connection: production admission

Status: backend admission and [operator startup entry](openai-operator-entry.md) implemented; normal startup remains **closed**. Validation uses local fixtures only. Installing this source does not authorize real login, native Keychain access, catalog queries, inference, refresh, retries, or paid-API fallback.

## Two independent gates

The existing Settings → Models workbench and connection approval ledger remain in place. A production connection requires both a trusted runtime admission policy and a fresh, single-use connection approval. Neither replaces the other.

1. A constructor-owned policy names one enabled OpenAI subscription account and its immutable `live` route, pins the existing official SIWC profile, and requires a host-owned credential-readiness check. Normal startup supplies no enabled policy. Only the paired explicit operator startup options and a fresh successful in-process journaled native probe can establish this prerequisite. HTTP fields, environment variables, saved settings, synthetic diagnostics and imported probe reports cannot enable it.
2. A proposal freezes the account revision, route, credential/authorization metadata, requested scopes/resource, admission generation, approval expiry, and login lifetime in its material digest. Approval does not start a listener or open a browser. Explicit execution durably consumes the approval before starting one owned connection attempt.

Admission is for `connection` + `subscription_only` + `browser_handoff` only. Catalog, inference, task dispatch, refresh, automatic retries, scripts, Git writes, and paid-API fallback retain their existing closed boundaries. Kimi is unchanged.

## Provenance and lifetime

Production material has an explicit production provenance and a process-owned admission generation. Local simulation material, receipts, credentials, routes, and approvals cannot be promoted into production. Recreating the admitted controller requires a new proposal; old approval material is not silently rebound.

The credential-readiness predicate is trusted host integration, not client-supplied evidence: absent, failing, or stale readiness denies admission. There is no JSON assertion or probe-report import. The paired operator opt-in, after separate user confirmation, runs a new exact-item Keychain diagnostic in-process and creates a 10-minute readiness lease only after confirmed cleanup and journal persistence. Ordinary startup never runs that probe. Synthetic readiness in tests is never evidence about this machine.

Approval material expires after 10 minutes. Explicit execution starts a separate maximum 600-second human-login window; individual protocol requests retain their 30-second bound. Admission policy and current material are rechecked at execution. The adapter independently verifies the durable running claim and reserves its single dispatch before callback binding, host registration, credential access, or provider I/O. Duplicate dispatches and recovered/interrupted claims cannot replay.

The trusted integration entry is `acceptance.NewProductionConnection` with an exact stored route, the default official-profile SIWC service, and a host-owned `CredentialReady func() bool`. Each construction creates a new random 128-bit generation. Construction itself never evaluates readiness. Invalid policies still reserve identifiable accounts against legacy HTTP bypass. Without operator opt-in the runtime constructs `acceptance.New`, recovers its ledger, and closes it before SIWC/storage shutdown. Neither startup path initiates OAuth by itself.

Optional material `production_admission` contains `generation` and the fixed profile `openai_siwc_subscription_v1`; empty metadata leaves historical simulation JSON/digests unchanged. Production operations use `source: production_connection` and `live_access: connection_only`, never `local_simulation`. These fields describe the connection approval, not catalog/inference authority or proof of real validation. A durable, immutable dispatch reservation extends the existing claim ledger. Completion compares the original running claim, generation and digest atomically; successful completion additionally requires that reservation and fresh matching authorization. Failed or interrupted work stays consumed.

## Protocol and evidence

The existing fixed protocol adapter is reused without endpoint customization. It requests `openid profile email offline_access resource.invoke chatgpt.tokens.use.direct` with resource `https://api.openai.com/v1`, uses fresh state/nonce/PKCE S256, and validates the issued client ID and signed identity before credential activation. Account and workspace selection remain the user's browser action. Requested permissions are not proof of granted permissions or available quota. See the [official registration guide](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), checked 2026-10-20.

Only bounded, sanitized connection evidence enters the existing ledger. OAuth URLs, codes, state, verifier, tokens, and provider response bodies do not. UI coordination exposes only the owned loopback handoff, never a raw authorization URL. A valid identity without plan permission does not authorize inference; no failure chooses another billing path. See [official errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery).

## Failure and recovery

Cancellation, login expiry, process shutdown, and receipt failures reuse the owned callback join and credential cleanup journal. Unknown outcomes remain consumed and require manual investigation and a new proposal; they never imply rollback of a remote grant. Recovery closes mutation admission before reconciling unfinished claims. A readiness check failing after approval denies dispatch; it does not retry or start a replacement login. Local cleanup failures fail closed and remain visible through existing recovery evidence.

## Delivery and acceptance

- Preserve historical simulation digests and existing simulation behavior.
- Exercise default denial, forged policy/material, stale account/route/generation, wrong phase, duplicate dispatch, readiness loss, cancellation, expiry, interrupted execution, receipt failure, and cleanup failure using only memory credentials and local protocol fixtures.
- Verify production-profile protocol dispatch offline, and keep every shipped runtime closed by default.
- Keep navigation/layout unchanged. Show a read-only installed-capability summary in the existing account check. Only an explicitly admitted host may offer the independent proposal/approval/execution sequence for a real connection. There is no web activation toggle.
- Run full tests, race checks, secret/vulnerability scans, a clean-source export, and browser acceptance before pushing reviewed public source. Check CI for that exact commit; do not create a Release.

The existing account check may include `openai_preparation.production_admission` with exactly `state: implemented_disabled`, `validation: local_simulation_only`, `native_keychain: not_checked`, and `operator_activation: explicit_startup`. This read-only diagnostic describes installed capability; it is not live eligibility, a native readiness report or permission to execute. Acceptance history separately exposes `production_connection_enabled` and optional current `production_admission`. Historical fields remain intact; production eligibility is never exposed as simulation eligibility.

## Next operator step (not performed)

Separately confirm real activation, including its fresh native test-item probe, the full requested scopes, local token retention and the user's own account/workspace selection. Follow the [operator runbook](openai-operator-entry.md), then independently approve one concrete connection proposal. Catalog and inference require later separate approval. This release has not performed real connection acceptance.
