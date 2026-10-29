# C4 verification: v2 and historical Go preparation

## 0.2.0 fixed profiles

The current source extends model-backed verification with the following closed single-package VM profiles. The [scoped Mac native/provider results](alpha-status.md) are recorded, while exact-release platform gates remain mandatory; the original C4-A/B/Double design and evidence below retain their historical meaning. See [the input walkthrough](desktop-guide.md#candidate-020-language-and-dependency-inputs) and [schema-2 admission](repository-import-c3.md#candidate-020-explicit-dependency-input-and-schema-2-inspection).

| Profile | Runner | Verifier |
| --- | --- | --- |
| `c4_node_model_v2` | `node-test-v2` | `/opt/foundry/verify-node-v2` |
| `c4_typescript_model_v2` | `typescript-node-test-v2` | `/opt/foundry/verify-typescript-v2` |
| `c4_python_model_v2` | `python-unittest-v2` | `/opt/foundry/verify-python-v2` |

Each schema-2 task freezes the exact VM identity (`foundry-vm-v2`, `multilang-vm-v2`), canonical runtime-inventory digest, dependency/admission binding and protected/execution input digests. Historical Go v1 profile/identity/report bytes remain unchanged; newly built bundles have distinct immutable identities. V2 uses one shared installed image and never Docker, host commands, another language or an unbound current catalog as fallback. Installed eligibility does not prove a boot/self-check or completed tests.

Separate consent imports an existing pure-JS npm lock-v3 tree or exact locked pure wheels. Committed inspection protects entire tests/support, manifests, locks, TS config and dependency payload; no package manager/install script runs in the guest. TS uses the fixed typecheck then frozen compiled Node test phase, including actual dependency type declarations. Python uses isolated unittest. Runtime/source/dependency/compiled-output freeze, UID/NNP, FD/control separation, cgroups and exact shutdown must be established by native evidence. Models receive only a bounded dependency summary, never the dependency payload or local input path.

Existing task controls remain at `GET/POST /api/missions/{id}/c4`. Readiness is `prepare_readiness` → `approve_control` → `execute_readiness`; baseline is `prepare` → `approve` → `execute`; exact cleanup is `prepare_cleanup` → `approve_control` → `execute_cleanup`. Every action binds current revisions and opaque material/operation/control references returned by the server. Readiness receives only the selected runtime target, never project/dependency inputs, and proves no project pass. Context, plan-execution and delivery approvals remain independent.

Completed case counts come only from complete authenticated framework results, never static test-file counts. Compiler, setup/discovery, truncated/zero-case or resource failures remain infrastructure/timeout evidence and cannot trigger Debugger. A genuine completed assertion failure with confirmed cleanup may enter at most two repairs. Profile/baseline/current candidate and independent QA/Auditor evidence still bind delivery. Unknown outcomes stop without retry, refund or provider switch.

Native acceptance has an explicit closed profile/case selection and strict schema-2 receipts; the unchanged v1 default uses schema-1 receipts. Typed authenticated v2 phase/check facts are available only after exact EOF, successful native completion, confirmed termination and successful Close for the same owned request. Defensive copies and disk receipts grant no execution authority. The [installed twelve-case matrix](release-readiness.md#candidate-020-gates) uses real guest tests and explicitly fixture model replies. All twelve cases passed on the scoped Mac component source; Windows distribution requires its own actual native CI result and final release receipts.

## Historical C4-A/B Go preparation

Status: C4-A/B implemented with local synthetic validation. A separately approved,
bounded C4-C acceptance subsequently ran the disposable Double project's tests in
its approved local Docker image: baseline passed, initial candidate failed,
repaired candidate passed, and exact-owned cleanup was confirmed. Roles remained
local simulations; original evidence and sealed exports are retained unchanged.
This establishes no general-repository support or future execution authority. Whole-build,
browser and hosted CI evidence must always be tied to the tested source revision.

## Scope and non-goals

Preserve the existing workbench navigation, C3 repository admission, context consent,
plan revision, execute approval and delivery approval. Add a versioned C4 execution
profile and an independently approved baseline in the current task decision area.
Only the disposable local acceptance runtime may use the synthetic C4 driver.
Production defaults closed. The strict optional startup file and independent
version-1 readiness/cleanup controls are documented in
[C4 controlled activation](c4-controlled-activation.md); there is no environment
enable switch, HTTP override or automatic fallback. A C4 task must never fall through to the legacy Docker runner or a real
model route. Subscription integration stays paused.

This increment does not select a user's project, contact a Docker daemon, inspect,
build or pull an image, execute a real container, access credentials, apply changes
to an original repository, deploy, or authorize arbitrary scripts/Git writes.
C4-C requires separate confirmation of project, commit, scope, protected tests,
image provenance/digest/platform, local endpoint and real execution.

The disposable acceptance launcher has an explicit `--c4-controls-fixture` mode
with a separate synthetic-only runtime/store and two shared-base scenarios:
readiness pending and simulated cleanup failure. Prepare, approve and execute
remain separate workbench actions; readiness does not approve baseline/candidate
execution. Default legacy fixtures are unchanged. These are mock validations;
this fixture performs no real C4-C execution. The separate
[repository-task mode](repository-task-orchestration.md) is descriptor-driven,
accountless local simulation only; it does not relax the real Double boundary.

## Versioned contract

New tasks opt into an immutable `c4_go_v1` profile (schema version 1).
Existing tasks omit the new fields; their authority hashes, report bytes and source
labels retain their old meaning. Unsupported versions and corrupt/partial C4
records are readable only and fail closed for mutation, dispatch and delivery.

The profile freezes mode, image content ID, linux/arm64 platform, absolute trusted
Docker executable and a local Unix socket endpoint, verifier identity
`/opt/foundry/verify-go-v1`, command/arguments, controlled environment and resources.
Synthetic declarations are explicitly fictional; they are not local readiness.
No mutable image tag, inherited Docker context, remote TCP/SSH endpoint, extra
mount, client-provided command/environment or resource override can authorize a run.

Fixed resource policy: one CPU, 1 GiB memory, 128 processes, 120-second execution
deadline, combined output limit 64 KiB. Candidate and root filesystem are read-only.
Only two tmpfs mounts are writable: /tmp (64 MiB, noexec,nosuid,nodev) and
/foundry-build (256 MiB, exec,nosuid,nodev, bounded inode count). Executable scratch
is a new, explicit C4 permission and does not relax the legacy profile.
Use UID/GID 65534, all capabilities dropped, no-new-privileges, no network,
published ports, healthcheck, logging driver or automatic pull.
No host repository, home, credentials, .git or Docker socket may be mounted.

Go execution uses the image-owned verifier binary, not a repository shell script.
It invokes a fixed Go test command, validates actual test events and fails on zero
tests. Freeze tests, go.mod/go.sum, build/configuration inputs and verifier identity.
Use GOPROXY=off, GOSUMDB=off, GOTOOLCHAIN=local, GOENV=off, GOWORK=off,
CGO_ENABLED=0 and scratch-only compiler/cache paths. No dependency install,
go generate or shell expansion. The verifier contract fixes Go 1.27.3 on
linux/arm64 and `go test -json -count=1 -timeout=90s ./...` in `/workspace`.
An admitted image must supply `/usr/local/go/bin/go`; the verifier checks its
version before testing. Image/base provenance and actual compiler require
run-specific admission; this declaration does not assert current availability.

## Approvals and task flow

1. C3 import fixes source provenance, then existing context consent remains.
2. Prepare baseline material for the unmodified frozen base. Bind task/revision,
   epoch, base commit/digest, profile digest and verifier. Preparation is not consent.
3. Approve that material, then execute it once as a separate user action. A stale
   material or duplicate click cannot dispatch. Readiness and execution in A/B are
   synthetic and visibly labeled; no actual Docker readiness is inferred.
4. A successful, clean baseline allows normal planning. It is not candidate
   evidence, plan approval or delivery approval.
5. The existing execute approval binds plan, candidate generation, C4 profile and
   baseline evidence. Worker changes and subsequent verification use this contract.
   At most two existing, explicitly budgeted Debugger repair rounds remain. A
   changed candidate is required for another validation; no transport retry or
   same-candidate redispatch is authorized.
   Missing or mismatched copied approval facts cannot produce a candidate pass
   label or current delivery eligibility; historically valid, correctly bound
   candidate results remain readable after the current candidate changes.
6. Only a classified test failure permits Debugger. Infrastructure, OOM, timeout,
   unknown outcome, persistence failure or unconfirmed cleanup stops the task.
7. QA/Auditor remain local simulated model judgements. Delivery stays independently
   approved and exports a patch/report only, never writes back to the source.

## Durable execution and recovery

Use an append-only, bounded C4 operation trace within the mission's existing
transaction/revision mechanism, not a second approval database. Baseline records
and candidate records are distinct. Input/approval bindings and terminal facts are
immutable. Persist intent and dispatch consumption before driver effects. Persist
created/started/outcome/cleanup facts with revision and epoch fencing.

Each operation fixes an opaque container name, then the verified container ID and
task/operation/owner labels. Create/start/inspect/remove are separate driver stages.
Cleanup checks exact ownership and only targets that operation; never use broad
owner sweeps or prune. Inspect exit status and OOM before cleanup. Cleanup state
is separate from command outcome. Bounded logs are untrusted and redacted; record
truncation, never serialize raw host environment or account data.

A crash or uncertain database commit never causes redispatch, budget refund,
automatic repair or automatic resume. On restart mark unfinished attempts unknown
and stop. A recorded exact-owned cleanup action may be requested separately;
cleanup cannot replay execution and cannot manufacture a passing result.
C3 diagnostic reads remain read-only and never contact a runner.
Failure to persist a terminal fact retains the durable intent for diagnosis.

## Bounded executor and verifier boundary

The A/B synthetic executor accepts only an in-process synthetic-driver capability.
The profile's `mode` string alone is not authority. The separate
[default-closed Docker adapter](c4-docker-adapter.md) requires strict admitted
startup configuration and independent readiness/execution approvals as described
in [controlled activation](c4-controlled-activation.md). HTTP/UI cannot enable it.
Its automated tests use synthetic process/filesystem fixtures and an owned Go
helper, never Docker. The legacy runner is not a fallback.

Every planned Docker command binds the trusted absolute executable and explicit
local Unix endpoint, with no inherited environment. The managed candidate must
be a canonical descendant directory without symlinks, `.git`, or special files.
Start acknowledgment and waiting are separate: a driver must confirm an
exact-owned container start before the executor persists the started fact and
waits. Dispatch, created ID, outcome and cleanup are separate durable mutations.
A failed or uncertain persistence callback stops all subsequent effects.
Deadline or cancellation before Start acknowledgment leaves execution
`outcome_unknown`, with a bounded static cause and no invented Started fact.
After confirmed start, Wait deadline/cancellation can classify as
`timeout`/`interrupted`. Both paths stop execution; exact-owned cleanup may still
run with its own bounded context, but neither permits a retry or Debugger repair.

Because logging is disabled, the plan uses `start --attach`, not Docker logs.
The driver must continuously drain attached output into a bounded buffer,
preserve truncation metadata, and expose that buffer through the typed logs stage
without launching another command. It must validate the image-owned verifier
summary independently of untrusted output strings. The verifier treats Go JSON
events as data, requires a completed passing test and successful package outcomes,
and rejects malformed, oversized, zero-test, skipped-only and nonzero-status
false success. Output retention is bounded to 64 KiB combined and common
credential patterns/control characters are redacted before persistence.

These are contracts, not a universal isolation guarantee. Each real run must validate
executable/socket trust, image provenance and content ID, actual platform and
toolchain, image configuration (including rejecting declared volumes and unsafe
environment defaults), effective container mounts/resources/network, immutable
candidate handoff, attach/start acknowledgment and verifier-summary transport.
No such readiness probe, image inspection, image build or container run occurs
in the local synthetic modes. The historical bounded Double acceptance is not
proof of any other image, host or repository. Explicit recovery inspects/removes only a separately known exact-owned
ID; it cannot discover by name, replay a command or promote an unknown result.

## Implementation seams

- Domain: optional C4 profile and operation records, strict validation, digest and
  transition rules. Add store transaction validation using the existing mission CAS.
- Sandbox: a fixed C4 command plan and shared durable lifecycle executor, with
  distinct synthetic and dormant Docker wrappers. The default-off adapter uses
  bounded stdlib subprocess transport, retained filesystem trust and strict
  effective-config/verifier framing checks. Real activation remains separately
  gated; the historical bounded acceptance does not renew it. The old sandbox is unchanged for legacy tasks.
- Engine: guarded C4 creation, baseline prepare/approve/execute, exact-owned cleanup,
  planning/approval/verification/recovery routing; no legacy-runner fallback.
- HTTP: task-scoped operations behind existing Host/Origin/session/JSON/read-only
  protections, strict bodies and revision/material binding.
- UI: current decision panel, verification area and activity stream, with baseline,
  candidate, profile, lifecycle and cleanup presented as factual lists, not new
  navigation or decorative charts.
- Acceptance runtime: disposable C3-admitted small Go repository, synthetic models
  and fake Docker driver; pending baseline plus content-driven failure/repair and
  explicit failure fixtures. No account credential reads or external traffic.

## Verification gates and implementation sequence

C4-A: domain/profile/trace and immutable approval material; legacy compatibility
tests. C4-B: bounded driver lifecycle, engine/HTTP integration, local-only fixture
and UI. First failing tests, then implementation, task review and whole-change
review. Test default-off, tampering, stale/duplicate approvals, source/profile/image
drift, foreign ownership, zero tests, truncation, timeout/OOM, failed persistence,
crash, cleanup failure and restart without replay. Check baseline cannot become
delivery evidence and only test failures enter Debugger.

Run targeted and full Go/race/vet, frontend tests/build, browser acceptance of real
local mock state, clean-source export and secrets/vulnerability/workflow scans.
Push only reviewed public source/docs after gates, inspect exact-commit CI, then
start the latest disposable acceptance workbench. No Release.

The historical bounded Double C4-C acceptance does not generalize to other
projects, images or hosts. Every future real run still requires project admission,
image provenance, local readiness and independent execution approval. Synthetic
passes do not prove compiler execution, host isolation or resource enforcement.

## Disposable workbench acceptance

Build the existing frontend, then start
`go run ./cmd/foundry-acceptance --web-dir web/dist`. This command always
allocates new disposable data and a random loopback URL; the default mode has no
user-project option and no acceptance mode enables real C4 execution. The separate
repository-task mode supports committed-source simulation as documented above.
The default mode's printed manifest includes
`c4_readiness: not_checked` and these task IDs:

- `c4_baseline_pending`: C3-admitted stdlib-only Go fixture, fixed protected
  tests/module/configuration, exactly seven frozen accountless synthetic roles,
  and prepared but **unapproved** baseline material. Nothing executes on viewing
  the task. In the existing decision area, approve the material, then separately
  execute the simulated baseline. Next authorize context, inspect the proposed
  plan, and explicitly approve execution. Worker proposes `n + 2`; the fake
  driver records a content-derived `test_failed`, and Debugger changes it to
  `n * 2`. The original baseline is `n + n`, so the final patch is meaningful.
  QA/Auditor produce C2 contract-bound simulated reviews. Delivery still needs
  its own approval and only exports patch/report artifacts.
- `c4_cleanup_failure`: separate disposable repository with an intentionally
  simulated `infrastructure_failed` baseline and failed first cleanup. The
  decision area permits a separately requested, exact-owned cleanup. Cleanup
  can become confirmed but cannot change the failure, re-run verification,
  authorize Debugger, or enable delivery. This fixture is deliberately seeded
  through actual engine approvals/execution; it is not a pending user decision
  or an observed Docker failure.

The driver compares the exact candidate file contents and frozen test fixture;
it does **not** run the Go compiler, tests, shell or Docker. All candidate
verification evidence is `source_version: 1`, `source_kind: synthetic_stub`.
Simulated lifecycle facts and model judgements are not actual container facts,
real command evidence, image provenance, or readiness. Models use a dedicated
in-process gateway with no router, account, credential interface or fallback.
The fixture restores live settings after freezing its roles, preserving the
other acceptance scenarios.

The current workbench navigation is unchanged. The decision area displays the
immutable profile/resources and executable scratch permission. The verification
view separates baseline from candidate records and shows operation/material,
generation, attempt/repair, stage, result, truncation and cleanup facts. Persisted
`c4.*` events appear in the existing activity stream. Missing/unsupported facts,
stale reads and unconfirmed cleanup never grant UI authority. Failed actions only
refresh GET facts; they are not retried. Real C4 remains visibly closed without a
toggle. Backend revision, material and delivery checks remain authoritative.

While baseline material is pending, context/plan and Main chat do not invite
intervening mutations. Approved material whose approval revision is no longer
immediate is visibly stopped. The UI never parses opaque operation IDs; a stale
prepared material is authoritatively rejected by the backend. Failed mutations
are inhibited for that material and only GET facts refresh. A new durable cleanup
revision permits a fresh **explicit cleanup-only** action. There is no reprepare
or same-material execution retry: diagnose the stopped task, or create a fresh
disposable task/runtime for a new acceptance flow.
