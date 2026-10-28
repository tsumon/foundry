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

### Explicit protocol policy correction

An actual-constructor loopback TLS regression found that the catalog and fixed
diagnostic transports could negotiate HTTP/2 despite `ForceAttemptHTTP2: false`.
That flag is not an HTTP/2 prohibition: with default dialing and TLS settings,
Go can enable HTTP/2 automatically. Earlier TLS fixtures substituted a client
with custom dial/TLS settings, unintentionally masking this difference. The
task router had such a custom dialer and relied on its implicit HTTP/1 policy.

All three constructors now explicitly select HTTP/1 using `http.Protocols`.
They retain certificate verification, no environment proxy, fresh connections,
redirect denial, existing deadlines, and exact request bodies. This also avoids
HTTP/2's bodyless-request replay paths; disabling keep-alives alone was not
sufficient to express that catalog contract. It is not a protocol fallback or a
second request. No setting exposes an alternative endpoint or paid API.

The regression uses the actual constructors and a loopback server advertising
both HTTP/1.1 and h2. A separate test process trusts the synthetic certificate
through Go's fallback-root facility; it never disables verification or changes
system trust. Budget integration tests retain the production transport policy
and cover successful completion, missing Content-Type and interrupted streams.
Missing media still fails before body parsing, consumes the one reserved call,
and cannot be replayed. Historical evidence is not rewritten.

This fixes a demonstrated local protocol-policy mismatch. It does **not** prove
why an upstream response omitted Content-Type, nor that changing protocol will
restore it. Official [subscription inference requirements](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
still require the public Responses endpoint, `store:false`, `stream:true`, and
completed-stream handling; the payload and strict success boundary are unchanged.
Offline/CI acceptance is not a claim of successful real model inference.

### Request correlation and connection observation (offline-only validation)

Subscription task POSTs now attach a fresh random `X-Client-Request-Id` and
an additive `exchange_diagnostic` receipt. This is not an idempotency key and
does not permit replay. The ID contains neither account nor task identifiers.
OpenAI's [API request-tracing guidance](https://developers.openai.com/api/reference/overview#debugging-requests)
describes support-side lookup for supported endpoints. Availability on the
subscription preview route is not guaranteed; no API-key route is substituted.

The request-local observer records DNS/connect/TLS phase outcomes and elapsed
times, time to first response byte, time until `http.Client.Do` returns, write
callback counts, connection reuse, and final response metadata. DNS names,
addresses, certificate subject/issuer names, raw errors, raw headers, prompts,
credentials and bodies are not retained. Peer addresses become only
loopback/private/public/other classifications. A verified TLS handshake can
record SHA256 fingerprints of the public leaf and immediate issuer certificates,
TLS version and negotiated-protocol enums. A trusted certificate alone does not
rule out an enterprise interception CA or a provider-owned gateway.

`proxy_mode` describes the configured client transport, not proof of the entire
network path: `disabled` excludes its explicit proxy function, not VPN/TUN or
transparent intermediaries. `configured` does not prove a proxy was used.
Unobserved phases remain zero/absent; times saturate at 120000ms, phase/write
counts at 32, and header-name counts at 4096. Write callback counts are
not model execution or billing counts. The existing 90-second timeout,
fresh HTTP/1.x connections, certificate validation, redirect denial, no retries,
and exact content-type/completed-stream checks are unchanged.
No `Got1xxResponse` hook is installed: such a hook changes Go's cumulative
response-header read limit. Repeated informational responses remain subject to
the original aggregate limit, covered by a loopback regression.
The exchange observation retains the parsed three-digit status range (0–999),
including nonstandard replies, so malformed upstream statuses do not erase the
correlation ID. `headers.state: completed` distinguishes a received `000` from
no response. This does not change the router's status classification, its sole
HTTP-200 success candidate, or the existing receipt HTTP-status sanitizer.

Observation runs only inside an already-authorized existing dispatch. It creates
no sockets, timers, readers, background jobs or files itself. Late callbacks
after the HTTP return snapshot are ignored. Transport failure, rejection,
invalid stream and success can all carry this receipt through the existing
engine/store; neither observations nor a success receipt approve later work.
Old receipts are not backfilled. The existing workbench layout/actions remain
unchanged; metadata is available in the invocation receipt, not a new control.

Implementation and verification scope:
1. Use real loopback HTTP/TLS servers with synthetic credentials to prove missing
   trace/correlation/persistence before implementation (red), then assert the
   new trace without changing the failed or unknown result (green).
2. Cover strict missing-media rejection, valid completion, quota rejection,
   redirect denial, abrupt disconnect, untrusted local TLS, cancellation/late
   callbacks, redaction, immutable receipts and one consumed reservation.
3. Run Go/race/vet, web and desktop regressions offline with real integration
   opt-in flags unset. Keep original acceptance data and consumed budget intact.
4. Do not launch the production app, access credentials, refresh, query models,
   execute a VM, perform a real TLS probe, or send a real inference request.

This improves the next separately authorized observation; it does **not** fix or
prove the cause of the historical missing Content-Type. Any live probe or model
request still needs separate approval. No new live test is implied by these
offline test results.

### Earlier staged-parser diagnostic implementation

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
