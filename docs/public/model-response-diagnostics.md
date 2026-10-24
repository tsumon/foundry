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
