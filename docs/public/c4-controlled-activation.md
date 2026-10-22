# C4 controlled activation and independent readiness

Status: implementation specification, not real Docker acceptance. This increment
is tested only with owned local fixtures. No project, image, Docker installation,
endpoint or real readiness/execution is approved by publishing this document.
Subscription integration remains paused.

## Scope and architecture

Keep the workbench and its context, plan, candidate execution and delivery
approvals. Add controls to the existing C4 task decision area, not a new workflow
or navigation section. Reuse the dormant fixed-policy adapter and the mission CAS
journal. Do not activate the legacy Docker runner, a shell, Git writes or a model
provider. Accountless fixture reasoning remains explicitly simulated even when a
future verification result comes from Docker.

New controlled missions use `C4Profile.control_version: 1`. The field is omitted
for historical synthetic missions; do not migrate/rewrite their hashes or infer
consent. New control records live in an optional `c4_controls` mission journal.
Unknown, null, corrupt or contradictory control data makes the mission read-only.
Controls cannot be grafted onto a historical profile.

There are three independent layers of authority:

1. Trusted startup configuration permits only the declared project/base, exact
   image ID, local executable/socket, frozen profile and bounded activation window.
2. A human approves one readiness material and explicitly executes that check.
3. Baseline or candidate execution still needs its own existing approval, plus
   fresh matching readiness evidence. Readiness never creates/starts a container.

Use a separate optional `--c4-control-config` file on the production launcher.
Absent configuration is disabled and performs zero Docker discovery/probes.
Configuration is strict bounded JSON, schema 1, enabled explicitly, with an
expiry no more than 24 hours from validation, and a complete profile, canonical
repository root, base commit and admitted base digest. No defaults from PATH,
Docker contexts, environment variables or HTTP are permitted. Construction and
configuration parsing are structural only: no Docker executable/socket lookup.
The configured root is compared against the existing repository import receipt;
paths alone are never approval. Activation is isolated from ordinary missions,
provider/account startup and legacy recovery. Invalid/mismatched history selects
diagnostic read-only startup, without rewriting records or invoking Docker.

The first controlled coding flow uses only the existing deterministic Double
fixture reasoning (local Planner/Worker/Debugger/QA/Auditor). Move/reuse that
implementation without a router fallback; unsupported goals stop. This is not
general autonomous model-powered coding. Real project/image admission and runtime
compatibility still require subsequent explicit user confirmation. Mock startup
cannot consume a real configuration or construct a real transport.

## Material and evidence

A version-1 control record has kind `readiness` or `cleanup`, an opaque ID,
mission ID, current epoch/plan revision, base commit/digest, profile digest,
activation digest, provenance, prepared mission revision/time and expiry.
Cleanup additionally binds the original operation ID/material digest and its
already recorded exact full container ID. The canonical material digest excludes
only lifecycle facts. Approval records that digest; dispatch is persisted before
any transport or trust-check effect. Materials expire after 10 minutes. A failed,
timed-out or consumed material cannot be approved or executed again.

Readiness results are `ready`, `failed` or `outcome_unknown`, with static bounded
failure codes and times. No raw CLI output, environment, credentials or daemon
metadata enters the journal. A ready receipt is valid for at most 10 minutes and
never past activation expiry. It binds project/base/profile/activation and the
source of the check. A fresh epoch/plan may reuse the result only when these
immutable bindings still match; approval of the readiness call itself must match
the epoch/plan at dispatch. A new readiness receipt changes execution authority,
so already prepared baseline/candidate materials cannot silently inherit it.

Controlled baseline/candidate operations bind the exact readiness record/digest.
Check receipt freshness at preparation and dispatch, not halfway through an
already dispatched operation. Loss of freshness never authorizes a retry or
automatic cleanup outside the already approved bounded lifecycle.

Status separates structural mission/runtime matching from fresh activation
availability. Expiry does not invalidate completed historical evidence or replace
the separate delivery approval, and does not itself block local planning/chat.
New baseline/candidate dispatch and all readiness/recovery-cleanup transport
actions still require their current unexpired authority. Mismatched history and
diagnostic read-only mode remain blocked; an expired flag alone grants nothing.

Synthetic control receipts and Docker control receipts are distinct. The runtime
capability, not request JSON or `mode`, determines provenance. Mock results cannot
unlock a real runtime. Docker verification evidence may use `isolated_execution`
only after real adapter dispatch and a complete validated operation; fixture
reasoning and synthetic transport evidence never receive that label.

## Independent readiness

Prepare, approve and execute are three explicit actions. Prepare/approve/page
load/startup do not inspect Docker. Execute consumes one approval durably, then
performs a bounded sequence: validate the configured executable/socket trust,
query server version/platform, query daemon identity/platform, inspect only the
approved local content-addressed image. Use fixed argv and the existing bounded
process boundary, no inherited environment, no shell/stdin. Require Linux arm64,
bounded valid metadata and the existing strict image policy. Persist only a
derived fingerprint and static outcome, not the raw daemon ID or inspect data.

No image list, pull, build, container discovery/create/start, network test or host
repository execution belongs to readiness. Unknown schema, unsupported platform,
missing image, deadline, cancellation, stale authority or persistence failure
stops. Each command is attempted once; no retries/polling/fallback. A transport
failure cannot become a ready receipt. Fixture parser tests do not prove real
Docker compatibility, compiler/verifier presence or isolation.

## Cleanup authorization before effects

Automatic cleanup within a previously approved execution keeps its existing
bounded exact-ID lifecycle. Recovery cleanup is a separate prepare → approve →
execute decision, not a repeat of an execution or a post-removal audit only.

Before recovery inspect/remove, validate under mission serialization: enabled
matching runtime; current mission revision/epoch/plan; unexpired exact cleanup
material and approval; durable terminal target operation with unconfirmed cleanup;
matching project/base/profile/activation; original managed candidate/base path;
known full ID and ownership. An old operation can be cleaned after recovery only
through a new material bound to the current mission, without reauthorizing start.
Unknown IDs cannot be typed in to discover or remove an object. Failed checks or
failed dispatch persistence mean zero trust probes, inspect or remove calls.

An expired activation cannot be reused. A newly confirmed activation may authorize
cleanup of an older operation only when the project/base/profile and transport
provenance still match, with a fresh current cleanup material and approval. Bind
that material to the new activation, not the expired readiness activation. This
permits explicit recovery after expiry without renewing readiness, start, execution
or any prior approval. Exact original ID/ownership/path checks remain mandatory.

Persist the one-shot cleanup dispatch before calling the adapter, retaining
serialization across the bounded cleanup and outcome commit (use a lock-aware
persister, not a recursive engine lock). Existing exact-owned effective-config
inspection still precedes removal. Record cleanup result separately from test
result: successful removal does not turn an unknown/failed run into a pass.
After a crash or ambiguous persistence result, mark dispatched control work
unknown; never automatically inspect/remove/replay. A further explicit recovery
attempt requires newly prepared and separately approved material.

## HTTP and workbench

Extend the existing task `/c4` endpoint with strictly discriminated control
actions. Keep revision/digest checks, bounded JSON, unknown-field rejection,
Host/Origin/session protection and diagnostic read-only behavior. Neither action
payloads nor settings can carry transport configuration, model credentials,
resource overrides or an enable switch.

The existing decision area shows configuration availability separately from
approval, material scope/expiry, ready/failed/unknown result, simulated provenance,
and pending baseline/execution approvals. Recovery shows the exact operation and
ID, never a free-form container selector. Disable stale, expired, consumed,
busy, mismatched and read-only actions. Network failure refreshes state once;
it does not repeat the mutation. Activity records use human-readable control
events. Existing synthetic history keeps its prior wording and behavior.

## Acceptance and publication gates

Local tests cover disabled zero-effects; malformed/unknown persisted controls;
immutable bindings; stale revision/epoch/base/profile/config; expired/replayed
approvals; simulated-versus-real provenance; readiness with no create/start;
fixed command/parser/trust boundaries; cancellation/deadline; failed dispatch and
result persistence; concurrent actions; cleanup-before-authorization rejection;
restart to unknown without effects; separate candidate and delivery approvals.

Use owned test binaries and injected trust/process/clock seams only. Never locate
or invoke an installed Docker executable, even for version or readiness. UI
acceptance uses a new local disposable runtime with synthetic controls. Complete
review, targeted/full/race/vet/frontend tests, clean export and scans, then push
reviewed public source to `tsumon/foundry` and check exact-SHA CI. Do not publish
private plans, runtime records, user paths, credentials, original private history
or a Release.
