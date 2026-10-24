# Explicit task authorization continuation

Status: backend and workbench implemented and local-fixture tested; real-task
continuation is not accepted. No existing real task is rebound by installation,
startup, account connection, or these tests. Fixture-only browser acceptance
passed on 2026-10-24, including independent prepare/approve/apply, retained 1/7
unknown usage, stale approval denial and readable original snapshots when
continuation history is malformed. No installed real task was used.

## Problem and outcome

SIWC sign-in intentionally issues a fresh local authorization binding while
retaining the registered client and validated identity. Successful replacement
retires the old local credential. Existing tasks freeze their original binding;
their catalog receipt also pins the old authorization generation and expiry.
Automatic selection of the newest credential would silently change authority.

Add explicit, task-local continuation with separately prepared, approved and
applied materials. Preserve the original model snapshot and profiles, task ID,
repository import, protected files, baseline, evidence, invocation ledger and
budget. A continuation authorizes metadata selection only, never a provider call,
catalog request, credential read, VM operation or repository modification.

## Supported boundary

Initially support model-backed coding tasks before an approved implementation
plan: blocked/paused, no active operation, no candidate changes, and no running or
reserved model invocation. Preserve terminal unknown outcomes and their consumed
budget. This is not automatic retry or replay of a prior invocation.

All affected roles must retain the exact account, provider, identity issuer and
subject, registered client, route, protocol, model, scopes and subscription-only
spend policy, with extra usage off. A scope change or a different account is not
continuation. Do not infer workspace identity from a subject: the current binding
does not contain an independently verified workspace field.

Use only already-recorded successful independently approved catalog evidence for
the replacement authorization. Production receipts must pass existing catalog
lineage checks, include the frozen model, and pin the new generation/expiry.
Missing or expired evidence blocks preparation/application; never query a provider
to fill a gap. Local fixtures may use the production catalog lifecycle with a
synthetic dispatch result, never an alternate production bypass.

## Immutable history and effective authority

Keep the original snapshot untouched. Append continuation materials and applied
records containing the predecessor effective snapshot digest, replacement
snapshot, exact authorization generation/expiry, catalog provenance, affected
roles, prepared task revision, creation/expiry, approval and application facts.
Each material has a canonical digest. Previously applied records are immutable;
only an approved, atomically applied record selects a new effective snapshot.

Future planning and invocation admission resolve the effective snapshot and bind
its digest. Historic invocation validation resolves its original digest against
the immutable original/applied snapshot history; it never reinterprets an old
invocation against the newest binding or replays it. Malformed/unknown continuation
formats fail closed while retaining readable, sanitized history.

Ordinary mission mutation cannot append, alter, delete, reorder or activate
continuations. Use dedicated transactional operations with exact revision and
digest checks. Preparation, approval and application recheck account/credential
and catalog metadata, task eligibility and absence of pending credential cleanup.
Materials expire no later than ten minutes or the credential/catalog expiry.
Concurrent task/account/grant changes invalidate unused materials. Apply is
single-use and atomic; restart never completes or repeats it automatically.

Application does not clear prior approvals or evidence. Instead, future context
and execution approvals must postdate the applied continuation and bind the new
effective snapshot. Old approvals remain audit records, not reusable authority.
Budget max/used and unknown-usage markers remain unchanged; invocation ordinals
continue globally within the original task.

## Independent gates and recovery

The real sequence remains separately approved: new connection; new catalog query;
task continuation; current VM readiness; context/budget approval; plan execution;
candidate testing and delivery. No action approves the next action.

A completed baseline attests frozen source and sandbox profile, not an OAuth
grant. Preserve it when commit/source/profile digests still match. Preserve its
historical readiness binding; acquire new readiness separately for future work.
Do not rerun a baseline merely to change an authorization.

An installed application update may contain a different embedded VM manifest,
root filesystem or verifier. This is a runtime identity mismatch, not evidence
that the task's model snapshot is corrupt. Startup reports
`c4_runtime_mismatch` and remains instance-wide read-only. History, the baseline,
original snapshot and consumed/unknown budget remain unchanged; old approvals
cannot authorize the new sandbox. Authorization continuation does not migrate a
sandbox binding. Restoring an exact trusted runtime, or designing a separately
approved runtime transition, is distinct work; neither happens automatically.
Missing/unknown recovery diagnostics use neutral wording, not a corruption claim.

On mismatch, expiry, interrupted transaction or cleanup uncertainty, stay blocked.
There is no rollback to a revoked credential. Retain the failed material and
original records; a later explicit preparation is a distinct operation. A commit
that succeeded before a lost response is displayed as applied, never replayed.

## Workbench and privacy

Use the existing task model/details area. Distinguish original frozen snapshot,
effective approved continuation, expired/missing catalog, and pending material.
Controls prepare, approve, and apply independently; no automatic checkbox or
execution chaining. Explain that the local authorization ID is per sign-in, not
the registered OpenAI client ID. Display remaining task calls, with unknown
provider usage retained. Continuation renderer DTOs contain no subject, client
ID, secret reference, OAuth parameters or tokens. Activity records contain safe
IDs/digests and outcomes only. Existing account-management identity display and
its general authorization DTO remain unchanged; continuation never consumes that
general list.

The explicit local-candidate read uses
`GET /api/missions/{id}/model-continuations/candidates`, with no query parameters
or request body. Its response is `{ "authorizations": [...] }`; each entry has
only `id`, `account_id`, `state`, `generation`, and `expires_at`. It reads the
current local grant for each distinct SIWC account in the task's validated
effective snapshot (the frozen original before application), never unrelated
accounts. It performs no mutation, secret read, login, refresh or provider query.
Recorded catalog choices still come from the existing local desktop-preparation
GET. Missing evidence stays missing. The read does not grant approval or replace
the exact prepare/approve/apply validations.

Original snapshot display validity is evaluated independently of continuation
history. Invalid history retains readable valid original details but yields
`model_continuation_status: invalid_or_unsupported`, no valid effective selection,
and blocked continuation/context/execution controls; malformed persisted history
is not returned to the renderer. Historical readability is not current authority.

## Acceptance

Use throwaway SQLite stores, synthetic identities/catalog receipts and fake
provider/VM/secret boundaries. Cover same-identity replacement, changed identity,
account/route/model/scope/spend drift, expired grants/catalog/material, stale task
revisions, double clicks, concurrent updates, active calls, unknown usage, cleanup
uncertainty, transaction failure/reopen, history tampering and malformed DTOs.

Prove old snapshot/records/baseline remain byte-identical, used budget does not
decrease, previous context approval cannot dispatch, new calls use the approved
effective selection only after a fresh context approval, and historical receipts
still validate. UI tests cover pending/expired/rejected/applied states and zero
automatic requests on render. Run focused RED/GREEN, full offline regressions,
race/vet, frontend tests/build and a fixture-only browser acceptance.
