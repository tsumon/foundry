# OpenAI subscription connection preparation — local simulation only

## Scope and release boundary

The next connection path is the documented open-source/local-app subscription flow, not a standalone identity-only prerequisite. This increment prepares its approval materials and failure recovery using process-owned local fixtures. It does **not** register an OpenAI client, open a real login, contact an external provider, read a real account, query a real directory, run inference or refresh a token.

Normal Foundry remains inert. There is no environment switch or browser request that enables live access. Existing workbench navigation, task approvals, MCP boundaries, deployment/database approvals, script denial and Git-write denial remain unchanged. No automatic retry, automatic refresh or paid-API fallback is introduced. Kimi receives no new integration work.

## Connection approval contract

A dedicated constructor-owned OpenAI simulation account and route use `connection_purpose: subscription_only`. Unlike the older three-phase laboratory fixture, this route accepts **connection proposals only**. It does not borrow identity-only receipts or unlock catalog/inference proposals. The existing identity-only fixture remains regression coverage; historical records keep their original meaning and digests.

The server freezes these exact values in the material and its digest:

| Field | Value / meaning |
| --- | --- |
| Requested scopes | `openid profile email offline_access resource.invoke chatgpt.tokens.use.direct` in that order |
| Requested resource | `https://api.openai.com/v1` — the protocol audience, **not** the simulation transport destination |
| Connection purpose | `subscription_only` |
| Route / account | Exact owned simulation route, provider, account revision, credential/authorization snapshot and loopback endpoint |
| Attempt budget | One explicit execution after separate approval; ten-minute approval expiry and at most thirty-second execution deadline |

Scopes and resource are server-owned. The browser cannot customize, omit, append or override them. The local protocol fixture verifies the scope/resource contract at authorization and code exchange. New optional fields are omitted from old serialized material so old digests are not silently rewritten.

The approval screen distinguishes **requested protocol permission** from **enabled application behavior**. In particular, requesting `offline_access` in the simulated subscription flow does not enable refresh. A successful simulated connection is not permission to query a directory, infer, spend money, or claim that a real subscription/quota was verified.

## Execution, proof and recovery

1. Propose a connection from the existing account-detail card. Read the frozen permissions, resource, local endpoint and limits.
2. Approve or reject the exact revision and material digest. Approval itself has no network or credential side effect.
3. Explicitly execute once. The durable claim is consumed before the owned local protocol runs. Concurrent clicks, a stale tab, a wrong purpose or a changed account cannot reuse the approval.
4. Success requires new matching subscription authorization evidence, not an old identity or subscription receipt. Evidence contains sanitized metadata only. Tokens, Keychain references, authorization URLs, state/nonce and raw responses never appear in the acceptance receipt.
5. Rejection is a terminal finite failure. Timeout, cancellation, interrupted callback or an uncertain completion write retain a consumed/unknown result. Callback work is canceled and joined before returning. No replay, implicit refresh or paid fallback occurs.
6. Restart reconciliation marks interrupted running operations unknown with the attempt still consumed. Storage/recovery failure disables mutations. An explicit history read may recover a lost UI response but never retries execution. A fresh attempt requires a new proposal and new approval.

Catalog, inference and refresh remain unavailable for this dedicated account, including through older direct API shortcuts. Connection evidence records quota as `unknown`, remote billing as `unverified`, and live access as `blocked`. Failure cleanup preserves auditable history; it does not claim to undo a provider effect. Existing legacy local laboratory routes are not promoted into this new policy or removed by this change.

## Workbench presentation

Keep the card in **Settings → Models → account detail**. Show connection purpose, six requested permissions with short explanations, resource versus local endpoint, one-attempt/deadline limits and disabled downstream capabilities. Use the existing separate proposal, approval and execution controls and receipt list. Unknown or inconsistent policy/material responses disable actions. No new top-level navigation, charts, background execution or polling.

## Acceptance and publication gates

- Domain/store: exact scope/resource/digest binding; historical digest compatibility; changed/expired proof, wrong account, wrong purpose, replay and stale revisions denied; fresh proof required; no catalog/token-usage output accepted for a connection-only receipt.
- Service/HTTP: default constructors deny before effects; only constructor-owned loopback fixtures execute; strict JSON, launch-session/Host/Origin and read-only recovery checks remain; catalog/inference/refresh and legacy bypass attempts have zero downstream effects.
- Protocol/recovery: real local HTTP authorization/code exchange with synthetic credentials only; one attempt; denial, timeout/cancellation, interrupted write and restart; no inference, directory, refresh or external-provider hits.
- UI: review materials before execution; disabled phases; rejection, unknown and lost-response recovery; malformed server material fails closed; real desktop and narrow-layout browser checks on a disposable local fixture.
- Release: full existing tests/race checks/build, clean-source export, secret/vulnerability scan, reviewed public diff. Push only the reviewed public source to `tsumon/foundry`; inspect CI for the exact pushed commit. No Release or private evidence upload.

## Official protocol references

The next implementation increment is specified in [the closed subscription adapter design](openai-subscription-adapter.md); it does not authorize real login.

Checked 2026-10-20: [Open-source/local-app sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations), and [errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery). The application restrictions above are intentionally stricter than protocol capabilities. Eligibility, real consent, account/workspace selection and any real call still need a future separately approved acceptance step.
