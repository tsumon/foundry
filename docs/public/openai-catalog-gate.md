# OpenAI subscription model catalog: independent approval

## Scope

This increment adds a default-closed, independently approved **single model-directory request**. It preserves the current workbench, connection approval, task approvals and general model-router denial. A successful connection is a prerequisite, not permission to discover models. Implementation and acceptance use synthetic local services only. Real execution requires a later explicit operator decision.

The only permitted network operation is `GET https://api.openai.com/v1/models`, with the existing selected subscription access token. No prompt, source file, task context or conversation history is sent. No additional OAuth scopes are requested. The existing grant may permit more than this application operation; Foundry's gate restricts actual use. Official reference checked 2026-10-20: [Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference).

## Authority and default-off activation

Use a dedicated catalog controller/adapter, not a general `live_enabled` switch. Normal startup remains closed. A nonpersistent `--openai-catalog-route=<existing-route-id>` opt-in selects exactly one official OpenAI subscription route; it is mutually exclusive with connection startup options. Startup and all preparation/status reads perform metadata inspection only: no Keychain reads/writes/probe, provider traffic, login or token refresh.

The catalog admission has a fresh process generation and profile `openai_siwc_catalog_v1`. Connection provenance remains `openai_siwc_subscription_v1`. Catalog operations use `source: production_catalog` and `live_access: catalog_only`. Neither synthetic receipts nor connection permissions can be reinterpreted as catalog execution authority. The HTTP client has no configurable destination, environment proxy, redirect following or reused-connection retry path. The general router continues to reject every live route, including inference.

Existing acceptance-history and proposal/decision/execution endpoints are reused. Catalog status is exposed separately as `production_catalog_enabled` plus a bounded `production_catalog_blocker`; `production_admission` identifies the current profile/generation. Existing connection history remains readable when the catalog controller is selected. No stale process generation can execute after restart.

## Frozen material and independent approval

The server derives material from a read-checked database snapshot, not client-provided authority. Freeze the enabled account/revision, exact route with `extra_usage: off`, authorization ID/binding/generation/expiry, current successful real connection receipt, and the admission generation. The connection receipt must be the latest claimed connection for that account, with matching result snapshot; a newer failed/unknown connection or unresolved credential operation blocks preparation. No token or secret reference is returned in the material.

Material fixes `phase: catalog`, `request_method: GET`, `request_url: https://api.openai.com/v1/models`, `deadline_seconds: 30`, `max_attempts: 1`. It has no model, prompt, login timeout, OAuth handoff, requested scopes or new credential write. Approval expiry is the earlier of creation plus ten minutes and the recorded authorization expiry. Account/workspace binding or credential generation changes invalidate the material. Expired credentials stop preparation/approval/execution; no refresh or re-login is offered as an automatic recovery action.

Creating material, approving its digest/revision and explicitly executing remain separate actions. Proposal and approval do not lease a secret or send a request. Execution first commits the existing single-use claim and an independently checked dispatch reservation. Only then may the backend read the exact Foundry credential, revalidate its generation and all frozen authority, and issue its one request. No retry is allowed even after an uncertain response or process crash.

## Results and evidence

Accept a bounded JSON response with the documented `models` array; retain only entries with `visibility: list`, preserving server order and their `slug`/`display_name`. Reject malformed/duplicate identifiers, unsafe names, credential echoes, oversized payloads and unexpected media types. At most 128 display entries and 1 MiB response data are retained/parsed under the request deadline. Treat returned strings as untrusted display data, never instructions, routes or permissions.

Persist only the bounded normalized catalog and existing receipt metadata: operation/material digest, account/route/authorization snapshot, claim sequence, attempts, timestamps and finite error/certainty classifications. Do not retain raw HTTP responses, authorization headers, OAuth URLs, codes or token contents. Empty valid catalogs are shown honestly as empty. Model visibility does not establish successful inference, available quota or pricing; quota and remote billing remain unknown/unverified. No role policy or existing/new task snapshot changes automatically.

## Failure and recovery

- Before sending: invalid/missing/expired/changed authorization, cleanup in progress, failed Keychain read or rejected approval stops with no provider request. Failure does not delete the established connection or rotate credentials.
- After dispatch: authentication, entitlement, rate-limit, quota and server failures use bounded classifications. No provider message or body is logged.
- Timeout, interrupted transfer, invalid response, lost receipt or process termination after possible dispatch remains unknown; consumed claims/reservations cannot replay.
- Receipt persistence failure closes mutation authority. Existing recovery marks interrupted running operations unknown without issuing network traffic or re-reading tokens.
- Approval expiry during execution never extends the network deadline. Completion cannot silently adopt changed credentials/account bindings. Old catalog evidence remains historical, not a reusable execution permit.

This read-only operation has no remote mutation to roll back. Recovery means conserving evidence and refusing replay, not pretending a possibly sent request was undone. No credential deletion, refresh, inference, paid fallback, scripts, Git writes, Kimi or external service integrations are added.

## Workbench and acceptance

Keep Settings → Models and the existing approval-card layout. In catalog mode show the prior real connection receipt read-only and only the directory phase as mutable; do not expose a live inference button. Show the exact GET destination, credential generation/expiry, connection prerequisite, no-code-data statement, single attempt and disabled downstream abilities. Disable actions on malformed/mixed provenance, stale generation, expired credentials/material and uncertain writes. A failed mutation permits explicit read-only reconciliation of that operation, never replay. No background polling or automatic catalog fetch is introduced.

Tests use in-memory secrets and owned local/mock transports; no native Keychain or real account is needed. Cover default-off, exact method/origin, missing/unapproved/stale/forged material, duplicate concurrent execution, expiry before and during execution, changed account/auth, secret-read failure, redirect, 401/403/429/5xx, malformed/oversized/credential-echo response, receipt failure and restart. Verify zero inference/refresh/retry/fallback and unchanged connection/simulation behavior. Frontend tests and a visibly labelled local browser fixture cover pending, approved, successful, failed/unknown and expired states.

After tests, clean-source scanning and exact-commit CI pass, prepare a real pending material only if metadata remains eligible. If the credential has expired, leave the workbench blocked with the truthful expiry reason; do not fabricate a pending executable material. Real execution still requires the user's later independent approval.
