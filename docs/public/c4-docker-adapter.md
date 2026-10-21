# Dormant C4 Docker adapter

Status: implementation specification. This increment implements a dormant
transport, not C4-C real acceptance. Project selection, image provenance,
Docker readiness checks and real execution require separate user confirmation.
Subscription integration remains paused.

## Scope and alternatives

Keep the C4 lifecycle, durable operation journal, workbench and independent
baseline/context/execute/delivery approvals. A fixed CLI adapter is smaller than
introducing a Docker SDK and reuses the already reviewed command plan. The legacy
Docker runner is not a fallback. A generic command runner or environment-driven
enable switch would widen authority and is excluded.

The new transport is an internal library with a zero-value-disabled, trusted
in-process configuration. Neither production startup, HTTP request fields,
environment variables nor the workbench construct an enabled adapter. Construction
of disabled configuration must perform no filesystem trust probes, Docker command,
socket connection or image inspection. Domain `mode: docker` alone is not authority.
The ordinary production C4 engine gate remains closed; future activation requires
a separately reviewed startup/readiness/approval integration. No apparent readiness
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
not a context or TCP/SSH URI. Before an enabled dispatch, verify executable/socket
identity and safe ownership/modes; recheck their identity before effects. These
checks do not establish image provenance or defend against a compromised host or
Docker daemon. Never include inherited environment or raw OS/CLI error text in
durable records. Invocation environment is the frozen command-plan environment.

The adapter is bound to one `C4Plan`, not an arbitrary `C4Command`. It compares
each stage's command against the plan, enforces lifecycle order, rejects reuse,
and only accepts the exact created/explicitly recovered full container ID. Inputs
are copied at construction so later mutation cannot widen authority.

## Readiness and configuration validation

Readiness is not checked on construction or page load. After future explicit
authorization, create-stage preparation may inspect only the exact approved
image. Missing image, wrong ID/platform, declared volumes, nonempty default
command or unsupported environment/configuration stops before container creation;
there is no pull/build fallback. Allow only explicitly documented benign image
environment defaults and fixed policy values; reject loader/compiler overrides.

Decode bounded inspect JSON and require the fields used for security decisions.
Unknown unrelated Docker metadata is tolerated; missing, duplicate, malformed or
conflicting security fields are not. Image inspection is not proof that the
compiler/verifier exists; actual verification remains a later acceptance gate.

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
Use a bounded wire buffer large enough for JSON escaping of 64 KiB, distinct from
the retained human-output limit. Partial or oversized metadata cannot mean pass.
Output and errors are untrusted and must be redacted before durable storage.

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

Preserve existing UI and verify baseline approval, execute approval, failed candidate,
repair and separate delivery using the local synthetic workbench. It must still
show real C4 disabled and synthetic evidence. Run targeted TDD, Go/race/vet, frontend
tests/build, clean export, secret/dependency/workflow scans and exact-commit CI.
Only reviewed public source/docs are pushed; private plans and runtime evidence
remain local. No Release, real readiness check or real Docker acceptance occurs.

## Official CLI references

- [Start and attach](https://docs.docker.com/reference/cli/docker/container/start/)
- [Wait and printed exit codes](https://docs.docker.com/reference/cli/docker/container/wait/)
- [Container inspect](https://docs.docker.com/reference/cli/docker/container/inspect/)
- [Image inspect](https://docs.docker.com/reference/cli/docker/image/inspect/)
