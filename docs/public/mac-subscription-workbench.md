# Desktop subscription account workbench (Mac and Windows)

Status: shared Mac/Windows wiring. Protocol acceptance uses local fixtures; native credential diagnostics use generated Foundry-only test items. Neither proves a real-account model loop. A newer package does not inherit earlier account or VM acceptance. This historical document URL is retained for existing links.

## Outcome and boundaries

The separate [session renewal](session-renewal.md) control now maintains a
registration only after explicit standing approval. Existing accounts remain
disabled. Prepare, read and mount are metadata-only; approve and disable do not
query a provider. The desktop-owned 30-second scheduler never retries model calls
or extends catalog, task, continuation or budget evidence. A fresh sign-in needs
new renewal consent. Acceptance here is synthetic/local; no real renewal was run.

Connect an OpenAI ChatGPT subscription inside the Mac or Windows Controlled Coding Workbench, independently query its model catalog, and deliberately select that account/model for future coding tasks. Keep the current navigation, account cards, approval materials, task model snapshots, isolated candidates and sandbox controls. Do not copy credentials or databases between the normal, simulation and coding workbench stores.

This is desktop wiring, not a new OAuth implementation. Reuse the existing SIWC connection, native diagnostic, durable single-use approvals, production catalog, account revision and authorization lineage checks. The ordinary desktop workbench and unsupported platforms do not acquire this capability. Application startup, opening settings, metadata reads and task configuration must have no credential-store or provider-network effects.

## Authority flow

1. In the desktop Controlled Coding Workbench, create/select local OpenAI SIWC subscription account metadata and its exact official route. Saving metadata does not sign in.
2. Explicitly prepare connection readiness. The UI explains the Foundry-only synthetic native-store write/read/delete check before this action. The host-owned controller uses Keychain on Mac and Credential Manager on Windows, retaining a private, exclusive journal; only confirmed cleanup grants the existing ten-minute, process-local readiness lease. Failure blocks the next step, without automatic retry or unrelated cleanup.
3. Generate, review and separately approve connection material, then execute once. Preserve the six requested scopes, resource, immutable route, admission generation, material digest and ten-minute manual-login window. Only then expose the current queryless loopback handoff. The user opens their normal browser and selects account/workspace and completes consent. Never automate the final consent.
4. Successful connection records a validated identity and credential lineage, not a catalog or inference grant. Independently prepare catalog mode, generate catalog material referencing that successful connection, approve it and execute one GET. Expired authorization stops; no refresh, retry or API fallback.
5. Read already-recorded catalog evidence locally. Explicitly choose an available model and account-bound role profile for a new task. Preserve the account, route, authorization generation/expiry and catalog provenance. Changing account, reconnecting or changing a profile cannot silently retarget an existing task. A catalog receipt does not authorize inference.
6. Existing repository import, sandbox readiness, baseline, context/model budget, plan execution, candidate tests and delivery approvals remain independent. Configuration and account preparation never start an agent, sandbox or test.

## Implementation shape

The process-local desktop account coordinator is supplied only by the trusted Mac/Windows coding composition root, behind the existing desktop capability and same-origin JSON protections. It serializes phase changes, refuses to replace active work, and delegates to the existing single-route acceptance services. Connection and catalog are distinct phase policies; inference acceptance is not installed. Do not accept transport URLs, secret-store implementations, readiness flags or admission generations from JSON, environment or persisted settings. Constructors used by tests may inject loopback services and in-memory secrets; the shipped application must not expose a fixture-to-production switch.

The coordinator must own service lifetime safely: no mutable service pointer races, no active callback orphaning, no silent re-execution after restart. Repeated preparation may not extend the same readiness lease. A new explicit preparation creates new authority, and old approval material remains unusable. Unknown or incomplete credential cleanup remains a blocker, never a success state.

Keep settings in the existing Models area. Show connection, catalog and task-selection readiness separately with plain-language blockers. The browser handoff must be validated against the current waiting operation by the trusted desktop process; a generic localhost external-link allowance is forbidden. Never expose OAuth codes, state, nonce, PKCE, access/refresh/ID tokens or native credential values to the renderer, logs, screenshots or public documents.

## Failure and recovery

Existing eligible blocked/paused coding tasks can use **Task overview → Task
authorization continuation** after a separately approved same-identity sign-in
and catalog operation. Explicitly read already-recorded local candidates, choose
one, prepare material, approve its exact digest, then separately apply it. These
controls do not prepare desktop access, query a provider, refresh credentials,
approve context, or retry model work. The local authorization ID belongs to that
sign-in; it is not a stable client ID. Original snapshots remain historical;
future consent shows the approved effective selection separately. Missing,
expired, invalid or stale material remains blocked, and consumed budget and
unknown usage are unchanged. See [task continuation](task-authorization-continuation.md).

- Native diagnostic failure: retain exact-item journal and stop; do not scan or remove other credentials.
- Login timeout/cancellation, token/identity failure: preserve sanitized stage evidence; consume the attempt; do not reuse the code, restore an old browser URL or begin another login.
- A credential may have committed before a final receipt error. Reconcile existing durable metadata; do not claim rollback or delete a valid credential merely because presentation failed.
- Catalog 401/403/429/5xx, redirects, malformed response or transport ambiguity: retain the one-call receipt, stop, and report certainty correctly. Never retry.
- Restart: metadata recovery and exact pending cleanup, with no provider retry,
  browser opening or catalog query. Process-local connection/catalog authority
  does not survive. Separately persisted renewal consent may resume at its next
  scheduled time; absent/disabled consent has no credential or network effects.
- Account/route/revision/generation mismatch or expired credential: disable task selection and fail closed before a credential read/provider send. Existing task snapshots are never rewritten.
- Sandbox unavailability still blocks tests: no host shell fallback. API billing routes remain distinct and are never substituted.

## Acceptance evidence

Both coding entries expose **Models → account → Desktop subscription preparation**.
Choose the saved official SIWC route, explicitly prepare connection (the card
explains the synthetic native storage diagnostic), then use the existing separate
proposal, approval and execute controls. Browser opening is manual and the native
desktop checks the exact current waiting session; arbitrary localhost links are
still blocked. After reading the successful connection, independently prepare
catalog, propose/approve/execute its one request, and explicitly read the local
catalog evidence. Select a model and save a new account-bound profile, then
assign that profile to roles for future tasks. No step auto-selects roles or
approves a task. Ordinary desktop and CLI do not install this coordinator.

The UI fixture is test-only and prominently synthetic: it exercises the actual
coordinator's preparation and approval layer with a fake consent/dispatch adapter
and real durable receipts. It is separate from the offline signed-protocol and
coordinator/catalog integration tests; it is **not** a live-production browser
end-to-end test. No native credential, provider, VM or Docker effects are part of
local protocol-fixture acceptance. Native CI separately checks Foundry-only
Credential Manager diagnostics and retains its independent Windows synthetic-role/WHPX gates.

Local fixtures must cover the real coordinator, existing approval service, catalog receipt and task-selection boundaries, not just mocked buttons. Include concurrent requests, lease expiration, rejected/stale materials, restart, cleanup uncertainty, catalog failure and cross-account selection. Label fixture evidence synthetic and prove zero real credential/provider effects. UI acceptance covers default closed state, explicit preparation wording, pending approval, stopped failure state, catalog selection and unchanged task approvals.

DMG/EXE packaging and installed-interface checks are separate from provider acceptance. New installers do not inherit earlier real-account or VM acceptance. Publish exact source revision, package digest and the tests actually run; real-model task acceptance remains pending. See [runtime retention and desktop parity](desktop-parity-upgrade.md).

Continuation controls are local-fixture tested only, not real-task accepted. The
test-only loopback harness serves the actual built workbench with temporary
SQLite and synthetic production-lifecycle catalog receipts. It includes ready,
pending, stale, applied, old-context-approval-denied and malformed-history tasks, each retaining one
unknown-outcome invocation of a seven-call budget. Its HTTP wrapper permits only
session creation and the actual continuation mutations; all login, provider,
desktop-preparation, model and VM mutations are closed. Browser screenshots and
real acceptance are separate gates, not implied by passing component/fixture tests.

Continuation candidates use the task-scoped, read-only
`GET /api/missions/{id}/model-continuations/candidates` projection (only grant ID,
account ID, state, generation and expiry). They do not consume the identity-bearing
general account-management list; the existing account settings contract is unchanged.
Malformed continuation history keeps valid original snapshot details readable but
blocks effective authority and all continuation/context/execution controls.

## Official protocol references

Checked 2026-10-24: [registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions). Foundry requires independent operation approvals; optional credential renewal has separate standing consent, not model retry authority. Model availability and remaining subscription quota are not inferred from local metadata.
