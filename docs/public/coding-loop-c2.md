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

If selection committed before an interrupted edit's terminal trace could be
saved, explicit freezing may retain a separate, sealed local-operation fact.
It must match that final attempt's task, plan, input generation and time window,
the committed result, frozen authority, edit chain and on-disk generation parent.
The interrupted trace is never rewritten or promoted to success; only the known
candidate selection is reconciled. Unknown outcomes remain unresolved.

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

### Version-one wire contract

The ordinary prompt supplies `coding_identity` (mission ID, epoch, selected
generation) only for version one. Review `extra` is a controller-generated
contract containing `mission_id`, `epoch`, `plan_revision`,
`candidate_generation`, `plan_digest`, `trace_digest`, `evidence_digest`, the
current `execution_approval`, `binding_digest`, and `required_checks`.
Repository text, findings, logs and prior output remain untrusted data.

Both reviewers return only `passed`, `summary`, `findings`, `binding_digest`,
and `checks`. The binding digest must exactly echo the supplied contract. Each
check has `id`, explicit boolean `passed`, nonempty `summary` (at most 4,000
UTF-8 bytes), and `paths` (at most 128 distinct supplied paths; empty is allowed).
The review summary and finding summaries have the same text bound; at most 64
findings are accepted. Missing/null decisions and unknown fields fail closed.

- QA requires `acceptance:0` through the final frozen command index, exactly
  once each, plus `acceptance_index` and the latest current `verification_id`.
  It cannot include Auditor reference fields. The controller independently
  checks command order, count, exit codes, timeout, candidate, plan, image and
  a successful verification trace record. Source labels remain independent of
  the result; a synthetic content check did not execute the command.
- Auditor requires `scope`, `protected`, `approval`, and `integrity`, exactly
  once each, plus each supplied `reference_digest`. It cannot include QA
  verification/index fields. These are purpose-separated digests of the common
  controller binding, not self-asserted permissions. Before and after review,
  the controller checks actual base/candidate trees, exact plan files, default
  and task protection rules, recorded edit-chain contents and frozen authority.

The common binding covers task/epoch, selected generation, immutable plan
content (excluding step runtime status), execution approval, frozen inputs,
changes, all verification material and sealed failure context. Its review trace
projection excludes QA/Auditor attempts to avoid self-reference; full archived
failure trace digests still include those attempts. Any relevant material
change invalidates the review. Passing prose, a failed mandatory item or a
high/critical/blocking finding cannot authorize delivery. Well-formed refusals
are retained as rejected reviews; malformed output is retained as a typed
failed attempt for the next explicit failure bundle.

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

### Local acceptance scenario

`foundry-acceptance` includes `coding_loop_replan` in its printed scenario map.
It creates only a version-one ordinary **draft**, with no seeded execution or
delivery approval. Open the corresponding existing workbench task and:

1. Authorize context and request its initial plan; inspect/approve execution.
2. Observe the unchanged bad candidate fail three content checks (Worker plus
   two budgeted Debugger repairs), then stop blocked.
3. Explicitly choose failure-based replanning. The fixture requires the exact
   controller-frozen bundle; it rejects absent, cross-task or altered material.
4. Inspect the failure-to-step adjustments, optionally revise the plan, and
   grant a fresh execution approval. Revision alone does not execute.
5. Observe the corrected Worker candidate, simulated passing verification and
   separate QA/Auditor checklists. Independently approve export, then download
   the patch and report. The report contains current checklists, the complete
   trace, failure archives and plan adjustments; download revalidates them.

The automated scenario traverses real Engine/Store/Workspace/HTTP routes with
disposable committed source, checks its source checksum and Git status remain
unchanged, and runs only `git apply --check` on the export (never applies it).
Focused negatives cover invalid references, changed material, review disagreement,
missing/stale approvals, bounded repairs and late results/restart without replay.
This is local simulation evidence only: no real account/Keychain/provider,
Docker command, external MCP/script, source Git write, automatic refresh/retry
or paid fallback is exercised by C2. C1 scenario behavior is unchanged. Final
clean-export, security, browser and exact-commit CI gates are separate evidence;
this scenario does not claim those gates or real project-command acceptance.

C3 adds visible repository-import and recovery diagnostics. C4 separately proves
a small real project with offline, pinned, trusted isolated verification while
models may remain simulated. Neither is claimed by C2 or depends on subscription
login. Source repositories remain read-only; delivery is a separately approved
patch, not automatic application or Git publication by the product.
