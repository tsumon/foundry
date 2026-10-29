# C3: repository admission and read-only recovery diagnosis

<a id="candidate-020-explicit-dependency-input-and-schema-2-inspection"></a>

## 0.2.0: explicit dependency input and schema-2 inspection

The current implementation extends the original C3 contract below. Its original design/fixture limitations are historical; the [controlled workbench](live-coding.md) separately composes real fixed VM verification. Published platform packages require exact release receipts; [Mac native and scoped provider evidence](alpha-status.md) is recorded separately from that publication gate. User instructions are in the [language/input walkthrough](desktop-guide.md#candidate-020-language-and-dependency-inputs).

`GET /api/coding-tasks` retains the legacy Go `available/profile/backends` fields and adds exactly three ordered `profiles` templates: `c4_node_model_v2`, `c4_typescript_model_v2`, `c4_python_model_v2`. Each has language/runner/verifier, `mode:embedded_vm`, limits, eligibility/reason and, when installed assets validate, the full VM identity plus `runtime_inventory_digest`. Top-level Go availability does not determine v2 eligibility. These templates cannot execute and do not prove a successful readiness check. Reading them allocates no VM session.

`POST /api/coding-dependencies` is a separate consent to bounded local copy/storage, never a package installer. Required fields are `profile_id`, exact installed `embedded_vm`, `runtime_inventory_digest`, absolute transient `input_directory` and `consent:true`, plus exactly one source selector: `repo_path`, or `repository_id` with positive `repository_revision`. JS/TS uses the pre-existing node_modules root and committed root package.json/package-lock.json v3; Python uses a wheel directory and committed foundry.python.lock.json. No client-selected dependency store, source bytes, command or authorizing receipt is accepted.

Success returns exactly `admission_digest`, `dependencies`, `source_files` and `summary`. The binding contains schema/ecosystem, bundle/manifest digests and file/byte counts. The bounded summary contains policy, user-supplied provenance, the same digests/counts and sorted package identities with an explicit omitted count. These counts describe copied payload, not completed tests or publisher authenticity. The absolute input path is neither retained in the receipt nor sent to models. Failed or unknown publication supplies no usable admission. Exact immutable metadata and complete payload are rechecked before creation, model context and verification.

Use the existing `POST /api/repository-imports/inspections` consent action with original scope/protected/acceptance/image fields plus `spec.verification_v2`. The selected declaration has `schema_version:2`, `profile_id`, complete installed identity, inventory/admission/dependency binding, `source_root`, `test_root` and sorted `test_support_prefixes`. Source root is exactly `.` or `src`, Node/TS test root `test` or `tests`, Python `tests`; scope still lists concrete editable paths, never `.` or globs. V2 image is empty; acceptance is the selected single fixed verifier:

| Profile | Acceptance verifier |
| --- | --- |
| `c4_node_model_v2` | `/opt/foundry/verify-node-v2` |
| `c4_typescript_model_v2` | `/opt/foundry/verify-typescript-v2` |
| `c4_python_model_v2` | `/opt/foundry/verify-python-v2` |

A ready schema-2 inspection uses policy `committed_text_c4_v2` and retains both the selected declaration and complete `verification_v2` input contract: source metadata, protected test/helper/manifest/config/lock rows, protected prefixes, selected test files, source root, `foundry-test-v2` harness, dependency/admission bindings and protected/execution digests. TS additionally binds config digest, all sorted `.ts` inputs including declarations and fixed compiled-output root. Entire test/support directories and reserved dependency/build prefixes are protected; root selection does not grant writable scope. Whole-source bounds remain 500 files/10 MiB and model context 30 files/200 KiB. The complete inner contract has an independent 1 MiB bound inside the 2 MiB inspection record. Dependency payload is independently bounded to 10,000 files/64 MiB.

Blocked inspection has no completed contract, successful counts or creation authority. Inspection counts mean **test files**, not passing/completed framework cases. Changing language/runtime/dependencies/layout/support/config/scope invalidates the selected inspection, including stale async replies. Inspection history remains bounded and immutable; failed actions never retry automatically.

`POST /api/coding-tasks` uses the exact inspection ID/digest, original unexpanded declaration and only `spec.c4:{schema_version:2,id:<selected profile>,mode:"embedded_vm"}`. The backend reconstructs the full profile and protected rules from immutable records; client-supplied full execution bindings are refused. Import/coding/control/operation versions remain 1; v1 JSON/hash/report behavior stays unchanged. Creation grants no readiness, baseline, context, model call, execution or delivery consent. Admission does not reconstruct a live runtime session. Current catalog eligibility is not used to rewrite historical tasks.

All routes retain session/Host/Origin/JSON and recovery read-only guards; requests use the existing 1 MiB body limit. V2 nested records reject unknown, duplicate/aliased, missing and null fields. Dependency errors remain the existing bounded envelope: 400 for consent/invalid inputs/location, 409 for changed committed source and 500 for storage failure, without raw host paths/parser detail. This reference supplies no alternate execution route.

## Historical C3 scope and invariant boundaries

C3 adds explicit local repository admission, durable import provenance and
read-only recovery diagnosis to the existing workbench. It does not replace the
engine or its context, execution and delivery approvals. Subscription work stays
paused. Acceptance uses disposable Git repositories, synthetic model gateways and
content-check runners only. No real accounts, Keychain, provider requests, Docker
inspection/execution, external MCP, refresh, automatic retry, paid fallback,
generic script execution or source-repository writes are authorized.

Registration is metadata only. A user explicitly requests a bounded local
inspection before any source read. Inspection does not authorize model context,
planning, execution or delivery. Source files, Git configuration and repository
instructions remain untrusted data. A diagnostic result is information, never an
approval or an instruction to replay an operation.

## A. Explicit admission inspection

Reuse the registered Git repository identity and its revision, or accept an
explicit absolute local path. Registered and direct-path inputs must be
unambiguous. Reject unsupported layouts and missing local commits/objects without
fetching, cloning, prompting for credentials or invoking repository helpers.
Use the trusted Git executable, bounded plumbing and allowlisted environment;
never add status/diff, checkout, hooks, filters or network-capable discovery.

The snapshot policy stays at 500 committed regular text files / 10 MiB in total,
with at most 30 files / 200 KiB in the selected model context. Existing sensitive
path, protected input, binary, symlink, submodule, collision and command-grammar
rules remain. A narrower context does not make an oversized or unsupported whole
tree importable. Uncommitted and untracked files are excluded. Working-tree
cleanliness is explicitly `not_checked`, never inferred from HEAD inspection.

Admission and managed-tree validation also share a shape bound: at most 4096
entries including the root, files and directories, and depth 64 (root depth zero).
Admission counts implicit parent directories from committed paths and reports
`tree_limit` before claiming readiness. Managed-tree reads use incremental batches
of at most 64 directory entries, including empty directories in their bound.
Unusually deep or directory-heavy trees previously accepted may now be blocked;
ordinary file/mode/digest and delivery-report bytes are unchanged. These checks do
not delete, repair or reseal rejected trees.

Admission freezes a version-one inspection record with a controller-owned ID,
resolved root, optional repository ID/revision, exact commit, import specification
(scope, protected rules, acceptance commands and requested image), measured counts,
context manifest, concrete protected inputs, policy version, check time and digest.
No file contents, raw Git errors or secrets enter the inspection response/log.
Failed inspections carry typed safe stage/code, bounded explanatory facts and
explicit unknown counts; a partial check cannot advertise an importable result.
Snapshot preparation and inspection share the same traversal/validation logic.

The requested image is configuration only: C3 must not resolve an image or contact
Docker. Unsupported paths/errors are safely projected; this is not general secret
detection. Each user-triggered inspection is bounded and single-attempt; no scan
on every keystroke, automatic polling or retry after failure.

## B. Frozen source and durable import

New ordinary workbench tasks use `import_version: 1` and an exact successful
inspection selection. Version zero retains the historical creation/authority/
report contract, including specialized modes. Unknown versions and unsupported
combinations fail closed. A version-one request without its selection is invalid.
The version and provenance are immutable and included in the task authority so
removing them cannot downgrade a stored task to the historical contract.

Creation rechecks repository registration, canonical root, HEAD and import spec,
then prepares the exact inspected commit. A changed input requires a fresh
inspection; never silently import the new HEAD. On success, bind a verified
snapshot digest, inspection identity/digest and committed import operation to the
new task. Project association remains organizational metadata, not authority.

An import operation is recorded before workspace writes and reserves a mission
identity. Prepare under a private controller-owned staging directory outside the
source. Validate/sync the complete base and initial immutable generation, publish
without overwriting an existing destination, then atomically commit mission plus
import selection in SQLite. A failed database commit must not make published
files executable. Storage or publication failures retain a bounded typed record;
if even the journal cannot be updated, its durable pre-operation record remains
unfinished rather than falsely successful.

An inspection selection is consumed by one explicit creation intent. Duplicate
requests cannot create multiple missions: the exact completed intent returns its
recorded result; changed input, unfinished or unknown outcome stops without
replaying filesystem work. After failure a new inspection/creation is an explicit
user action. Do not adopt an orphan, automatically clean staging, resume a failed
import, refund model budgets, or replace an existing mission. Interrupted imports
are diagnosable even when no mission was created. Preserve artifacts for diagnosis.

Existing tasks/exports are not migrated or resealed. Newly bound source facts
appear in task and delivery provenance without changing historical report bytes.
Before planning, execution and delivery, validate the new binding against the
authoritative committed import record and frozen on-disk base, not against the
current live source HEAD. Later changes to the original repository do not mutate
or invalidate an already-frozen task; any future patch application is separate.

## C. Read-only recovery diagnosis

The endpoint reads the current mission, import/local-operation records and managed
files through bounded validators. It never calls Engine.Recover, runner.Resolve,
runner.Run or runner.Reconcile, and never contacts Git, providers, secrets, MCP or
Docker. It does not mutate mission state, epochs, approvals, candidate selection,
operation status, budget, seals or activity history. A bounded request performs one
check; GET is not a repair command. Existing startup reconciliation remains separate.

Report each dimension independently: recorded candidate, candidate integrity,
frozen base/authority, import provenance, execution trace, verification/review
bindings, exports and latest operation outcome. Use fixed statuses such as
verified, blocked, unknown, not_applicable and unsupported; absence is not success.
Include task revision/epoch, checked time, relevant record IDs, bounded safe codes
and next-action identifiers. Re-read the mission revision after checking; a changed
revision returns stale/no action eligibility. All actions still revalidate under
their original server-side guards, even after a fresh-looking diagnosis.

- Intact candidate: show facts, do not continue automatically.
- Interrupted/unknown attempt: preserve uncertainty, show the existing explicit
  replan action only when its original guards permit it; require fresh execution
  approval afterward. Never promote a prepared result to selected or successful.
- Interrupted export: direct the user back to the existing delivery decision;
  fresh explicit invocation and exact material checks are required.
- Corrupt base/candidate/approval/evidence: block execution/delivery, preserve data
  and explain that this diagnosis cannot repair it or reuse invalid authority.
- Unsupported or unreadable format: show bounded read-only facts; never reconstruct
  missing records from UI state or parse error strings as authority.

The existing global diagnostic mode remains fail-closed. In that mode new
diagnostic GETs preserve host/origin/session checks and cannot enable mutations.
Do not turn task-level diagnosis into an alternative global recovery path.

## Workbench integration

Keep navigation, layout and independent approval panels. Add admission inspection
and confirmation to the ordinary recoverable creation form, a repository entry in
existing project details reusing the Git registry, and source facts in task views.
Registration/editing remains in existing settings. The ordinary form defaults to
the new import flow; legacy/specialized behavior remains explicitly distinguishable.
Changing inputs invalidates the displayed selection immediately; late requests
cannot restore stale readiness. Failed requests retain history but not eligibility.

Enhance the current candidate/local-operation section with a manual diagnosis
action and a concise result/checklist, not another dashboard. Show interrupted
imports in the source/creation area when no task exists. No one-click repair,
database edit, source rollback, arbitrary generation selection or cleanup buttons.
Historical provenance stays visibly absent, not backfilled as verified.

### Implemented interface and local fixture

The ordinary form uses `检查可导入性` for a single explicit inspection, then
`创建任务并检查上下文` for the separate creation confirmation. All import-bound
inputs invalidate readiness immediately, including changes away and back while a
request is pending. A refused creation also clears the selection; history is read
again without retrying the mutation. Specialized or legacy modes carry no C3
selection. Project details reuse Settings / Git registration metadata and prefill
the existing form; this does not automatically link a new task or grant permission.

Inspection and operation history GETs return bounded arrays. Inspection POSTs
return a versioned inspection record. Task diagnosis GET returns
`{ "diagnostic": ... }`, with ten dimensions: `candidate_record`, `candidate`,
`base_authority`, `import_provenance`, `coding_trace`, `invocations`,
`verification`, `reviews`, `export`, and `local_operation`. The manual
`核对恢复状态` control lives in Overview / candidate and local operations.
Changing task identity, revision or epoch makes its result stale and discards
late responses. Any suggested replan or delivery action points back to the
original decision panel; the result grants no authority.

`foundry-acceptance` supplies an explicitly synthetic C3 project, an imported
unplanned draft, a binary repository that is not inspected until requested,
a reserved import with no task, and an isolated evidence-corruption task.
The corruption is seeded only by the acceptance command after real synthetic
engine transitions; no production fault-injection endpoint exists. New-task
defaults use the local `synthetic-stub` for every role. Historical local protocol
scenarios retain their frozen selections, and the optional policy proposal stays
unconfirmed. Controller workspace data is in a separate `managed` sibling of
the disposable sources, preserving the source/data overlap guard.

The HTTP acceptance path covers exact import, context consent, initial execution
failure, explicit replan, fresh execution approval, corrected verification and
independent reviews, and separate delivery. Downloads are compared with independently
read export bytes, SHA-256 values and the export manifest; the report's C3 binding
is compared with the task binding. Diagnosis snapshots check that missions,
budgets, events, imports, local operations and invocations remain unchanged.
The manifest labels C4 readiness `not_checked`. Final clean-export browser QA,
security scans and exact-commit CI remain separate publication gates.

## Acceptance and handoff to C4

Use real disposable Git, SQLite, workspace and HTTP with synthetic model/runner
boundaries. Test inspection consent, missing/foreign/stale receipts, registration
and HEAD changes, input changes, unsafe layout/config/paths, limits, missing objects,
partial preparation, publication/transaction failure, duplicate requests, crash
and restart without replay, tampered provenance and read-only diagnostic purity.
Test concurrent mutation, old reports, specialized modes and global read-only mode.
Browser acceptance covers import success, an explained admission block, interrupted
operation and corrupted evidence, plus independent C2 approvals and exported bytes.
Run Go/race/vet, frontend tests/build, clean-source export, security scans and exact
commit CI. Private runtime evidence never enters the public source export.

C3 proves local admission and diagnosis, not real compiler or Docker readiness.
C4 requires separate confirmation of a small real project, exact commit, approved
scope/test inputs, already-present trusted image and image ID, verifier command and
resource policy. Start with a baseline run, then failed and corrected candidates;
models can remain synthetic. The existing candidate mount stays read-only; any
compiler cache/build output must use an explicitly bounded isolated scratch area.
No automatic image pull, network, dependency installation or source application is
implied. Unchecked Docker/project readiness must remain `not_checked` in C3.
