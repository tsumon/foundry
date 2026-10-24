# Task model response diagnostics

## Scope and design

The task router records bounded diagnostics for subscription response failures.
This is observability, not a parser relaxation or proof that the provider failed.
HTTP 200 alone is not success. A completed event must still pass the existing
response, assistant-message, text-content and credential-echo checks. Partial
deltas, incomplete/failed events, malformed frames and invalid output stay failed
or outcome-unknown exactly as before. There is no retry, token refresh, route
fallback, budget refund, approval or execution implied by a diagnostic.

`response_diagnostic` is optional versioned metadata in the existing model call
receipt: fixed stage/reason/media/terminal enums plus bounded event and byte
counts. Counts describe bytes read (including buffering), not tokens or billing.
No raw headers, event names, response bodies, deltas, reader error strings,
prompts, paths or credentials are included. Only known terminal event names are
retained. Domain/store validation rejects arbitrary or contradictory metadata;
the UI renders a closed vocabulary and never echoes unknown diagnostic values.

Stages distinguish HTTP media validation, stream reading, SSE decoding,
provider terminal events, completed-response validation and credential guarding.
EOF without a terminal, an unterminated frame, read failure and size/line bounds
remain distinct observations, not claims about which party caused the failure.
Unknown event payloads remain inert. No additional reads or requests are made.

Old receipts remain unchanged and display “no staged diagnostic recorded”; the
software cannot reconstruct missing historical evidence. Existing task budget,
baseline, candidate, approvals and evidence are not migrated or rewritten.

## Observed live stop, 2026-10-24

One separately authorized Mac task invocation on `61cb519` recorded HTTP 200,
`http_media / missing_media_type`, `events_seen: 0`, `bytes_read: 0`, unknown
usage and a consumed reservation. This means the response reaching the router
had an absent or empty Content-Type value as seen by `Header.Get`. The old receipt
cannot distinguish these cases. The router stopped before SSE decoding; it does **not** prove
that an SSE stream was empty, that the provider emitted `response.incomplete`,
or that inference was not performed remotely. The body was not retained, and
the historical request cannot be reconstructed by replaying its code or token.

Inspection of the task transport confirms `Accept: text/event-stream`, JSON
request content, `stream: true`, `store: false`, and no max-output-token field on
the subscription route. It does not strip the response Content-Type. The current
evidence cannot distinguish upstream omission from an intermediary. Keep the
strict success boundary and unknown outcome; do not invent a parser fix or infer
safe replay. Further live diagnosis needs a distinct explicit authorization.

## Bounded offline transport diagnosis

The follow-up fixes the diagnostic blind spot, not a proven upstream defect.
After `http.Client.Do` returns and before router header handling, a receipt may
now record an optional version-1 `transport` observation: Content-Type is absent,
empty, single non-empty, or multiple; the parsed protocol is HTTP/1.0, HTTP/1.1,
HTTP/2, or other; and `ContentLength` is zero, positive, or unknown. These are
closed enums, not raw headers or packet captures. Decompression/chunking may
make the reported length unknown. No extra body reads, request, or retry is
introduced. No provider identity, URL, credential, header value, or body is logged.

The transport is an isolated standard-library client with no environment-proxy
configuration, redirect following, or keep-alive reuse. The router only removes
credential echoes from request-ID response headers; it does not remove
Content-Type. That narrows the local path but cannot prove where the historical
header was lost outside it. No success fallback by body sniffing is permitted.

Offline implementation/verification sequence for this bounded fix:
1. Reproduce absent and empty headers over an actual loopback HTTP connection,
   including complete SSE content and a valid Content-Type control case.
2. Capture the fixed-value transport observation at the HTTP return boundary;
   propagate it to failed receipts without changing parser or status semantics.
3. Test redaction, closed-vocabulary validation, persistent unknown outcome,
   one consumed call, no replay, and immutable historical receipts.
4. Verify the existing activity display with synthetic receipts. Historical
   receipts show missing transport evidence rather than invented observations.
5. Run offline regressions and compare the original task, invocations, baseline,
   evidence and 2/7 budget before/after. Do not replay the real request, refresh
   credentials, execute a VM, replace the installed app, or claim live recovery.

## Offline implementation and acceptance plan

1. Add table-driven failing task-router tests for content type, invalid JSON,
   missing/mismatched event type, premature EOF, unterminated frames, reader
   failure, bounded streams, explicit incomplete/failed/error and rejected
   completed envelopes; retain positive completed-response coverage.
2. Add fixed-value diagnostic validation, propagate it through ProviderError and
   ModelCallReceipt, and retain existing status/certainty/error-code semantics.
   Verify unknown/malicious values are omitted or rejected, not persisted.
3. Exercise the real router/engine/store against loopback fixtures: one dispatch,
   one consumed reservation, no output evidence on failure, unknown usage and
   unchanged independent approval behavior. Reload persisted receipts.
4. Add read-only activity-row display and regressions for known diagnostics,
   unknown diagnostic values and historical receipts. No new action buttons.
5. Run focused red/green tests, full Go tests/race/vet, frontend and desktop
   suites, source/security checks and an isolated synthetic UI acceptance.
6. Review changes, commit/push only public source, check required GitHub CI,
   rebuild the Mac DMG from the exact revision with existing verified runtime
   assets, and run isolated installation checks without real accounts or VMs.

Real model attempts, refresh, retries, sandbox execution and changes to original
acceptance evidence are outside this plan. Any later real attempt needs fresh
readiness and separate user authorization. Local checks do not certify provider
behavior. Packaging remains ad-hoc signed, not Developer ID notarized.
