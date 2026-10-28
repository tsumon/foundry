# Stay signed in: controlled subscription renewal

Status: implementation and local-simulation acceptance, 2026-10-24. Real refresh requires a separate, explicit user approval. Installing an update does not grant that approval.

## Outcome and boundaries

Foundry should retain an OpenAI subscription registration across restarts and renew that registration's short-lived access credentials without reopening a browser on every expiry. Credential maintenance is not permission to run an agent. Connection, catalog, context/budget, plan, repository changes, sandbox execution and delivery retain their existing independent approvals.

Existing accounts start with renewal disabled. Existing tasks, immutable model snapshots, authorization-continuation records, baseline evidence, invocation receipts and consumed budgets are not migrated or rewritten by this feature. Renewal never replays a failed model call, queries a catalog, starts a VM, switches accounts or falls back to paid API access.

## Official protocol basis

Checked against OpenAI's [accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions), [token reference](https://developers.openai.com/siwc/token-sharing-open-source/token-reference), and [errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery) on 2026-10-24. Refresh uses the registration's issued client ID, refresh token and original resource; it does not request additional scopes. Replacement credentials must be stored together and refreshes serialized per session. Credential values never enter HTTP DTOs, SQLite, browser storage, diagnostics or public artifacts.

## Independent standing approval

Use the existing account settings layout. The user first prepares renewal material bound to the current account revision, authorization ID and immutable binding (issuer, subject, issued client ID, scopes and subscription-only spend class). The material describes the exact official token endpoint, original resource, system credential store, schedule, failure behavior and what it does **not** authorize. It expires after ten minutes.

A separate explicit approval enables standing credential renewal for that registration. Preparing or reading material has no secret-store or network effects. Approval is optimistic-concurrency checked and cannot silently adopt a different account/grant or stale material. A later fresh sign-in changes the authorization ID and requires fresh renewal consent; ordinary successful rotation keeps the same authorization ID. Disabling renewal stops future dispatches, does not sign the user out, and does not erase evidence. Local in-flight work is canceled/joined before disable returns. Another process's already-sent request may continue until its bounded 30-second deadline; status honestly retains in-progress/recovery metadata, withdrawn consent fences commit, and new approval is blocked until the original claim settles. An already sent request can have an unknown outcome and must not be replayed.

The shipped desktop composition supports this capability on Mac and Windows, but no existing account is opted in. This implementation/acceptance run must not approve renewal for a real account. Generic HTTP clients, recovery-read-only mode and CLI composition cannot enable the desktop-only path. Existing desktop capability, session and origin checks remain mandatory.

## Renewal lifecycle

1. Metadata-only status distinguishes registration retained, access credential usable/expired, renewal disabled/enabled, renewal in progress, recovery required and login required. Expired access is not automatically labeled a lost registration.
2. A desktop-owned coordinator checks enabled policies every 30 seconds while the app is running. It does not wake a closed app. It schedules at access expiry minus five minutes, respecting any provider `earliest_refresh_at`. Opening settings and reading status never trigger a refresh.
3. Before reading the exact stored credential or sending anything, revalidate policy revision, unchanged account/binding, enabled subscription-only account, official transport/resource, offline access and absence of unresolved refresh/cleanup. Expired access alone does not prohibit refreshing a valid renewable session.
4. Persist a single-owner dispatch claim. Reuse the existing authorization-generation journal and secret-store write/commit/cleanup lifecycle. Concurrent processes and repeated ticks cannot dispatch the same generation twice. Requests have bounded deadlines, no redirects and no transport retries.
5. Validate the replacement ID token and unchanged identity, client, resource and granted scope set. Commit the replacement bundle to Mac Keychain or Windows Credential Manager before advancing the public generation pointer. Delete only the superseded exact secret reference after the commit. Task snapshots do not contain refresh tokens and remain unchanged.
6. Record only allowlisted stage, safe error category, certainty, old/new generation and timestamps. Successful rotation permits a later scheduled rotation, not a retry of an earlier request. A scheduler tick does not consume model-call budget.

## Failure and recovery

### Native credential wait and network deadline (2026-10-27)

Local credential-store reads can wait for an operating-system prompt. They must
not consume the 30-second network/exchange deadline. The desktop's cancelable
lifetime still owns the operation; cancellation/withdrawal is checked again
after credential access and before the single token exchange. The exchange gets
a fresh bounded deadline only once the two existing credential checks finish.
No background goroutine is allowed to send later after a canceled wait.

A failure proven to occur before any credential-rotation intent is a local
`credential_preflight` failure with certainty `not_sent`. Preserve the current
grant/generation rather than labeling it as an uncertain server rotation. The
durable receipt still blocks subsequent ticks and restarts: this distinction is
not an automatic retry or a new recovery authorization. After an intent, or if
dispatch certainty is unknown, existing conservative recovery rules remain.
UI copy distinguishes local unsent failures from actual invalid-grant/login
requirements without promising that expired access tokens are usable.

Existing `refresh_unknown` credentials and historical receipts are not repaired
or reclassified on upgrade. In particular, lack of a success receipt is never
permission to replay an old refresh token. Task snapshots, baselines, evidence,
and consumed budgets are untouched. This change is validated with isolated
synthetic credentials/loopback OAuth, not with real subscriptions.

- Missing or revoked consent, changed account/grant/route, missing offline access, malformed scheduling metadata, stale material or recovery-read-only mode: no secret/network work; renewal blocked.
- Provider says too early: wait for the known permitted time; do not loop requests.
- Any sent request whose outcome is unknown: stop automatic renewal for this session, retain previous protected credential and immutable journal, deny use of uncertain credentials, and require explicit recovery/reconnection. Never replay the old rotating token after restart.
- Terminal invalid/reused/expired refresh token: login required, local invalidation/precise cleanup recorded; no automatic browser launch.
- Replacement validation, secret write, metadata commit or exact cleanup failure: fail closed, retain recovery evidence; startup recovery never retries network work. Distinguish a committed replacement with pending cleanup from an uncommitted one.
- Withdrawal races with dispatch/commit must not resurrect an enabled policy. The operation journal is authoritative after a crash; a missing success receipt is not permission to resend.

The coding desktop performs metadata-only authorization/renewal reconciliation
under its exclusive startup data lock before starting the scheduler. A crash
before an intent or during rotation becomes recovery-required/unknown; a committed
replacement records its confirmed new generation even if receipt completion or
exact cleanup was interrupted. Pending cleanup remains a blocker and is not
performed by this startup path. No startup credential Get/Set/Delete or provider
request is needed. A failed explicit status read also closes UI mutations until
the user successfully rereads metadata; old displayed status is not actionable.

No change to automatic paid-API switching, model retry, VM fallback or task continuation approvals is included.

## Acceptance

Use isolated temporary stores, in-memory fake credentials and literal-loopback OAuth fixtures only. Cover disabled defaults, stale and cross-account approval, identity/scope/resource changes, earliest time, duplicate/concurrent dispatch, normal rotation, expiry, cancellation/withdrawal, unknown response, invalid token, secret write/commit/cleanup faults and restart. Assert requests never contain a scope expansion and no model/catalog/VM endpoint is reached. HTTP tests cover session, origin, capability, CSRF and read-only boundaries. UI tests cover material review, enable/disable and honest failure states without automatic POST/retry.

Verify old task bytes, baseline/source hashes, invocation receipts and the consumed call budget before and after installed Mac startup. Run full Go/race/vet, web, desktop, security and packaging gates; Windows native installer acceptance remains mandatory. Public documents must distinguish locally simulated refresh validation from a real refresh that has not been performed.
