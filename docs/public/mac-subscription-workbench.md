# Mac subscription account workbench

Status: design approved for implementation; local-fixture acceptance only. Real sign-in and model calls remain unaccepted until the user separately authorizes them.

## Outcome and boundaries

Connect an OpenAI ChatGPT subscription inside the Mac Controlled Coding Workbench, independently query its model catalog, and deliberately select that account/model for future coding tasks. Keep the current navigation, account cards, approval materials, task model snapshots, isolated candidates and sandbox controls. Do not copy credentials or databases between the normal, simulation and coding workbench stores.

This is desktop wiring, not a new OAuth implementation. Reuse the existing SIWC connection, native diagnostic, durable single-use approvals, production catalog, account revision and authorization lineage checks. Windows and the ordinary desktop workbench do not acquire this capability. Application startup, opening settings, metadata reads and task configuration must have no credential-store or provider-network effects.

## Authority flow

1. In the Mac Controlled Coding Workbench, create/select local OpenAI SIWC subscription account metadata and its exact official route. Saving metadata does not sign in.
2. Explicitly prepare connection readiness. The UI must explain the Foundry-only synthetic Keychain write/read/delete check before this action. A host-owned, Mac-only controller runs that check, retaining its private, exclusive journal; only confirmed cleanup grants the existing ten-minute, process-local readiness lease. Failure blocks the next step, without automatic retry or cleanup.
3. Generate, review and separately approve connection material, then execute once. Preserve the six requested scopes, resource, immutable route, admission generation, material digest and ten-minute manual-login window. Only then expose the current queryless loopback handoff. The user opens their normal browser and selects account/workspace and completes consent. Never automate the final consent.
4. Successful connection records a validated identity and credential lineage, not a catalog or inference grant. Independently prepare catalog mode, generate catalog material referencing that successful connection, approve it and execute one GET. Expired authorization stops; no refresh, retry or API fallback.
5. Read already-recorded catalog evidence locally. Explicitly choose an available model and account-bound role profile for a new task. Preserve the account, route, authorization generation/expiry and catalog provenance. Changing account, reconnecting or changing a profile cannot silently retarget an existing task. A catalog receipt does not authorize inference.
6. Existing repository import, sandbox readiness, baseline, context/model budget, plan execution, candidate tests and delivery approvals remain independent. Configuration and account preparation never start an agent, sandbox or test.

## Implementation shape

Add a process-local desktop account coordinator supplied only by the trusted Mac coding composition root, behind the existing desktop capability and same-origin JSON protections. It serializes phase changes, refuses to replace active work, and delegates to the existing single-route acceptance services. Connection and catalog are distinct phase policies; inference acceptance is not installed. Do not accept transport URLs, secret-store implementations, readiness flags or admission generations from JSON, environment or persisted settings. Constructors used by tests may inject loopback services and in-memory secrets; the shipped application must not expose a fixture-to-production switch.

The coordinator must own service lifetime safely: no mutable service pointer races, no active callback orphaning, no silent re-execution after restart. Repeated preparation may not extend the same readiness lease. A new explicit preparation creates new authority, and old approval material remains unusable. Unknown or incomplete credential cleanup remains a blocker, never a success state.

Keep settings in the existing Models area. Show connection, catalog and task-selection readiness separately with plain-language blockers. The browser handoff must be validated against the current waiting operation by the trusted desktop process; a generic localhost external-link allowance is forbidden. Never expose OAuth codes, state, nonce, PKCE, access/refresh/ID tokens or native credential values to the renderer, logs, screenshots or public documents.

## Failure and recovery

- Native diagnostic failure: retain exact-item journal and stop; do not scan or remove other credentials.
- Login timeout/cancellation, token/identity failure: preserve sanitized stage evidence; consume the attempt; do not reuse the code, restore an old browser URL or begin another login.
- A credential may have committed before a final receipt error. Reconcile existing durable metadata; do not claim rollback or delete a valid credential merely because presentation failed.
- Catalog 401/403/429/5xx, redirects, malformed response or transport ambiguity: retain the one-call receipt, stop, and report certainty correctly. Never retry.
- Restart: metadata recovery only; no credential probe, refresh, browser opening or catalog query. Previously granted process-local authority does not survive.
- Account/route/revision/generation mismatch or expired credential: disable task selection and fail closed before a credential read/provider send. Existing task snapshots are never rewritten.
- Sandbox unavailability still blocks tests: no host shell fallback. API billing routes remain distinct and are never substituted.

## Acceptance evidence

Local fixtures must cover the real coordinator, existing approval service, catalog receipt and task-selection boundaries, not just mocked buttons. Include concurrent requests, lease expiration, rejected/stale materials, restart, cleanup uncertainty, catalog failure and cross-account selection. Label fixture evidence synthetic and prove zero real credential/provider effects. UI acceptance covers default closed state, explicit preparation wording, pending approval, stopped failure state, catalog selection and unchanged task approvals.

DMG packaging and installed-interface checks are separate from provider acceptance. A new DMG does not inherit earlier real-account or VM acceptance. Publish exact source revision, package digest and the tests actually run; real sign-in and real-model task acceptance remain pending.

## Official protocol references

Checked 2026-10-24: [registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions). Foundry intentionally requires explicit new approvals instead of automatic refresh/retry. Model availability and remaining subscription quota are not inferred from local metadata.
