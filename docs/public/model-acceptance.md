# Model acceptance: preparation, not live access

This stage prepares OpenAI and Kimi acceptance while keeping real sign-in, account-route discovery and inference closed. A successful local simulation is not evidence of a real account, subscription, entitlement, quota or charge. The existing workbench, controller and separate context, MCP, plan and delivery approvals stay in place.

## Scope and implementation stages

| Stage | Work | Permission boundary |
| --- | --- | --- |
| A: preparation (this change) | Metadata-only acceptance inspection, failure tests, bilingual documentation and synthetic workbench screenshots | No real credentials, authentication or provider calls; no live-enabling switch |
| B: connection gate (local simulation) | Separately approved synthetic OpenAI authorization or generated Kimi fixture-Key setup | Account connection only; no inference permission implied |
| C: catalog gate (local simulation) | A separately approved bounded request to the owned fixture model directory | A visible model is not proof that inference works |
| D: one-call gate (local simulation) | One small coding request with fixed synthetic input per approval | Exact account, endpoint, model, input, deadline and attempt budget; no retry or fallback |
| E: task acceptance (future) | One disposable repository through the existing workflow | Frozen role/model snapshots and all existing approvals; no source writes or deployment |

Real-provider versions of B–D and all of E require further user confirmation and implementation review. This document is not authorization to perform them. Packaging, Windows support, external MCP execution and production deployment are outside this change. The executable simulation-gate design is in [Single-use acceptance gates](model-acceptance-gates.md).

## Provider choices and billing evidence

OpenAI's open-source/local-app Sign in with ChatGPT flow separates verified identity from permission to use a ChatGPT plan. The intended integration uses the system browser, PKCE/state/nonce and validated identity, and retains the issued client/account binding. It does not borrow another application's tokens. Actual eligibility remains unverified. See the [official sign-in contract](https://developers.openai.com/siwc/token-sharing-open-source/sign-in).

The first Kimi candidate is a user-created Kimi Code package Key, distinct from a Moonshot pay-as-you-go Key. Foundry must identify itself honestly. Direct third-party OAuth authorization has not been established for this project; neither copying an official CLI client ID nor reading its token cache is an approved shortcut. See [Kimi's third-party integration guidance](https://www.kimi.com/en/help/kimi-code/membership-guide).

The local route field `extra_usage: off` is a **user declaration**, not remote evidence. ChatGPT app settings can permit credits after plan limits; Kimi Extra Usage can consume a prepaid balance on the same package endpoint. Future subscription-only acceptance must separately verify the provider-side setting with the user and retain a dated, non-secret attestation. Unknown, missing or stale evidence cannot mean “off” or “free.” Sources: [OpenAI account/usage settings](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions), [Kimi membership and Extra Usage](https://www.kimi.com/en/help/kimi-code/benefits).

Provider rules were checked on 2026-10-19. Recheck before live acceptance. Do not hard-code quota-reset windows: accounts and plan generations differ. Report real usage only when supplied; token usage is not subscription balance. Otherwise show **unknown** and an official usage-management link. Kimi's package path is for personal interactive coding, not unattended batch service or resale; see its [usage guidelines](https://www.kimi.com/en/help/kimi-code/community-guidelines).

## Read-only acceptance inspection

The settings account detail exposes an explicit local inspection action. The server reads account, route and credential/authorization **metadata** in one database transaction. It does not read Keychain values, contact a provider, refresh a token, repair storage, create optional schema, change an account, write an event, reserve a call or consume a budget.

`GET /api/model-accounts/{id}/acceptance` is protected by the existing session, Host and Origin checks. It accepts no query parameters or mutations, and remains readable in diagnostic recovery mode. Unknown accounts return not-found; unreadable storage returns an error, never a healthy empty report. The response has non-null check/route arrays and only safe identifiers, finite state/reason codes and configuration metadata. No token, secret reference, callback or authorization URL is returned.

The report distinguishes:

- `scope: account_routes_only`: legacy accountless connections are not covered.
- `live_access: blocked`: this build does not grant live access, even when local metadata is complete.
- Account revision and local credential/authorization metadata: configuration only, not credential readability or remote validity.
- Login, catalog and inference phases: blocked; task phase: not evaluated and requiring its own approvals.
- Per-route local extra-usage declaration versus remote extra-usage state: always unverified here.
- Quota: unknown. No network lookup or inference is performed to improve the report.

The JSON envelope is `{ "acceptance": report }`. Its metadata-only contract is:

| Field | Values / meaning |
| --- | --- |
| `account_id`, `account_revision` | Identity and revision observed in the same read transaction |
| `scope`, `live_access`, `quota` | `account_routes_only`, `blocked`, `unknown` |
| `checks[]` | `phase`: `local_metadata`, `login`, `catalog`, `inference`, `task`; `state`: `ready`, `blocked`, `not_evaluated`; finite `reason_codes[]` |
| `routes[]` | Safe route `id`, known `adapter` or `unknown`, `environment`: `live`, `simulation`, `unknown` |
| Route metadata | `local_metadata`: `ready` or `blocked`; `local_extra_usage`: `off` or `unknown`; `remote_extra_usage`: always `unverified`; finite `reason_codes[]` |

Only local metadata can be `ready`. It requires an enabled, valid account with available current credential metadata and no pending/cleanup operation, or an available unexpired authorization with the required inference scopes and no account-wide pending/cleanup authorization operation, plus a valid route declaring local extra usage off. An older available authorization cannot hide an unfinished new sign-in, refresh or replacement cleanup. This does not check whether the secret can be read or whether the provider will accept it. Optional authorization storage is never created by this GET; a storage failure is an error, not permission to initialize or repair it.

The complete `reason_codes` vocabulary is:

| Code | Meaning |
| --- | --- |
| `live_provider_access_pending_confirmation` | Real provider access remains closed |
| `account_disabled` | The account is locally disabled |
| `route_missing` | No account route exists |
| `route_invalid` | Route metadata fails local validation |
| `credential_metadata_unavailable` | Current credential metadata is missing, unavailable, pending or requires cleanup |
| `authorization_unavailable` | No currently available authorization metadata, or an account-wide authorization operation is pending or requires cleanup |
| `authorization_expired` | Recorded authorization expiry has passed; no refresh is attempted |
| `inference_scope_missing` | Identity authorization does not grant direct inference |
| `extra_usage_local_setting_unknown` | Local extra-usage declaration is unknown |
| `remote_billing_unverified` | Provider-side billing/extra usage has not been verified |
| `task_approval_required` | This diagnostic does not evaluate or approve a task |

A report is a point-in-time diagnostic, **not an execution permit**. Account changes invalidate its displayed revision. There is no “approve live”, “test paid API”, background refresh, poller, retry or alternate-provider action. A failed read must clear a prior report rather than leaving an apparent current success.

### How to inspect local preparation

1. Open **Settings → Models** and select an account's **Manage** action. Do not enter a real credential in this alpha.
2. Request the **local acceptance check**. This reads local metadata only.
3. Review the phase blockers, local billing declaration and unverified remote state. Changing configuration does not unlock real access. Re-read after a change; do not treat an old report as authority.

If loading fails, keep live access closed and inspect the sanitized error. Recovery read-only mode permits diagnostics but does not grant mutation permission. A missing report or unavailable secret store is not a reason to switch to a legacy connection.

## Future execution-permit requirements

Before any real operation is implemented, the controller must bind approval to the operation kind, account revision, route/endpoint, selected model, approved data summary and digest, deadline, attempt limit and billing class. Only an explicit user action may authorize dispatch. Account login, catalog discovery, inference and task execution are separate stages; an approval for one never covers the next.

The future acceptance workflow must reject accountless profiles, including a mixed-role task containing an accountless selection. That restriction belongs to the new acceptance workflow; existing legacy behavior is not silently changed by this diagnostic. Existing production account-route and SIWC hard denials remain before secret access or outbound activity. Simulation uses only constructor-pinned, owned literal-loopback listeners; an arbitrary URL containing `127.0.0.1` is not proof of ownership.

Role recommendations require comparable coding samples and recorded plan/schema quality, patch correctness, validation results, latency and actual usage. No provider is declared universally best. A confirmed policy affects new tasks only. Model output, including a request to broaden permissions, remains untrusted controller input.

## Failure and recovery acceptance matrix

| Case | Required result |
| --- | --- |
| Wrong account/route, disabled account, missing/revoked/pending credential, expired or unresolved authorization | Report local blockers; no secret reads and no dispatch |
| Identity granted without inference scopes | Do not promote identity or directory metadata to inference authority |
| Local extra usage says off; remote state unknown | Remote state remains unverified; no subscription-only certification |
| Authentication, entitlement, quota or rate-limit failure | Preserve sanitized provider code/request ID; distinguish causes, no automatic refresh/retry/provider substitution |
| Timeout, interrupted response or process restart after possible dispatch | Outcome unknown; budget remains consumed, no replay or fabricated rollback |
| Secret-store or database failure | Fail closed; no plaintext fallback and no healthy-looking empty data |
| Local disable/revoke | Stop future use; distinguish local state from confirmed remote revocation |
| Repeated inspection, account switch, stale response or unmount | No state mutation, no polling and no stale cross-account result |

Faults are exercised using local fixtures, not by deliberately exhausting a real subscription. OpenAI subscription Responses currently require `store:false` and `stream:true`; `max_output_tokens` is unsupported on this route. A local response-size/deadline cap is not a hard provider billing cap. See [official protocol limits](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations) and [error handling](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery).

## Evidence and verification

Record the build/source revision, test scenario, account/route revision, phase, sanitized request ID when one exists, outcome certainty and actual usage when available. Never include tokens, authentication headers, authorization URLs, personal repositories or account screenshots containing private information in public evidence. Read-only inspection creates no model-call receipt; no request was sent.

The implementation sequence is: metadata/API tests and implementation; settings component tests and implementation; local protocol/denial and recovery regression tests; production build; browser checks at desktop and narrow widths; sanitized screenshots and bilingual documentation review. Run `./scripts/verify.sh` for the full existing automated suite. Document skipped Docker, Keychain, real-provider and independent-machine checks explicitly.

README images must come from the actual workbench backed by fresh disposable fixture data, not an invented UI. Every image is labelled as a synthetic local example with provenance in the accompanying media documentation. Simulated runner results must never be described as real Docker or real-provider acceptance.

Related: [Safety](safety.md) · [Architecture](architecture.md) · [Alpha status](alpha-status.md) · [Contributing](../../CONTRIBUTING.md).
