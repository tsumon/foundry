# OpenAI subscription connection adapter — implemented behind a closed gate

## Scope

Build the official open-source/local-app subscription **connection** adapter on the existing SIWC protocol and credential lifecycle. This is implementation preparation, not authorization to register a client or access an account. This release keeps the live entry closed, with no configuration, environment variable, browser request or command-line switch that opens it.

The workbench layout and independent proposal → decision → explicit execution boundary stay unchanged. The existing constructor-owned subscription fixture remains local-only and connection-only. Directory queries, inference, automatic refresh, automatic retries and paid API fallback remain closed. Scripts and Git writes remain closed; Kimi is unchanged. Existing laboratory fixtures are not promoted to production authority.

## Adapter architecture

Separate the connection protocol from its transport and admission policy, without replacing the accounts service, acceptance ledger or model router:

1. **Production profile:** fixed official authorization/token/OIDC origins and issuer, exact subscription resource and six scopes. No caller-supplied URL or generic HTTP proxy. Ordinary public starts still deny before account lookup, host registration, callback binding, credential access or network activity. The [production admission backend](openai-production-admission.md) provides a separate constructor-owned policy requiring host readiness plus the existing fresh approval, then independently reserves one durable dispatch before entering this core. Shipped startup supplies no enabled policy.
2. **Shared connection core:** validates account/revision and purpose, creates fresh state/nonce/PKCE, owns the loopback listener and exchanges a single code, verifies signed identity and commits through the existing durable credential lifecycle. The production profile must reach this core in offline transport tests; merely returning a disabled error is not an adapter implementation.
3. **Owned simulation profile:** every transport origin must be an exact literal loopback origin. It runs the same subscription connection core from the existing approved fixture. Official `resource` is a protocol audience, not a transport destination. Simulation credentials retain their local binding and cannot become live credentials.
4. **Read-only presentation:** the existing account preparation report may report an implemented-but-disabled adapter with local-simulation-only validation. It is not an execution permit, proof of provider eligibility, native Keychain health, subscription entitlement or available quota. Missing or malformed status must never enable an action.

No rewriting of historical approval material is needed. Existing optional scope/resource fields, material digests, frozen revisions, one-attempt claims and restart reconciliation retain their meaning. Production dispatch adds an immutable reservation table to the same ledger, not a second approval system. No new unapproved connection endpoint is introduced. The [approval handoff and human-login lifetime](openai-connection-handoff.md) increment prepares this wiring with local simulation; it does not enable shipped live admission. The laboratory's thirty-second automated deadline is not a human browser-login timeout.

## Official protocol profile

| Boundary | Fixed contract |
| --- | --- |
| Authorization | `https://auth.openai.com/api/accounts/authorize` |
| Code exchange | `https://auth.openai.com/api/accounts/oauth/token` |
| Discovery / issuer | `https://auth.openai.com/.well-known/openid-configuration` / `https://auth.openai.com` |
| Resource | `https://api.openai.com/v1`, in authorization and code exchange |
| Scopes | `openid profile email offline_access resource.invoke chatgpt.tokens.use.direct` |
| Initial registration | `dynamic_agent_client`, `agent_name_hint=FOUNDRY`, stable per-host `ext_agent_host_id` |
| Issued client | Valid `oaiapp_...` from the callback; use it for code exchange and subsequent authorization, never persist `dynamic_agent_client` as an issued ID |
| Callback | Exact `http://127.0.0.1:<port>/auth/callback`; no `localhost`; same URI in both protocol steps |
| Public-client protection | Fresh state and nonce, PKCE S256; no client secret |

Pin discovery issuer and JWKS origin; disallow proxy use and redirect following. Bound response size and time. Reject duplicate/invalid callback parameters, wrong client, issuer, audience, nonce, signature, expired proof, expanded/mismatched scopes and account changes before credential commit. Never place tokens, OAuth codes, private authorization URLs, state/nonce/PKCE or Keychain references in logs, browser storage or acceptance receipts.

Requesting `offline_access` does not enable refresh. Granted scopes must be recorded independently from requested scopes; an identity-only grant never enables subscription use. A connection does not verify remaining quota, remote extra-usage billing or permission for a later operation. Those remain `unknown`, `unverified` and independently blocked respectively.

## Storage, failure and recovery

Reuse the existing `model.Secrets` boundary, native non-interactive macOS Keychain implementation and write-ahead authorization journal. No plaintext fallback, imported CLI credentials or account discovery is introduced. Tests substitute only the external credential store and transport with synthetic data; they do not access a real account or claim that the current machine's Keychain has been certified.

- Denial, malformed callback or invalid proof cannot produce usable authorization.
- Code exchange is one attempt. A timeout, lost response, 403 eligibility rejection, 429 or 5xx never triggers retry, refresh, another OAuth cycle or paid fallback.
- Cancellation/expiry closes owned sockets and joins accepted callbacks before completion; a late callback cannot commit after cleanup.
- Partial credential writes leave durable cleanup intent; recovery cleans exact references or remains unavailable. Restart does not replay code exchange. Unknown completion remains consumed/unknown, not success.
- Local cleanup never claims remote revocation. A new attempt requires fresh proposal, approval and explicit execution in a future authorized live workflow.

## Implementation and acceptance gates

1. Document the profile, closed release boundary and test matrix before implementation.
2. Implement the shared adapter/profile and read-only preparation metadata. Cover production denial before effects and official-profile protocol construction with offline transport, plus real loopback exchanges through the approved fixture.
3. Keep the existing account-detail UI. Display “implemented, default closed; local simulation only,” with login, directory, inference and refresh still blocked. Do not add a live login button, toggle or new navigation.
4. Verify permission/replay/revision boundaries, signed proof, reduced grants, credential failures, interrupted writes, cancellation and restart. Existing approval and downstream denial suites must pass unchanged in meaning.
5. Run full tests, race detector, vet, builds, desktop/narrow browser acceptance, clean-source export and secret/dependency scanning. Publish only reviewed public source; no private plans, test data, credentials or internal evidence. Inspect CI for the exact pushed commit; do not create a Release.

Real registration/login is explicitly **not accepted** by these tests. Before live use, the user must separately confirm subscription-scope consent, account/workspace selection, storage readiness and one-attempt approval. Catalog, inference, refresh, retry and paid API switching are not included in that future connection approval.

## References

Protocol checked 2026-10-20 against the official [open-source/local-app sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [profiles and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions), and [errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery). This preview protocol can change. Application authority remains narrower than protocol capability.
