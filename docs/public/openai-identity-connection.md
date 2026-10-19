# OpenAI identity-only connection

This stage implements and verifies **local simulation only**. Real login still requires a separate user decision and a reviewed live implementation. It does not authorize model discovery, inference, automatic refresh, paid API fallback, scripts or Git writes. Kimi is unchanged. The existing workbench and separate proposal → approval → explicit execution flow remain in place.

## Design and permissions

Reuse SIWC, protected credential storage, immutable acceptance materials, durable one-attempt claims and recovery records. Add an explicit `identity_only` connection purpose. The constructor-owned local identity route, approval material and SIWC attempt must agree on that purpose; callers cannot omit it to obtain the older broader simulation grant. Legacy local protocol fixtures remain available for regression tests, not as live authorization.

Acceptance eligibility is distinct from the older non-gate local SIWC simulation capability. The account card enables those legacy local controls only after its history read carries an explicit affirmative capability derived by the backend from the shared SIWC account guard, recovery state, and owned-account policy; absent, malformed, false, or identity/acceptance-owned policy stays closed. This capability never enables production login, refresh, discovery, inference, or paid fallback.

Identity authorization requests exactly `openid profile email`, PKCE S256, state and nonce. They do not request `offline_access`, `resource.invoke`, `chatgpt.tokens.use.direct` or a model resource. No account, email or workspace selection hint is supplied. When real login is separately authorized, the user will choose the account and workspace themselves. The app must use its own registration, never another application's client identity or credential cache.

The callback remains literal `http://127.0.0.1:<port>/auth/callback`, opened before the handoff. Validate state, one-use code exchange, issued client ID, issuer, signature, audience, nonce and signed expiry with the existing bounded transport/JWKS validation. The production entry remains blocked before network or secret access. Local endpoints are exact constructor-pinned loopback origins, with redirects/proxies disabled.

An identity response can contain only `id_token`: access token, refresh token, token type, `expires_in` and scope fields are not required. Signed ID-token expiry is authoritative. A bounded optional access token is discarded, never made available to models. A returned refresh token or scope outside the requested identity set fails closed before credential persistence. If no explicit scope is returned, only `openid` is recorded as verified identity evidence; requested scopes remain separately visible in the approval material. Profile/email request scopes do not prove a selected workspace, subscription entitlement, quota or API permission.

Store the ID token only through the existing protected secret lifecycle, with an `identity_only` binding and no access or refresh token. Retaining this proof supports the existing durable authorization-generation/cleanup and bound reauthentication lifecycle; it is never sent to a model. SQLite, HTTP responses, activity, logs and public materials contain only non-secret bounded metadata. Identity records cannot lease model credentials or refresh credentials. Existing broader bindings still require their original access-token validation; accepting an ID-only response must not weaken them. Do not silently convert an existing broader authorization into an identity receipt.

## Approval and evidence

The identity purpose and fixed requested scopes are included in the immutable material digest alongside account revision, route, credential/authorization snapshot, expiry, deadline and maximum attempts. Approval and execution are separate user actions. A changed revision, route, purpose, scope or digest invalidates the material. Every execution is claimed durably once before the first side effect. Approval alone sends nothing.

Connection success records identity verification only. It cannot satisfy a catalog or inference prerequisite, even if another authorization later appears. Catalog, inference and refresh are unavailable for the identity-owned route at the service boundary, not merely hidden in the UI. Current task model snapshots and role policies are not changed. Remaining quota stays `unknown`; remote billing stays `unverified`; live access stays `blocked`.

The account's existing acceptance card shows the purpose, requested scopes, separate approve/execute controls and a finite result. It keeps catalog and inference visibly closed and explains that successful local simulation is not real-account acceptance. The existing authorization card distinguishes identity-only from model-use permission and does not offer refresh for identity-only records. No new navigation or charts are required.

## Failure and recovery

- User denial, invalid state/nonce/audience/issuer/expiry, broadened grants, storage denial and malformed responses do not create a usable identity authorization.
- Partial secret writes use the existing exact-reference cleanup journal. No plaintext fallback or credential-store enumeration is introduced.
- Timeout, interrupted execution and lost completion writes remain consumed/unknown; restart recovery does not replay the provider request. The owned callback is canceled and joined before failure completion so it cannot write a late success.
- A failed or unknown attempt is not automatically retried, refreshed or replaced. A new attempt needs new material, a new approval and explicit execution.
- Once unresolved cleanup is cleared, expired or revoked identity metadata may be snapshotted for a fresh independently approved connection. It never counts as current proof: success requires a new valid, unexpired identity proof.
- Errors and receipts use bounded codes and summaries, never provider bodies, tokens, callback codes or auth URLs. Tests must verify secret-read/write and endpoint counters for denied paths.

## Implementation and acceptance sequence

1. Add identity-only SIWC/token persistence semantics and protocol/storage regression tests.
2. Bind the purpose to the existing acceptance gates and add a dedicated local identity account/route to the disposable fixture. Verify approval, one-shot execution, downstream denial and recovery through real local protocol calls.
3. Extend existing account cards and their tests; perform desktop and narrow-width browser acceptance on a fresh fixture.
4. Run clean-source export, all Go/race/vet tests, frontend tests/build, dependency and secret scans. After acceptance, push reviewed public source only and check GitHub CI. No Release, private history, internal evidence or real credentials are published.

Exit criteria include successful ID-token-only simulation, rejected broader grants and invalid identity proofs, stale/duplicate/concurrent execution rejection, recovery without replay, and zero catalog/inference/refresh calls on the identity path. Local success must not be described as real OpenAI login, entitlement or workspace verification.

## Official references and remaining live gate

Checked 2026-10-19: [identity-only sign-in](https://developers.openai.com/siwc/website), [open-source registration and SIWC flow](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [SIWC quickstart](https://developers.openai.com/siwc/quickstart). Identity-only response shape and the open-source dynamic registration flow are tested locally together; actual availability for Foundry must still be verified at the future live-connection gate. Do not substitute a partner website client or a borrowed Codex client to bypass eligibility.

Before any future real login, separately confirm the app registration/eligibility, identity-only scopes, callback and storage readiness, then obtain explicit permission for connection alone. The user selects the account/workspace on the provider page. Catalog and inference each remain separate later decisions. See [stage 0](openai-stage0.md) and [acceptance gates](model-acceptance-gates.md).
