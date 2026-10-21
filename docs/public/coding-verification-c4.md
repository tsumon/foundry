# C4-A/B: approval-bound Go verification preparation

Status: C4-A/B implemented with local synthetic validation. C4-C real
project/image/Docker acceptance is not authorized or performed. Whole-build,
browser and hosted CI evidence must always be tied to the tested source revision.

## Scope and non-goals

Preserve the existing workbench navigation, C3 repository admission, context consent,
plan revision, execute approval and delivery approval. Add a versioned C4 execution
profile and an independently approved baseline in the current task decision area.
Only the disposable local acceptance runtime may use the synthetic C4 driver.
Production has no C4 enable flag, environment switch, HTTP override or automatic
fallback. A C4 task must never fall through to the legacy Docker runner or a real
model route. Subscription integration stays paused.

This increment does not select a user's project, contact a Docker daemon, inspect,
build or pull an image, execute a real container, access credentials, apply changes
to an original repository, deploy, or authorize arbitrary scripts/Git writes.
C4-C requires separate confirmation of project, commit, scope, protected tests,
image provenance/digest/platform, local endpoint and real execution.

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
The future image must supply `/usr/local/go/bin/go`; the verifier checks its
version before testing. Its image/base provenance and actual compiler must be
reviewed in C4-C; this declaration does not assert that an image is available.

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

The executor accepts only an in-process synthetic-driver capability in A/B.
The profile's `mode` string alone is not that capability. Production Docker mode
is rejected before persistence or driver calls. The deterministic command plan
is not a subprocess transport; the existing legacy runner is not a fallback.

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
A future driver must continuously drain attached output into a bounded buffer,
preserve truncation metadata, and expose that buffer through the typed logs stage
without launching another command. It must validate the image-owned verifier
summary independently of untrusted output strings. The verifier treats Go JSON
events as data, requires a completed passing test and successful package outcomes,
and rejects malformed, oversized, zero-test, skipped-only and nonzero-status
false success. Output retention is bounded to 64 KiB combined and common
credential patterns/control characters are redacted before persistence.

These are testable contracts, not observed isolation. C4-C still must validate
executable/socket trust, image provenance and content ID, actual platform and
toolchain, image configuration (including rejecting declared volumes and unsafe
environment defaults), effective container mounts/resources/network, immutable
candidate handoff, attach/start acknowledgment and verifier-summary transport.
No such readiness probe, image inspection, image build or container run occurs
in A/B. Explicit recovery inspects/removes only a separately known exact-owned
ID; it cannot discover by name, replay a command or promote an unknown result.

## Implementation seams

- Domain: optional C4 profile and operation records, strict validation, digest and
  transition rules. Add store transaction validation using the existing mission CAS.
- Sandbox: a fixed C4 command plan and lifecycle executor with an injected driver.
  This increment supplies synthetic/in-memory drivers only; no production C4
  subprocess transport. The command plan is reviewable but never dispatched to
  Docker here. The old sandbox is unchanged for legacy tasks.
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

C4-C remains not performed: actual project admission, image provenance/preload,
local Docker endpoint readiness, compiler execution, host isolation and resource
enforcement require separate approval and real acceptance. Synthetic passes do
not prove these properties.

## Disposable workbench acceptance

Build the existing frontend, then start
`go run ./cmd/foundry-acceptance --web-dir web/dist`. This command always
allocates new disposable data and a random loopback URL; it has no user-project
or C4 real-execution option. Its printed manifest includes
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
