# Single-use model acceptance gates

## Scope

OpenAI connection, Kimi Code connection, model-directory discovery and one inference are exercised **only against process-owned local fixtures**. Normal Foundry keeps real authorization, account-route discovery and inference blocked. There is no live-enable flag, environment override, credential import, automatic retry, token refresh, paid fallback, script execution or Git write in this feature. Existing context, MCP, plan, delivery and deployment/database approval boundaries do not change.

The existing metadata-only `GET .../acceptance` remains read-only, including in recovery mode. The new operations are separate, account-scoped records; they neither approve tasks nor change role/model policies or frozen task snapshots.

OpenAI subscription acceptance starts with its own subscription connection; it does not require an earlier standalone identity-only operation or an identity-client inquiry. The optional identity-only fixture remains isolated and its receipts cannot satisfy subscription catalog/inference prerequisites. See [the subscription connection direction](openai-stage0.md#current-subscription-connection-direction). This direction changes neither the implemented simulation-only boundary nor the separate approval and explicit-execution requirements.

## Three separate permissions

| Operation | Reviewed material | Allowed effect after approval |
| --- | --- | --- |
| `connection` | Exact provider, account/revision, route, owned loopback endpoint, auth method, billing class, fixture-only data summary, expiry | One synthetic connection attempt. OpenAI uses the existing local SIWC protocol flow; Kimi installs only a generated fixture package Key. No directory or inference request. |
| `catalog` | Exact connected account/revision, credential generation or authorization binding, route/endpoint, connection evidence, expiry | One bounded model-directory HTTP request through the existing router. No inference. |
| `inference` | All above, successful same-snapshot catalog evidence, exact catalog model, fixed synthetic coding prompt and digest, deadline and one-attempt budget | One bounded router request with no tools or repository context. No task execution. |

Connection uses freshly created dedicated synthetic accounts in the acceptance harness. No user-entered or existing private credentials are accepted. The harness explicitly registers its allowed account/route pairs with the gate service. Merely supplying a loopback URL does not enable it. Production constructors have no simulation executor or eligible accounts.

The OpenAI fixture drives the existing SIWC implementation against its owned authorization, token and JWKS listeners. Its consent is synthetic, not a browser login to OpenAI. Kimi setup proves local fixture-key binding only; catalog/inference remain separate checks. Neither proves a real identity, subscription, quota or billing setting.

## Immutable materials and state

An operation stores an opaque ID, monotonic revision, phase, account revision, route snapshot, credential/authorization snapshot where applicable, prerequisite evidence IDs, material digest, fixed data summary, model when applicable, creation/expiry timestamps, deadline duration and `max_attempts:1`. Server-owned fields cannot be supplied or overridden by the browser. Default approval expiry is ten minutes; execution deadline is thirty seconds. A local deadline is not a real-provider billing cap.

The material digest is a SHA-256 of a canonical typed structure, not a browser-computed string. Review and execution both compare the exact digest and operation revision. An account or credential change, route mismatch, unresolved credential/authorization operation, disabled account, expired authorization, unknown local extra usage, missing prerequisite or non-catalog model fails closed. Only the OpenAI subscription and Kimi interactive-package adapters are eligible; explicit paid API routes and accountless profiles are excluded.

State transitions are:

```text
pending → approved → running → succeeded | failed | unknown
       ↘ rejected
pending / approved → expired | invalidated
```

Approval does not dispatch. The user explicitly executes the approved operation. Before any credential read or outbound action, a database transaction revalidates the frozen material and atomically consumes the single attempt (`approved → running`, attempts 0 → 1). Concurrent execution, stale tabs, repeated clicks and HTTP retries cannot dispatch it twice. Failures and unknown outcomes require a new proposal and new approval; there is no retry endpoint.

At dispatch, that transaction assigns a durable monotonic `claim_sequence`; connection lineage follows this sequence, never proposal order or timestamps.

Connection may legitimately create a new credential/authorization revision. Its evidence records the resulting revision; later catalog proposals bind that result, not the pre-connection revision. Catalog evidence must match the current connection and account snapshot. Inference selects a model from that exact catalog, never a hard-coded provider recommendation. Reconnecting invalidates prior downstream authority.

## Recovery and evidence

Starting the acceptance service explicitly reconciles durable `running` operations to `unknown`, retaining `attempts:1`. It does not replay, refresh, refund or claim to undo a remote effect. Pending/approved operations remain inert and must revalidate when acted upon. Recovery/read-only HTTP mode rejects all new business mutations. Startup recovery must complete before simulation execution is enabled; storage failures disable execution.

Known authentication, entitlement, quota and rate-limit errors are finite sanitized codes. Timeout, cancellation, interrupted streaming or a lost completion write after possible dispatch are not successful rollback: retain an unknown/consumed record. A completion-persistence failure is surfaced as a failure to record, not a success response. No raw provider error or response body is stored. Evidence contains phase, timestamps, source (`local_simulation`), digest, attempts, outcome certainty, safe request/model identifiers and actual token usage only when supplied. Catalog entries are bounded and validated. Raw credentials, tokens, references, authorization URLs and prompt/output text are excluded from receipts. Quota remains `unknown`, remote billing remains `unverified`, and live access remains `blocked` on every receipt.

## HTTP and UI

All endpoints inherit launch-session authentication, exact Host, same-origin JSON mutation checks, strict request decoding and recovery-mode denial.

- `GET /api/model-accounts/{id}/acceptance-operations`: safe operation history and simulation eligibility; reads never dispatch or reconcile state.
- `POST` to that collection: propose `{phase, route_id, expected_account_revision, model?, prerequisite_id?}`. The server resolves and freezes everything else. It returns a pending operation.
- `GET .../acceptance-operations/{operation_id}`: fetch a saved receipt without network activity.
- `POST .../{operation_id}/decision`: `{expected_revision, material_digest, decision:"approve"|"reject"}`.
- `POST .../{operation_id}/execute`: `{expected_revision, material_digest}`. Consumes and dispatches once.

Unknown fields, duplicates, wrong-account operation IDs, unsupported phases/routes, query strings and unsafe identifiers are rejected. Production mutation attempts fail before secret access or network use, including when a caller fabricates a simulation route. The gate runtime cannot be switched through these endpoints.

The card lives in **Settings → Models → account detail**, alongside existing read-only preparation. It shows a compact ordered three-phase flow, a reviewable material summary, separate Approve/Reject/Execute controls and saved receipts. All successful states say **local simulation**, not “real account available”. No new top-level navigation or charts. Switching account/revision clears stale materials; failed reads clear prior healthy-looking results. No background execution, retry or polling. When a mutation response is lost, the UI offers an explicit read of the existing record, not another execution.

Existing synthetic protocol laboratory scenarios remain test fixtures, not approval evidence. They cannot create acceptance receipts. Acceptance accounts cannot use the older direct authorization/probe shortcuts to bypass the new gate; other existing fixture scenarios retain their established tests.

## Verification gates

1. Store tests prove revision/digest binding, separate permissions, prerequisite lineage, single claim under races, expiry, restart recovery and failed persistence behavior.
2. Service tests prove production denial before secrets/network, exact fixture eligibility, disabled/stale/pending state denial, no cross-account use, one attempt, no retry/fallback, finite errors and no secret reflection.
3. HTTP tests cover authentication, Host/Origin, duplicate/unknown JSON fields, read-only recovery, ID/query validation and safe receipts.
4. Owned fixture tests run both providers through connection → independently approved catalog → independently approved inference; inspect hit counts to show no implicit next-stage call. Exercise quota, entitlement, rate limit, transport/stream interruption and cleanup/restart. Existing SIWC identity/PKCE and router tests remain mandatory.
5. UI tests cover review before execute, denial/unknown states, stale selection and lost responses. Browser acceptance checks actual desktop/narrow layouts and the three-phase workflow against disposable fixture data.
6. Run the full existing verifier, public allowlist/export check and secret scan. No real account, Keychain integration, Docker certification, GitHub push or Release is part of this acceptance.

See [Model acceptance](model-acceptance.md) for official protocol sources and the separate future real-account confirmation checklist.
