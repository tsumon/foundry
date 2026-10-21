# C2: traceable failure, explicit replanning, evidence-backed review

## Scope and authority

C2 builds on C1 without replacing the engine, workspace, UI, or independent
context/execution/delivery approvals. Subscription work stays paused. Acceptance
uses local model fixtures and content-check runners only: no real account,
Keychain, provider, Docker, external MCP, generic script tool, source-repository
Git write, automatic refresh, transport retry, or paid fallback is authorized.
Real Docker/project-command acceptance remains a separate milestone.

New ordinary recoverable tasks opt into `coding_version: 1`. The creation form
selects it with `generations_v1`, not with legacy or specialized tool modes.
Version zero remains the unchanged historical contract. The coding version is
immutable and authority-bound; unknown versions fail closed. Existing tasks,
reports, hashes, approvals, and source labels are not migrated or backfilled.

## A. Controller-owned execution trace

Each version-one execution records local attempt IDs, epoch, plan revision,
phase/role, step or repair round when known, selected candidate generation and
digest before/after, verification ID when available, timestamps, status, and a
finite failure code. A real existing invocation ID may be linked; a local
attempt ID must never be advertised as a provider receipt or historical call.

Worker completion means candidate changes were generated, not tests passed.
Whole-candidate verification stays whole-candidate evidence; shared paths do not
prove a particular step caused a failure. Distinguish validation failure,
invalid model output, denied edit, rejected review, infrastructure failure,
persistence failure, interruption, and unknown outcome. Unknown remains unknown;
model guesses and error-text substring matching are not authoritative typing.

Persist the attempt before dispatch, finish it once, and never rewrite a terminal
attempt. Epoch fencing rejects late results. Recovery marks unfinished attempts
interrupted/unknown without replay; failed persistence must not select an
uncommitted candidate or continue the workflow. Preserve the existing maximum
two budgeted Debugger repair rounds; infrastructure/authority/storage/unknown
outcomes do not trigger model retries. Existing cumulative call budget survives
replanning. New trace facts appear in existing plan, verification, and activity
views, with historical absence explicitly distinguished from success.

## B. Failure-driven explicit replanning

For a failed version-one run, the existing explicit planning action freezes one
read-only FailureBundle for the most recent run before resetting from the base.
It binds mission, epoch, plan revision, base/candidate identity, archived-run
digest, trace, failed verification/source, and rejected reviews. Verify identities,
current candidate and archive consistency before use. Historical unsealed data
cannot silently become a verified failure bundle. Missing, altered, cross-task,
or unsupported material blocks dispatch rather than silently falling back.

Only bounded, sanitized projections enter Planner prompts: maximum encoded bundle
128 KiB, each log excerpt 8 KiB, explicit truncation and original-record digest/
reference. No profile/credential objects, entire repository, or unbounded run
history is included. Repository/log/model text stays untrusted data, not authority.
Redact recognizable credential assignments, authorization tokens, and credential
URLs; this is not a claim of general-purpose secret detection.

Planner must map each blocking failure to proposed step IDs and explain the
adjustment, or explicitly report it unresolved. Unresolved blockers cannot receive
execution approval. References must name supplied failures and current steps.
The bundle digest and adjustments bind the proposal and execution approval;
material changes invalidate old authority. User revisions retain/check these
relationships and remain proposals, never execution approval.

Replanning archives the old run and creates a fresh candidate from the frozen
base, retaining old generations as history. It does not resume the failed
candidate, reset budget, copy passing reviews/results, or reuse execution/delivery
approval. Changing scope, protected rules, acceptance commands, or other frozen
inputs requires a separately authorized new task. Interrupted operations require
explicit action; no transport or model-call replay occurs on restart.

## C. Distinct review contracts

Version-one QA reviews every frozen acceptance item against the latest matching
verification and its source. Auditor reviews approved file scope, protected-file
integrity, current approval/plan identity, and evidence integrity. Structured
checklist references must resolve to controller-supplied IDs in the same task,
epoch, plan, and candidate; missing/duplicate/foreign/stale references fail closed.

Passing review requires every mandatory check, no unresolved/blocking finding,
and matching candidate, verification, trace, and plan bindings. Controller checks
are decisive: model prose cannot turn failed verification into success. A model
review is never presented as an independently executed compiler/test. Retain C1
simulation/unknown/isolated source distinctions. Rejection becomes future failure
material, never an automatic bypass or implicit replan.

## Workbench and delivery

Keep navigation and layout. Enhance current plan rows, verification cards,
decision summaries, and task activity only. Show what failed, what changed in the
new proposal, unresolved items, and links to relevant evidence. Reuse existing
approval actions and CAS checks. Newly generated reports include the same trace,
failure/adjustment summary, review checklists, and source labels; bind these to
report/delivery integrity. Historical report bytes remain unchanged.

## Acceptance and next milestones

Use actual Engine/Store/Workspace/HTTP with disposable committed repositories,
scripted local model outputs, and content-driven simulated checks. Prove failure
exhaustion, explicit replan with matching failure inputs, independent approval,
corrected candidate/reverification, both reviews, delivery approval and export.
Negative tests cover unchanged bad content, missing/wrong citations, cross-task
material, tampering, stale approval, protected files, quota exhaustion, crashes,
late results, failed storage, and recovery without replay. Old contracts and
reports must retain compatibility. Run full Go/race/vet, frontend tests/build,
clean export/security scans, browser acceptance, and exact-commit GitHub CI.

C3 adds visible repository-import and recovery diagnostics. C4 separately proves
a small real project with offline, pinned, trusted isolated verification while
models may remain simulated. Neither is claimed by C2 or depends on subscription
login. Source repositories remain read-only; delivery is a separately approved
patch, not automatic application or Git publication by the product.
