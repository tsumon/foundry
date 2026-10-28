# OpenAI subscription: independently approved single inference

## Scope and authority

This increment adds a default-closed acceptance controller, not general live model access. Keep Settings → Models, the current workbench and separate connection/catalog/task approvals. Only a later explicit operator activation and per-operation approval/execution can send a real request. Development, automated tests and browser acceptance use synthetic local fixtures only; never inspect native Keychain or reuse real accounts during verification.

Use a constructor-owned inference controller alongside, not through, the general model router. The latter continues rejecting all live account traffic. A nonpersistent `--openai-inference-route=<existing-route-id>` selects one exact official subscription route and is mutually exclusive with connection/catalog/native-probe options. Startup, status, material creation and approval inspect metadata only; they do not probe, read secrets, log in, discover models or refresh tokens. Infer-only startup skips native credential recovery just as catalog startup does; outstanding writes/cleanup block readiness.

Provenance is `production_inference`, live access is `inference_once`, and the new process admission profile is `openai_siwc_inference_v1`. No UI/HTTP setting, synthetic receipt, connection permission or directory approval substitutes for current process admission. Existing terminal connection/catalog receipts remain readable across modes.

## Fixed request and permission

The sole permitted request is `POST https://api.openai.com/v1/responses`, using the exact selected Foundry subscription access token. No additional OAuth scopes, token refresh, account switch or paid API fallback is permitted. Fixed body:

```json
{"model":"gpt-6.1-sol","input":[{"role":"user","content":"Reply exactly: FOUNDRY_INFERENCE_OK"}],"store":false,"stream":true}
```

Do not add instructions, tools, reasoning/service-tier overrides, history, source files, task context, conversation IDs or unsupported fields. In particular, omit `max_output_tokens`; an internal value such as 128 is not a provider token cap. The expected text is `FOUNDRY_INFERENCE_OK`; comparison may trim surrounding whitespace, but content is never executed. This is a connectivity test, not a coding-quality evaluation. `store:false` is an API option, not a promise that OpenAI retains no operational data.

The material freezes enabled account/revision, subscription-only route with `extra_usage:off`, authorization binding/generation/expiry, latest claimed successful connection, successful same-snapshot real catalog receipt and digest, fixed catalog-listed model, prompt and digest, exact method/URL, admission generation, 30-second deadline, 1 MiB total response-byte limit and `max_attempts:1`. A changed account, route, authorization, connection/catalog lineage, missing model, unresolved write or cleanup invalidates authority. No implicit catalog refresh occurs. Material expiry is the earlier of creation plus ten minutes and authorization expiry.

## Single-use lifecycle

The 30-second deadline is the network window, starting at an immutable send
checkpoint after credential access. Waiting for native storage remains subject
to material expiry and cancellation; a late OS result cannot authorize sending.
Successful finish requires that checkpoint as well as the unchanged completed
response, exact authority and budget checks. Local access errors are distinct
from provider authentication errors and never trigger automatic login or retry.

Reuse the existing propose → pending → separately approve → approved → explicitly execute → running → succeeded/failed/unknown ledger. Creation and approval perform no secret read or network request. Execution transactionally revalidates the complete material, consumes one attempt and creates a unique immutable dispatch reservation before reading the one credential. Revalidate after leasing and immediately before send; generation, deadline and snapshot must still match. Duplicate clicks, parallel requests, lost UI responses and process restarts cannot replay the operation. Fresh connections disable environment proxies, redirects, HTTP/2/reused-connection retries and application retries.

Successful inference requires an actual `response.completed` event with a completed response, the requested model, bounded well-formed text and no executable/tool output. Deltas alone are never success. Report transport completion and expected-output match separately: a completed response with different text fails the content check without another request. Persist only safe receipt fields: request/model identifiers, timestamps, terminal event, finite error code/certainty, material/result digests, attempts/claim, output match and output digest, and actual usage when supplied. No raw response, token, secret reference, headers, OAuth URLs, codes or arbitrary output text is retained in the receipt.

Success does not enable task inference, roles, MCP, Skills, scripts, Git writes or another acceptance operation. Never relabel local fixtures as real acceptance.

## Limits, errors and recovery

Quota and remote billing remain unknown/unverified; actual returned token usage is not remaining quota or a price. Local subscription-only/extra-usage flags do not prove provider-side settings. Before future real execution the user must review those settings and separately accept possible plan consumption without a precise token or monetary cap. A 30-second local timeout/1 MiB receive bound cannot guarantee remote cancellation or refund.

- Expiry, stale material, disabled account, missing permission or Keychain lease failure before send: stop, no provider request.
- HTTP or stream authentication/entitlement/quota/rate-limit/unsupported-capability errors: bounded classification, stop, no retry/login/fallback. A quota code does not establish that the entire plan is empty or a reset time.
- Stream `response.failed` can arrive after output deltas; never turn those deltas into success. Preserve finite provider code/parameter and safe request ID, not raw provider messages.
- Timeout, cancellation, interrupted/malformed/oversized stream, credential echo, lost completion or process death after possible dispatch: unknown and consumed. Do not report remote rollback or zero usage.
- Receipt-write failure locks mutations; explicit read-only reconciliation is allowed, replay is not. Startup marks abandoned running claims unknown without credential I/O. Expired credentials remain a blocker, never an automatic refresh or re-login trigger.
- Temporary provider/network failure must not delete or rotate valid credentials.

## Workbench and verification

Keep the existing settings card, with prior connection/catalog evidence read-only. Show fixed model, complete synthetic prompt, endpoint, expiry, single request and no token-cap warning before approval. Separate approve and execute controls. Hide/disable real execution without matching explicit capability/admission; reject malformed/mixed provenance and stale responses. Unknown mutations offer only an explicit read of the same record. No background polling or request.

Tests must cover default-off before secret/network, wrong phase/account/model/route/digest/generation, expired material/credential at each boundary, same-snapshot catalog lineage, concurrent single claim, lease failure/change, redirect, 401/403/429/503, stream quota errors after deltas, missing completion, malformed/oversized/credential-echo output, output mismatch, unavailable usage, persistence failure and restart. HTTP tests cover Host/Origin/auth/recovery mode and unsupported client fields. UI tests and synthetic browser acceptance cover pending/approved/success/failed/unknown/expired and unchanged approvals. Run full Go/race/vet, frontend tests/build, clean-source export, secret/dependency scans and exact-commit GitHub CI before handoff. Real inference remains separately unapproved.

## Official protocol references

Checked 2026-10-21: [models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations), [errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery). Explicit user no-retry/no-refresh policy is stricter than optional recovery examples in provider documentation.
