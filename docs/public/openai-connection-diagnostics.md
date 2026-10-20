# Bounded connection failure diagnostics

## Scope and authority

This increment adds evidence, not permission. Keep the current workbench and independent proposal → approval → explicit execution flow. One approved connection still allows one attempt. Login, model catalog, inference, refresh, retries, paid fallback, scripts and Git writes are not enabled by diagnostics. Validation uses synthetic loopback services and synthetic credential stores only.

The existing callback reports a generic failure, and the handoff controller collapses most terminal failures to `transport_unknown`. Add a bounded diagnostic to new connection receipts so a local failure stage is distinguishable without retaining provider bodies or private OAuth material. This cannot reconstruct the cause of an older attempt.

## Contract

A version-1 `connection_diagnostic` is optional, output-only, and separate from approval material and its digest. It is written atomically with the existing terminal receipt. It is never accepted from a proposal, decision, execution or cancellation body. It cannot change a claim, grant an action, restore a handoff URL or reset attempts.

Record only an allowlisted stage and code, optional bounded HTTP status and allowlisted provider error code, and independently derived local credential/cleanup outcomes. No free-form error messages. Stages distinguish callback validation, browser authorization, authorization intent, token exchange, scope/grant validation, OIDC discovery, signed identity validation, credential commit and session termination. Known provider codes may include `invalid_client`, `invalid_grant`, `subscription_sharing_user_not_eligible` and known refresh-token failures; unknown values are omitted rather than echoed.

New identity-validation failures may also record a static, output-only `identity_check`. Its closed vocabulary and compatibility boundaries are specified in [OpenAI identity validation compatibility](openai-identity-validation.md). Older receipts remain unchanged and do not establish a specific failed identity predicate.

Do not store or display tokens, authorization codes, callback query strings, state, nonce, PKCE, authorization URLs, account identity claims, email addresses, Keychain references, response bodies, arbitrary headers or raw exception strings in diagnostic records. Provider-controlled strings must not become a log format. UI copy is selected from static labels, never interpolated from an unknown diagnostic value.

Preserve conservative terminal-state semantics: diagnostic detail alone must not turn an unknown outcome into a success or prove a remote rollback. Cancellation, expiry and interrupted processes remain consumed. An incomplete response remains unknown even if local cleanup succeeds. An invalid proof never becomes a usable credential.

## Credential and recovery evidence

Credential outcome is `not_committed`, `committed` or `unknown`, based on the exact operation's durable authorization record, not on a global credential version or absence of a success message. Cleanup outcome is `not_required`, `completed`, `pending` or `unknown`. A failed durable read yields unknown; a pending write or cleanup must never be presented as completed. Initial pre-intent failures can say no local credential commit was started, but cannot claim a remote grant was revoked.

Capture terminal evidence only after owned callback work and cleanup have joined. Do not perform extra Keychain reads or deletions for display. A successful commit followed by another failure must not be described as never saved. Recovery must not fabricate a precise stage after a crash. Previously terminal receipts without diagnostics stay unchanged and are labeled “stage not recorded.” The receipt's cleanup observation is a snapshot, not a promise about future state.

## Workbench and callback

Keep the current account-detail connection card and receipt list. In a failed/unknown connection, show the recorded stage, a safe failure explanation, optional HTTP status/provider code, local credential and cleanup status, and the next safe step. Keep the existing consumed-attempt notice and manually requested status read. Do not add a retry, reconnect, model probe or permission toggle.

The callback failure page must explain that this attempt has stopped and direct the user to the workbench; it must not echo query parameters or raw errors or offer replay. Historical or malformed diagnostic data gets a conservative fallback, never a blank page or an enabled control. Remote authorization remains explicitly unverified.

## Implementation and acceptance plan

1. Define and test the bounded diagnostic contract and output-only validation.
2. Carry safe stage information from the connection core through its terminal handoff to the existing receipt transaction. Preserve code exchange count, lifecycle cleanup and concurrency boundaries.
3. Add deterministic loopback fault cases for token rejection, dropped response, malformed response, scope mismatch, discovery and proof validation, credential write failure and cleanup failure. Assert safe evidence and no replay or downstream use.
4. Add the small failure-detail view and callback guidance; test unknown/malicious values, historical receipts and unchanged approval controls.
5. Test persistence/restart, cancellation/expiry, receipt-write failure, account/revision isolation and disabled capabilities. Run unit, integration, race, static, frontend and build checks.
6. Inspect desktop and narrow workbench views using synthetic local data; export and scan only allowlisted source and public documentation. Push only after acceptance and inspect CI for the exact commit. No Release and no renewed real attempt.

This is not evidence that any real provider failure has been corrected. A later real connection requires fresh material and explicit user approval.
