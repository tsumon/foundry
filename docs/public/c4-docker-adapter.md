# C4 Docker adapter (default closed)

Status: dormant transport implemented and validated with synthetic fixtures and
an owned Go-test helper subprocess only, not C4-C real acceptance. No Docker
executable, endpoint, image or container was contacted. Project selection, image
provenance, Docker readiness checks and real execution require separate user confirmation.
Subscription integration remains paused.

## Scope and alternatives

Keep the C4 lifecycle, durable operation journal, workbench and independent
baseline/context/execute/delivery approvals. A fixed CLI adapter is smaller than
introducing a Docker SDK and reuses the already reviewed command plan. The legacy
Docker runner is not a fallback. A generic command runner or environment-driven
enable switch would widen authority and is excluded.

The transport is an internal library with a zero-value-disabled, trusted
configuration. The optional strict production startup configuration and separate
readiness/cleanup approvals are specified in
[C4 controlled activation](c4-controlled-activation.md). HTTP request fields,
environment variables and the workbench cannot enable or configure the adapter. Construction
of disabled configuration must perform no filesystem trust probes, Docker command,
socket connection or image inspection. Domain `mode: docker` alone is not authority.
The ordinary production C4 engine gate remains closed without explicit admitted
startup configuration. This integration has mock validation only. No apparent readiness
is inferred from implementing or mock-testing a transport.

## Frozen inputs

Use `c4_go_v1`, schema 1, linux/arm64, a content-addressed local image ID and
`/opt/foundry/verify-go-v1`. Keep the policy from `coding-verification-c4.md`:
1 CPU, 1 GiB memory and memory-swap, 128 PIDs, 120-second execution deadline,
64 KiB retained combined output, UID/GID 65534, no network/ports/capabilities,
no-new-privileges, no healthcheck, no logging driver, no pull. Root and candidate
are read-only. The only writable paths are /tmp (64 MiB, noexec,nosuid,nodev)
and /foundry-build (256 MiB, exec,nosuid,nodev); both have 16384 inodes.
No host source, home, credentials, .git or Docker socket mount is permitted.

The trusted executable is an absolute, canonical, executable regular file, not
a PATH lookup or symlink. The explicit endpoint is a canonical local Unix socket,
not a context or TCP/SSH URI. Before an enabled process call, verify executable/socket
identity and safe ownership/modes; recheck their identity before effects. These
checks do not establish image provenance or defend against a compromised host or
Docker daemon. The checker rejects symlinks and group/world-writable parent
directories and retains executable, socket and parent device/inode/owner/mode
identity. Socket group write is permitted; world write is not. These checks do
not close filesystem check/use races or protect against a malicious same-user
process. Never include inherited environment or raw OS/CLI error text in
durable records. Invocation environment is the frozen command-plan environment.

The adapter is bound to one `C4Plan`, not an arbitrary `C4Command`. It compares
each stage's command against the plan, enforces lifecycle order, rejects reuse,
and only accepts the exact created/explicitly recovered full container ID. Inputs
are copied at construction so later mutation cannot widen authority.

## Readiness and configuration validation

Readiness is not checked on construction or page load. Version-1 controls require
separate prepare, approve and execute actions before the bounded readiness check.
After explicit execution authorization, create-stage preparation may inspect only the exact approved
image. Missing image, wrong ID/platform, declared volumes, nonempty default
command or unsupported environment/configuration stops before container creation;
there is no pull/build fallback. Allow only explicitly documented benign image
environment defaults and fixed policy values; reject loader/compiler overrides.

Decode bounded inspect JSON and require the fields used for security decisions.
Unknown unrelated Docker metadata is tolerated; missing, duplicate, malformed or
conflicting security fields are not. Image inspection is not proof that the
compiler/verifier exists; actual verification remains a later acceptance gate.

Image inspection uses one fixed CLI Go-template projection to render every policy
field, including empty `Cmd` and `Volumes` that ordinary Docker JSON may omit.
Values are JSON-escaped by the CLI; nonempty unsafe values are never replaced.
The fixed template uses map indexing for these two optional fields, including
under `missingkey=error`. Typed CLI input selects its buffered raw-JSON rendering
path within the same inspect call; this is not another Docker request or retry.
All existing metadata and case aliases are preserved for the unchanged bounded
parser. Missing required fields, unsafe values, conflicting aliases and malformed
types still fail closed. Unsupported rendering stops without a transport retry.
Both readiness and create use the same projection. Offline regressions exercise
typed-to-raw rendering, omitted/null/empty fields and unsafe/malformed variants.

An installed CLI beneath a group-writable ancestor remains untrusted. Local
acceptance may use a byte-for-byte, SHA-256-verified copy of the previously verified
CLI in a private directory, with non-writable group/other ancestors and executable
mode `0500`. System-directory permissions must not be changed. The existing
transport trust and replacement checks still apply to that copy and the socket.

After creation and before start, and on subsequent inspections, validate full
ID/name/owner/task/operation labels, image, entrypoint/command/user/workdir/env,
read-only mount and allowed tmpfs, network/ports, privileged/capability/security
settings, resource limits, logging/healthcheck/restart/namespace policy. A command
line alone cannot prove these effective settings. No foreign or ambiguous object
may be started or removed. Cleanup validates ownership separately from result;
changed/unsupported security settings fail closed rather than authorizing deletion.

## Process, start and verifier transport

The production process boundary uses `exec.CommandContext` with the exact absolute
path/argv/environment, no shell, no stdin and bounded pipe wait. Control responses
are size-limited while stdout/stderr continue draining. Context cancellation kills
only the owned CLI child; it never implies that the container stopped. Container
cleanup is separately bounded and ownership checked.

Use `container start --attach EXACT_ID`, not `docker logs`. For this dormant
version Start waits for the attached CLI to finish while continuously draining
separate bounded stdout/stderr, then performs one exact-ID inspection to establish
a nonzero StartedAt. This deliberately conservative acknowledgment may be persisted
after the container exits; it is not a real-time started indicator. A crash or
deadline before acknowledgment remains outcome_unknown. No inspection polling,
repeated start, transport retry or invented Started fact is permitted.

Nonzero attached CLI exit is not automatically a transport failure: a verifier
test failure exits nonzero. Docker wait prints a container exit status while the
CLI command itself may succeed. Parse one numeric wait result and reconcile it
with inspect exit status, attach status and the verifier envelope. Command
transport errors, malformed/multiple/truncated responses, mismatches and OOM
cannot be promoted to test success or repair eligibility.

The image-owned verifier captures Go output internally and emits exactly one
JSON `c4verify.Result` envelope on stdout. Decode that envelope, not text within
its Output field or stderr. Require every field, reject duplicate/unknown envelope
keys and trailing data, bound decoded output to 64 KiB, and validate class/test
counts and verifier process exit mapping (passed=0, test_failed=1, other=2).
`tests` counts matched, completed Go test `pass` **or** `fail` events, not only
passing tests; skipped tests and package-level events do not increment it. One
test containing four input cases is still one test. Failure classification also
requires a complete event stream: no invalid/trailing partial JSON, unfinished
tests, or packages without terminal events. A positive count from an incomplete
stream is not repair-eligible evidence. Zero-test, skip-only and build-only
failures remain infrastructure failures, not successful verification.

The controller's positive-test-count, readiness binding, dispatch/start and
confirmed-cleanup requirements for `isolated_execution` remain unchanged.
Historical zero-count failure envelopes remain readable but are not promoted or
rewritten; new parser accounting does not grant retroactive provenance. Local
process fixtures exercise this classification without running Docker. Images
built from older verifier source must not be assumed to contain this fix; a new
verifier build and its exact image ID still require separate approval.

Use a bounded wire buffer large enough for JSON escaping of 64 KiB, distinct from
the retained human-output limit. Partial or oversized metadata cannot mean pass.
Output and errors are untrusted and must be redacted before durable storage.
Malformed or truncated verifier wire is replaced with a static unavailable
diagnostic rather than retained as raw JSON; only valid decoded envelope output
and bounded sanitized stderr enter the human-output capture.

This framing prevents ordinary test stdout from masquerading as metadata. It is
not cryptographic isolation from hostile processes sharing the verifier's UID
inside the container. The first real acceptance must use a reviewed disposable
repository, not arbitrary malicious code; stronger in-container adversary isolation
is outside this increment and must not be claimed.

## Lifecycle and recovery

Reuse the same execution core for synthetic and dormant Docker wrappers. Preserve
dispatch-before-effects and all durable CAS/epoch fences. The Docker wrapper checks
the disabled gate, profile, approved operation and frozen plan before any effects;
it never wraps the real driver in a synthetic capability. Synthetic APIs and
historical bytes retain their meaning. No mock result is persisted as real evidence.

`ExecuteC4Docker` and `CleanupC4Docker` accept a zero-value-disabled
`C4DockerConfig`; there is no application enable switch. Disabled or structurally
invalid/unapproved calls return a static error before filesystem validation or
persistence. An otherwise valid execution performs the existing read-only managed
candidate preflight before dispatch CAS. Current mission epoch, material and
candidate execute-approval authority remain the persister's responsibility; an
operation alone cannot prove that current mission authority. Failed dispatch CAS
stops before executable/socket trust probes or any process call. Trust checks are
lazy after durable dispatch, not construction-time readiness checks. Explicit
cleanup requires a previously durable terminal operation, its original admitted
candidate path and the known full ID; it reuses the existing cleanup lifecycle.
Unlike execution's dispatch CAS, recovery requires the trusted caller to authorize
the current mission/epoch/material/known ID under existing mission serialization
**before** calling the wrapper. The cleanup persister records the outcome after
removal; it does not provide a pre-removal current-authority check. Future startup
integration must wire this caller gate. Local malformed/unapproved/foreign-ID
rejection and a fresh exact-owned effective-config inspection are still mandatory;
a stale post-removal CAS cannot be claimed to prevent removal effects.

Only a classified test_failed result with confirmed cleanup permits the existing
budgeted Debugger path. Infrastructure, unknown result, persistence failure, OOM,
timeout and cleanup uncertainty stop. Any persistence failure stops subsequent
driver effects; do not add cleanup after an uncertain write. A separately requested
cleanup wrapper can inspect/remove only an explicitly known exact-owned ID; it
cannot create, start, wait, discover names, retry a failed run or turn an unknown
result into a passing one. A successful remove is checked against the exact ID;
unconfirmed removal remains failed/unknown.

No resume-on-startup, same-candidate redispatch, budget refund, source-repository
application, Git write or subscription/model fallback is introduced. The existing
engine remains synthetic-only, including the workbench acceptance fixture.

## Verification and publication

Tests inject the lowest process/trust boundary and exercise the actual parser,
policy checker, driver and lifecycle core. Process plumbing may use the Go test
binary as a disposable helper; it must never discover or invoke installed Docker.
Cover disabled/invalid approval zero effects; argv/env tampering; foreign ownership;
image/config drift; malformed/duplicate/oversized JSON; output spoofing; nonzero
test exits; wait/inspect disagreement; cancellation/timeout/OOM; cleanup failure;
persistence failure; no replay; bounded buffers and subprocess reaping.

Inspection fixtures are hand-authored synthetic JSON, not daemon observations.
Required fields and accepted normalization are deliberately conservative: omitted
security fields, unsupported Docker schema variants, and changed effective
configuration fail closed. Matching fixtures does not establish compatibility
with an installed Docker version, image provenance, verifier/compiler presence,
daemon readiness or runtime isolation. Those require separately authorized real
acceptance and review; there is no schema fallback or retry.

Preserve existing UI and verify baseline approval, execute approval, failed candidate,
repair and separate delivery using the local synthetic workbench. It must still
show real C4 disabled and synthetic evidence. Run targeted TDD, Go/race/vet, frontend
tests/build, clean export, secret/dependency/workflow scans and exact-commit CI.
Only reviewed public source/docs are pushed; private plans and runtime evidence
remain local. No Release, real readiness check or real Docker acceptance occurs.

## Readiness failure diagnostics (offline verified)

Failed readiness records use the existing `failure_code` field. New diagnostics
are a closed stage/reason pair: `version`, `info` or `image`, followed by
`trust_failed`, `process_failed`, `command_failed`, `output_limit`,
`protocol_failed`, `timeout` or `canceled`. Preflight rejection is
`readiness_gate_failed`. Unknown error data still becomes `failed`.

No command output, stderr, raw OS error, path, environment value, or daemon
identity is included. A command failure does not assert why the CLI exited;
protocol failure does not imply the daemon was unreachable. The existing
workbench facts display the code without adding navigation or action buttons.
These codes never grant readiness or execution authority, and each dispatch is
still consumed once. Old generic results remain unchanged; a historical phase
must not be invented or backfilled. Diagnostic tests use only local fixtures.

The image projection compatibility change remains distinct from these diagnostic
codes. A typed fixture alone cannot establish strict raw-JSON rendering behavior;
both paths are exercised offline. This public specification does not claim real
acceptance, and neither parser missing-field checks nor image constraints relax.

## Official CLI references

- [Start and attach](https://docs.docker.com/reference/cli/docker/container/start/)
- [Wait and printed exit codes](https://docs.docker.com/reference/cli/docker/container/wait/)
- [Container inspect](https://docs.docker.com/reference/cli/docker/container/inspect/)
- [Image inspect](https://docs.docker.com/reference/cli/docker/image/inspect/)

## Container inspection compatibility and pre-start diagnostics

The trusted Docker CLI boundary normalizes only omitted `Config.ExposedPorts`
and `HostConfig.Sysctls` to null. Lossless bounded parsing precedes normalization;
duplicate keys and case aliases remain rejected. Existing nonempty or malformed
values are never replaced. The strict security parser and all ownership,
mount, network, resource, executable and approval checks remain unchanged.

Pre-start failures retain a fixed phase/reason in the existing result output:
image inspection, creation, or first container inspection. A successful create
followed by failed inspection retains the observed full ID as explicitly
unverified diagnostic data. It does not populate the verified `ContainerID` or
`Created` fact, grant cleanup authority, start a container or retry. Raw CLI
output is never copied into diagnostics. Uncertain result persistence stops all
further effects; a crash before that result commit still requires independent
read-only investigation. Historical unknown results are not rewritten.

The CLI boundary also accepts an explicitly present `HostConfig.OomKillDisable:
null` as unset/default (false), not as disabling the OOM killer. Moby defaults
this pointer to false and may reset it to nil for an unsupported kernel
capability; Docker documents that the flag is discarded under cgroup v2.
Missing fields, true, wrong types, duplicates and case aliases remain rejected.
This is field-specific transport normalization, not a general null fallback;
the strict parser and all memory/resource checks remain unchanged.
See [Moby's implementation](https://github.com/moby/moby/blob/master/daemon/daemon_unix.go)
and [Docker's cgroup documentation](https://docs.docker.com/engine/containers/runmetrics/).

Start failures now retain fixed `start_precheck`, `start_attach` or
`start_postcheck` diagnostics (generic drivers use `start_ack`). Cleanup failures
persist an immutable failed record with optional closed-enum `phase` and `reason`:
`cleanup_precheck`, `cleanup_remove_precheck` or `cleanup_remove`. Reasons use the
same bounded failure vocabulary as readiness. Raw CLI/OS data never enters these
fields. A failed precheck causes zero removals and is never cleanup confirmation
or ownership authority; failure to persist the diagnostic stops further effects.
Legacy records remain valid and are not retroactively annotated.

## Exported report provenance

New C4 reports describe agent configuration separately from command evidence.
Local fixture agents are labeled as simulated even when their candidate tests
ran in Docker. Non-synthetic configuration does not prove a real model call.
Each baseline/candidate operation uses the existing execution-provenance rules:
Docker mode alone, success text, incomplete lifecycle records or a missing
trusted readiness receipt cannot establish real execution. Failed tests may
still be real execution; source and outcome are independent. Baseline evidence
never replaces candidate verification or a delivery approval.

Historical sealed reports retain their exact bytes, hashes and known renderer
format. Prepared-export recovery and download still check current evidence,
intent, approval, artifact binding and hashes; the compatibility path does not
rewrite reports or accept arbitrary old text. A corrected review copy must be a
separate derived artifact, explicitly linked to its original report and mission,
not a replacement seal or a new execution/approval record.
