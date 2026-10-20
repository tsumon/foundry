# OpenAI subscription connection: operator entry

## Scope and authority

This increment wires the existing production connection controller into the normal workbench. Installation and normal startup remain closed. Development and acceptance use local fixtures only: no real sign-in, native Keychain access, model catalog, inference, refresh, retry, or paid-API fallback is performed as part of verification.

Accepting the requested scopes and local credential retention is not permission to sign in. A future operator must separately authorize real activation and the user must approve one concrete connection proposal. Account and workspace selection belongs to the user in the official browser flow. This change does not alter navigation, task approvals, scripts, Git writes, or Kimi.

## Explicit host activation

The normal executable accepts a paired, nonpersistent startup opt-in: `--openai-subscription-route=<existing-route-id>` and `--openai-keychain-journal=<new-absolute-private-journal-path>`. Neither environment variables, saved settings, HTTP fields, nor a previous JSON probe report can activate the connection. Supplying only one option, an invalid route, a diagnostic read-only instance, or an unsafe/reused journal fails closed.

After checking the exact stored route and enabled OpenAI subscription account, this opt-in runs the existing journaled Foundry-only synthetic Keychain probe in the same executable, using the native in-process storage implementation. It does not read other credentials. The journal must be new, outside the source checkout, and preserve the diagnostic's existing exclusive creation, file-mode, locking, and crash-cleanup protections. The separate diagnostic CLI remains usable for exact-item cleanup. There is no subprocess, shell execution, report-import shortcut, or fallback storage.

Only a successful probe with confirmed cleanup creates a process-local readiness lease, valid for 10 minutes. Failed probes or journal writes stop activation; the journal identifies any exact diagnostic item requiring cleanup. Readiness checks only examine the lease, never rerun the probe. Restarting requires new host activation, a new journal/probe, and a new approval generation. The UI does not claim that an earlier diagnostic from a different executable proves current readiness.

The startup opt-in does not bind an OAuth callback, open a browser, register an OAuth client, or contact OpenAI. It constructs `acceptance.NewProductionConnection` for exactly one immutable official live route. The normal default still constructs the disabled service. Approval and explicit execution remain separate operations.

## Approval contract and network boundary

Reuse the existing material digest, account revision, route snapshot, admission generation, 10-minute approval expiry, one-attempt durable claim, and 600-second human login deadline. Each protocol request has a 30-second bound. Scope and resource are fixed:

`openid profile email offline_access resource.invoke chatgpt.tokens.use.direct`

`resource=https://api.openai.com/v1`

These request subscription-use and refresh capabilities from OpenAI; this acceptance stage does not exercise those capabilities. A one-attempt Foundry approval does not make the remote grant single-use. Fixed official authorization/token/OIDC/JWKS endpoints, fresh state/nonce/PKCE, issued-client validation, and exact loopback callback policies are unchanged. OIDC discovery is identity verification, not model catalog access. See the [registration guide](https://developers.openai.com/siwc/token-sharing-open-source/sign-in).

Only an explicit execution after approval may start the owned callback and return a queryless `http://127.0.0.1:<port>/auth/start` handoff. The user opens it manually. No automatic browser launch, polling, retry, token refresh, catalog fetch, model call, task dispatch, or billing fallback is added.

## Workbench presentation

Keep Settings → Models and the existing acceptance cards. Extend the history response with `production_connection_enabled` and optional `production_admission` (generation and fixed profile). Simulation eligibility remains a different field and must never become true for production. For an owned production account, return only its exact route and admission metadata; mutation availability additionally requires recovery and fresh readiness. Missing readiness removes mutation authority without hiding prior receipts.

The frontend validates production provenance (`source: production_connection`, `live_access: connection_only`), live official route, exact scopes/resource/purpose/mode, and current generation independently from synthetic receipts. Production material may only describe the connection phase. Malformed or mixed source, missing metadata, stale generation, unsupported phases, and unsafe links disable actions. Old receipts remain read-only, never executable after a restart.

Labels explicitly distinguish real subscription connection from local simulation. The approval card discloses all six scopes, Keychain token retention, local identifier/audit retention, no model usage, and local cleanup versus remote revocation. Creating a proposal, approving, executing, and manually opening the browser remain separate actions. An enabled host never restores a browser link from durable history. Existing synthetic flows retain clear synthetic labels and local-only transport.

## Storage and failure recovery

Tokens remain in the Foundry native Keychain namespace; SQLite stores identifiers, granted scopes, version/expiry, secret references, and sanitized evidence, not tokens. Temporary state/nonce/verifier/code and raw OAuth URLs never enter receipts or logs. User passwords and OTPs are entered only on the provider login page.

Cancellation, expiry, shutdown and interrupted claims reuse callback joining and the exact-reference credential cleanup journal. Cleanup failure stays visible and blocks further mutation. A failed receipt can coexist with a committed credential; classify the outcome as unknown and reconcile it rather than asserting complete rollback. No failure repeats an OAuth code exchange. Local deletion does not revoke a remote grant; remote revocation is not implemented by this change, and the user can disconnect the app in ChatGPT settings. See [accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions).

## Acceptance and operator runbook

1. Run startup/configuration tests with injected in-memory diagnostic storage; assert zero native or provider calls by default and no activation through environment/HTTP flags or old journal imports.
2. Verify paired opt-in validation, exact route/account checks, failed/unconfirmed probe, fresh/stale readiness, recovery read-only denial, and startup/shutdown ownership.
3. Use the existing official-profile test transport to exercise production proposal → approval → execution → synthetic code exchange → receipt. Do not enable production egress in the acceptance preview.
4. Test mixed provenance, stale generations/material, duplicate execution, denied scope, cancellation, timeout, credential/receipt persistence failure, and unchanged downstream denials.
5. Run frontend tests and browser acceptance for disabled/default and synthetic flows; production UI tests use intercepted synthetic DTOs, never real provider traffic. Clearly label any intercepted scenario as UI simulation.
6. Run full Go tests/race/vet, frontend tests/build, clean-source export verification, secret scanning and dependency checks. Push reviewed public source only, inspect CI for the exact commit, and create no Release.

For a later real run, obtain separate user confirmation before using the paired startup opt-in (including its fresh native test-item probe). Then create and independently approve one concrete connection proposal. The user chooses their account/workspace. Stop after the connection receipt; actual quota, catalog and inference remain untested and require their own future authorization.
