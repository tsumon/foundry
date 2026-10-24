# Renewal and catalog prerequisite lineage

## Scope and invariants

Credential renewal maintains an existing subscription registration; it does not approve a catalog request, continue a task, spend a model-call budget, or start a sandbox. This change fixes only the connection prerequisite used when preparing a **new, independently approved** catalog request after a confirmed renewal.

The existing workbench, independent approvals, exact per-request snapshots, no model-call retry, no paid-API fallback, and no host execution fallback remain unchanged. Original task snapshots, connection receipts, catalog receipts, baseline evidence, and consumed budget are not rewritten.

## Cause

The connection result digest includes authorization generation and expiry. A legitimate refresh changes both, so comparing today's full snapshot directly against the original connection result incorrectly rejects a same-registration successor. Ignoring authorization fields would be unsafe: a different registration, incomplete rotation, or unknown refresh outcome must not inherit the connection.

## Evidence-based resolution

Retain the existing exact-match path. For a renewed generation, require an exact nonsecret origin authorization snapshot that reproduces the original connection result digest, plus a contiguous, confirmed, committed renewal chain to the current available generation. Account, route, credential version, immutable registration binding, scopes, issuer, subject, client, and subscription-only spending class remain exact.

Store origin metadata in an append-only side record, separate from historical receipts. Capture it transactionally when a connection succeeds; an older connection may also be anchored before its first future rotation only when the then-current snapshot exactly reproduces its result digest. The record must contain no tokens or credential-store references. A missing historical origin cannot be reconstructed by guessing an expiry or trusting the latest generation. Legacy records without an independently verifiable origin remain blocked, without automatic login or refresh.

The side table is `model_acceptance_connection_origins`, keyed by connection operation ID and guarded against updates and deletes. It stores the original authorization's binding, state, generation, and actual expiry; the authorization type excludes secret references from serialization. `ClaimSessionRenewal` may insert a missing origin before reserving rotation, using current account/route/credential metadata with the connection's original provenance. Access expiry does not prevent exact historical anchoring; it still prevents a new catalog request until the current authorization is usable. An already-rotated legacy connection without an origin remains unavailable even after further successful renewals.

Each accepted renewal edge must be same-account/same-registration, adjacent generations, succeeded/confirmed with no error, and backed by the committed refresh operation with no pending secret cleanup. Missing, failed, unknown, skipped, uncommitted, or pending-cleanup edges reject the prerequisite. Historical and current operation state must be consistent with credential retirement. Latest-connection and dispatch-claim checks remain mandatory.

Specifically, each retired authorization operation must be `revoked`, with cleanup complete and no outstanding writer. Every successor must have kind `refresh`; the final operation must be `available` and match the current authorization's exact secret reference internally (the reference is never copied to origin evidence). Renewal dispatches require matching account, adjacent `new_generation`, positive policy revision, valid ordered timestamps, `succeeded`, `confirmed`, and an empty error category. Pending dispatches or unresolved renewal outcomes block renewed lineage. The existing authorization journal does not record a separate renewal-revision field on each refresh operation; proof uses its immutable registration/generation identity and committed state together with the adjacent dispatch journal, not an invented historical field.

A new catalog material pins the **current** generation and expiry. Rotation after proposal or approval invalidates that material. Old catalog results do not become current; task continuation still requires a separately approved fresh catalog and independently approved continuation. Expired credentials stop the flow. Reading eligibility performs metadata-only operations.

## Verification and compatibility

Use synthetic store/account fixtures and loopback UI fixtures only. Cover direct connections, one and multiple confirmed rotations, legacy exact-match anchoring, unverifiable history, changed binding/account/route, missing or uncertain edges, cleanup failures, stale approvals, expired credentials, and restart persistence. Verify no provider/catalog/model/VM dispatch occurs during readiness or material preparation, and old records and budgets remain unchanged.

Public CI and package acceptance must retain their existing checks. The Windows installer workflow includes a real WHPX VM gate; when VM execution is not authorized, publication must stop before triggering that workflow rather than omit or weaken the gate.

The opt-in local browser fixture uses `FOUNDRY_TEST_SESSION_RENEWAL_CATALOG_BROWSER=1` with `TestSessionRenewalBrowserFixture`. It seeds an original task with a consumed unknown 2/7 budget, a successful connection/catalog, and a confirmed metadata-only rotation to generation 2. Prepare a new catalog and inspect its independent pending approval; do not execute it. No renewal ticker, real credentials, provider request, model call, or VM is used. Automated HTTP checks verify the original task, events, invocations, budget, connection, and old catalog remain unchanged.
